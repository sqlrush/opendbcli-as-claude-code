#!/bin/bash
# dm_complex_local.sh — 本地 docker dm8 容器复合饱和故障
#
# 目标实例: 本地 docker `dm8` 容器 (DM Database V8, port 5236, SYSDBA/SYSDBA)
# 配置见: ~/database/README.md, ~/database/dameng/
#
# 6 类相互放大的故障 (饱和强度):
#   F1 idle 连接洪水         — 60 个 dbms_lock.sleep(7200) 会话占连接
#   F2 长事务持锁            — 20 个 BEGIN+UPDATE+sleep, 持 trx 阻 purge
#   F3 CPU 饱和              — 8 个 CONNECT BY LEVEL + MD5 计算 worker
#   F4 I/O 饱和              — 8 个 100 万行 fault_io 全表扫循环
#   F5 行锁串行              — 25 个 UPDATE 同热行 worker
#   F6 redo 压力             — 8 个 INSERT+COMMIT 高频写 worker
#
# 总会话 ≈ 60+20+8+8+25+8 = 129 (留 ~120 安全余量)
#
# 标签: 所有故障会话用 dbms_application_info.set_module('fault_F<N>', ...)
# 清场: SP_CLOSE_SESSION(SESS_ID) WHERE MODULE LIKE 'fault_%'
#
# 用法:
#   bash dm_complex_local.sh setup     # 起 6 类故障 (异步, 立即返回)
#   bash dm_complex_local.sh daemon    # 持续模式: 30s 巡检自动补
#   bash dm_complex_local.sh verify    # 查 6 类症状是否触发
#   bash dm_complex_local.sh cleanup   # 终止 fault_* 会话 + drop 测试表

set -u

DM_CONTAINER="dm8"
DM_USER="SYSDBA"
DM_PWD="SYSDBA"
DISQL_PREFIX="cd /home/dmdba/dmdbms/bin && LD_LIBRARY_PATH=/home/dmdba/dmdbms/bin ./disql ${DM_USER}/${DM_PWD}"

# ── helpers ──────────────────────────────────────────────────────────────────

# Run SQL in container via disql (multi-statement OK, accepts heredoc on stdin)
disql_exec() {
  docker exec -i "$DM_CONTAINER" bash -c "$DISQL_PREFIX" 2>&1
}

# Fire a detached session — write SQL to a temp file in container, run via disql \`file
fire_session() {
  local fault_id="$1"        # e.g. fault_F1
  local idx="$2"             # session index (for unique file path)
  local sql_body="$3"        # SQL statement(s) to execute (after set_module)

  local sqlfile="/tmp/${fault_id}_${idx}.sql"

  # write SQL to container; first statement tags MODULE for accountability
  # IMPORTANT: -i needed to attach stdin (heredoc) to the container process
  docker exec -i "$DM_CONTAINER" bash -c "cat > ${sqlfile}" <<EOF
SET HEADING OFF;
SET FEEDBACK OFF;
CALL dbms_application_info.set_module('${fault_id}', NULL);
${sql_body}
EOF

  # detached fire — disql runs the file then session exits
  docker exec -d "$DM_CONTAINER" bash -c \
    "${DISQL_PREFIX} \\\`${sqlfile}" >/dev/null 2>&1
}

# ── schema setup ─────────────────────────────────────────────────────────────

setup_schema() {
  echo "[setup] 建测试表 (200K 行 fault_io, 分 20 批 commit, 避免 OOM)..."
  disql_exec <<'EOF'
SET FEEDBACK OFF;

DROP TABLE IF EXISTS fault_io;
CREATE TABLE fault_io (id BIGINT IDENTITY(1,1) PRIMARY KEY, k BIGINT, payload VARCHAR(200));

-- 分 20 批 × 10K 插入, 每批 commit (避免单事务撑爆 undo)
-- 200K 行 × 100B = 20MB, 加索引/版本/redo 实际占 ~80MB, 安全
BEGIN
  FOR batch IN 1..20 LOOP
    INSERT INTO fault_io(k, payload)
      SELECT LEVEL, RPAD(MD5(RAND()::VARCHAR(20)), 100, '_')
      FROM DUAL CONNECT BY LEVEL <= 10000;
    COMMIT;
  END LOOP;
END;
/

DROP TABLE IF EXISTS fault_lock;
CREATE TABLE fault_lock (id INT PRIMARY KEY, counter BIGINT);
INSERT INTO fault_lock VALUES (1, 0);
COMMIT;

DROP TABLE IF EXISTS fault_wal;
CREATE TABLE fault_wal (id BIGINT IDENTITY(1,1) PRIMARY KEY, tag VARCHAR(64), ts DATETIME);

SELECT 'fault_io rows' AS metric, COUNT(*) AS value FROM fault_io
UNION ALL SELECT 'fault_lock rows', COUNT(*) FROM fault_lock;
EOF
}

# ── fault firing ─────────────────────────────────────────────────────────────

fire_F1_idle() {
  echo "[setup] F1: 40 个 idle 会话 (dbms_lock.sleep 7200s)"
  for i in $(seq 1 40); do
    fire_session "fault_F1" "$i" "CALL dbms_lock.sleep(7200);"
  done
}

fire_F2_longtx() {
  echo "[setup] F2: 15 个长事务 (持 trx)"
  for i in $(seq 1 15); do
    fire_session "fault_F2" "$i" "
SET AUTOCOMMIT OFF;
UPDATE fault_lock SET counter = counter WHERE id = 1;
CALL dbms_lock.sleep(7200);
"
  done
}

fire_F3_cpu() {
  echo "[setup] F3: 4 个 CPU 饱和 worker (CONNECT BY LEVEL + MD5, 1万次循环 ≈ 10min/worker)"
  for i in $(seq 1 4); do
    fire_session "fault_F3" "$i" "
DECLARE
  v_dummy VARCHAR(64);
BEGIN
  FOR k IN 1..10000 LOOP
    SELECT MAX(MD5(LEVEL::VARCHAR(20))) INTO v_dummy FROM DUAL CONNECT BY LEVEL <= 30000;
  END LOOP;
END;
/
"
  done
}

fire_F4_io() {
  echo "[setup] F4: 4 个 I/O 饱和 worker (200K 行全扫, 5000次循环 ≈ 10min/worker)"
  for i in $(seq 1 4); do
    fire_session "fault_F4" "$i" "
DECLARE
  v_cnt BIGINT;
BEGIN
  FOR k IN 1..5000 LOOP
    SELECT COUNT(*) INTO v_cnt FROM fault_io WHERE payload LIKE '%abc%';
  END LOOP;
END;
/
"
  done
}

fire_F5_lock() {
  echo "[setup] F5: 15 个 UPDATE 同热行 worker (id=1, 50k 次)"
  for i in $(seq 1 15); do
    fire_session "fault_F5" "$i" "
BEGIN
  FOR k IN 1..50000 LOOP
    UPDATE fault_lock SET counter = counter + 1 WHERE id = 1;
    COMMIT;
  END LOOP;
END;
/
"
  done
}

fire_F6_wal() {
  echo "[setup] F6: 4 个 redo 压力 worker (INSERT+COMMIT, 100k 次)"
  for i in $(seq 1 4); do
    fire_session "fault_F6" "$i" "
BEGIN
  FOR k IN 1..100000 LOOP
    INSERT INTO fault_wal(tag, ts) VALUES (MD5(RAND()::VARCHAR(20)), SYSDATE);
    COMMIT;
  END LOOP;
END;
/
"
  done
}

setup_all() {
  setup_schema
  echo
  fire_F1_idle
  fire_F2_longtx
  fire_F3_cpu
  fire_F4_io
  fire_F5_lock
  fire_F6_wal
  echo
  echo "[setup] 全部 6 类故障已发射 (~82 会话, 比原版减半防 OOM)"
  echo "[setup] 等 10 秒让症状显现, 跑: bash $0 verify"
  echo "[setup] dbaa 视角: bash ~/database/dbaa/run.sh dbaa -c dm /health"
}

# ── verification ─────────────────────────────────────────────────────────────

verify() {
  disql_exec <<'EOF'
SET LINESIZE 200
SET FEEDBACK OFF

-- 总连接数
SELECT '当前连接' AS metric, COUNT(*) AS value FROM V$SESSIONS;

-- 按 fault tag 分组
SELECT MODULE, COUNT(*) AS sessions
FROM V$SESSIONS
WHERE MODULE LIKE 'fault_%'
GROUP BY MODULE
ORDER BY MODULE;

-- 等待事件 / 状态分布
SELECT STATE, COUNT(*) AS cnt
FROM V$SESSIONS
WHERE MODULE LIKE 'fault_%'
GROUP BY STATE
ORDER BY cnt DESC;

-- F5 热行 counter 累积进度 (锁争用证据)
SELECT 'fault_lock counter' AS metric, counter AS value FROM fault_lock WHERE id = 1;

-- F6 WAL 表行数 (redo 写入证据)
SELECT 'fault_wal rows' AS metric, COUNT(*) AS value FROM fault_wal;

-- 锁等待
SELECT COUNT(*) AS lock_waits FROM V$LOCK WHERE BLOCKED = 1;
EOF
  echo
  echo "=== 容器 CPU/MEM ==="
  docker stats --no-stream "$DM_CONTAINER" 2>&1 | tail -2
}

# ── cleanup ──────────────────────────────────────────────────────────────────

cleanup() {
  echo "[cleanup] 终止所有 fault_* 会话 (一次 PL/SQL 块批量杀)..."
  # 改进版: 用单个 PL/SQL 块循环 SP_CLOSE_SESSION, 避免几十次 docker exec 串行开销
  disql_exec <<'EOF' | tail -5
SET FEEDBACK OFF;
SET SERVEROUTPUT ON;
DECLARE
  v_count INT := 0;
BEGIN
  FOR r IN (SELECT SESS_ID FROM V$SESSIONS WHERE MODULE LIKE 'fault_%') LOOP
    BEGIN
      SP_CLOSE_SESSION(r.SESS_ID);
      v_count := v_count + 1;
    EXCEPTION WHEN OTHERS THEN
      NULL;  -- 忽略已自然退出的会话
    END;
  END LOOP;
  PRINT '已终止 ' || v_count || ' 个 fault 会话';
END;
/
EOF

  echo "[cleanup] 删测试表 + /tmp 文件..."
  disql_exec <<'EOF' | tail -3
SET FEEDBACK OFF;
DROP TABLE IF EXISTS fault_io;
DROP TABLE IF EXISTS fault_lock;
DROP TABLE IF EXISTS fault_wal;
EOF
  docker exec "$DM_CONTAINER" bash -c "rm -f /tmp/fault_F*.sql" 2>/dev/null
  echo "[cleanup] 残留 fault_* 会话数:"
  disql_exec <<'EOF' | grep -A1 "COUNT" | tail -3
SET HEADING OFF;
SET FEEDBACK OFF;
SELECT COUNT(*) FROM V$SESSIONS WHERE MODULE LIKE 'fault_%';
EOF
}

# ── daemon ───────────────────────────────────────────────────────────────────

SAFE_TOTAL_CAP=150

daemon_loop() {
  setup_all
  echo
  echo "[daemon] 进入持续模式 — 每 30 秒巡检, 故障掉了自动补"
  echo "[daemon] 安全上限: 总会话 > ${SAFE_TOTAL_CAP} 时跳过补救"
  echo "[daemon] 停止: bash $0 cleanup"
  echo

  trap 'echo; echo "[daemon] 退出 (会话保留, 用 cleanup 清场)"; exit 0' INT TERM

  while true; do
    sleep 30

    local counts
    counts=$(docker exec -i "$DM_CONTAINER" bash -c "$DISQL_PREFIX" 2>/dev/null <<'EOF' | grep -oE 'fault_F[0-9]+ +[0-9]+'
SET HEADING OFF
SET FEEDBACK OFF
SET LINESIZE 100
SELECT MODULE, COUNT(*) FROM V$SESSIONS WHERE MODULE LIKE 'fault_%' GROUP BY MODULE;
EOF
)

    local f1 f2 f3 f4 f5 f6 total
    f1=$(echo "$counts" | grep "^fault_F1" | awk '{print $2}'); f1=${f1:-0}
    f2=$(echo "$counts" | grep "^fault_F2" | awk '{print $2}'); f2=${f2:-0}
    f3=$(echo "$counts" | grep "^fault_F3" | awk '{print $2}'); f3=${f3:-0}
    f4=$(echo "$counts" | grep "^fault_F4" | awk '{print $2}'); f4=${f4:-0}
    f5=$(echo "$counts" | grep "^fault_F5" | awk '{print $2}'); f5=${f5:-0}
    f6=$(echo "$counts" | grep "^fault_F6" | awk '{print $2}'); f6=${f6:-0}
    total=$((f1+f2+f3+f4+f5+f6))

    echo "[daemon $(date +%H:%M:%S)] F1=$f1 F2=$f2 F3=$f3 F4=$f4 F5=$f5 F6=$f6  total=$total"

    if [ "$total" -gt "$SAFE_TOTAL_CAP" ]; then
      echo "  ⚠ total $total > $SAFE_TOTAL_CAP, 跳过本轮补救"
      continue
    fi

    [ "$f3" -lt 3 ]  && { echo "  → 补 F3 CPU";  fire_F3_cpu; }
    [ "$f4" -lt 3 ]  && { echo "  → 补 F4 I/O";  fire_F4_io; }
    [ "$f5" -lt 12 ] && { echo "  → 补 F5 Lock"; fire_F5_lock; }
    [ "$f6" -lt 3 ]  && { echo "  → 补 F6 WAL";  fire_F6_wal; }
    [ "$f1" -lt 32 ] && { echo "  → 补 F1 idle (异常掉太多)";    fire_F1_idle; }
    [ "$f2" -lt 12 ] && { echo "  → 补 F2 longtx (异常掉太多)";  fire_F2_longtx; }
  done
}

# ── entrypoint ───────────────────────────────────────────────────────────────

case "${1:-help}" in
  setup)   setup_all ;;
  daemon)  daemon_loop ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  help|*)
    cat <<USAGE
用法: $0 {setup|daemon|verify|cleanup}

  setup    起 6 类故障负载 (~129 会话, 异步)
  daemon   持续模式: 30s 巡检自动补, 不停 (Ctrl-C 退出)
  verify   查 6 类症状是否触发
  cleanup  终止 fault_* 会话 + drop 测试表

后续验证 (从 dbaa 容器):
  bash ~/database/dbaa/run.sh dbaa -c dm /health
  bash ~/database/dbaa/run.sh dbaa -c dm /llm "诊断当前最严重的问题"
USAGE
    ;;
esac
