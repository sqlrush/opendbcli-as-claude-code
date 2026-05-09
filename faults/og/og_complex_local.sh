#!/bin/bash
# og_complex_local.sh — 本地 docker og5 容器复合饱和故障
#
# 目标实例: 本地 docker `og5` 容器 (openGauss-lite 5.0.3, max_connections=200)
# 配置见: ~/database/README.md
#
# 6 类相互放大的故障 (饱和强度, 总连接 ~180/200):
#   F1 连接洪水           — 80 idle 会话占连接槽
#   F2 idle-in-tx 长事务  — 30 个 BEGIN; pg_sleep(7200) 持 xmin, 阻 autovacuum
#   F3 CPU 饱和           — 12 个 cross join + md5 worker 跑满 4 vcpu
#   F4 I/O 饱和           — 12 个 1000 万行 Seq Scan 反复扫, buffer cache 抖
#   F5 行锁串行           — 30 个 UPDATE 同一热行, 排队拉高 wait event
#   F6 WAL 压力           — 12 个 INSERT/UPDATE 高频写, WAL 累积
#
# 总会话 ≈ 80+30+12+12+30+12 = 176, 接近 max_connections=200 上限
#
# 用法:
#   bash og_complex_local.sh setup    # 起负载 (异步, 立即返回)
#   bash og_complex_local.sh verify   # 检查 6 类症状是否触发
#   bash og_complex_local.sh cleanup  # 终止所有故障会话 + drop 测试表
#
# 验证 dbaa 是否抓到:
#   dbaa -c og /health
#   dbaa -c og /llm "诊断当前最严重的问题, 给出修复 SQL"

set -u

OG_CONTAINER="og5"
DB_NAME="postgres"
GSQL_CMD="su - omm -c \"/usr/local/opengauss/bin/gsql -d ${DB_NAME}\""

# ── helpers ──────────────────────────────────────────────────────────────────

# Run SQL in container as omm via local socket (trust auth, no password)
gsql_exec() {
  docker exec "$OG_CONTAINER" su - omm -c \
    "/usr/local/opengauss/bin/gsql -d ${DB_NAME} -c \"$1\"" 2>&1
}

# Run multi-statement SQL via stdin
gsql_stdin() {
  docker exec -i "$OG_CONTAINER" su - omm -c \
    "/usr/local/opengauss/bin/gsql -d ${DB_NAME}" 2>&1
}

# Fire a detached session tagged with application_name (won't block this script)
fire_session() {
  local app_name="$1"
  local sql="$2"
  docker exec -d "$OG_CONTAINER" su - omm -c \
    "/usr/local/opengauss/bin/gsql -d ${DB_NAME} -c \"SET application_name='${app_name}'; ${sql}\"" \
    >/dev/null 2>&1
}

# ── schema setup ─────────────────────────────────────────────────────────────

setup_schema() {
  echo "[setup] 建测试表 (10M 行 fault_io 约需 30s)..."
  gsql_stdin <<'EOF'
DROP TABLE IF EXISTS fault_io;
DROP TABLE IF EXISTS fault_cpu;
DROP TABLE IF EXISTS fault_lock;
DROP TABLE IF EXISTS fault_wal;

-- F4 I/O target: 10M rows, no index on payload
CREATE TABLE fault_io (
  id bigserial PRIMARY KEY,
  k bigint,
  payload text
);
INSERT INTO fault_io (k, payload)
SELECT i, md5(random()::text) || md5((i % 7)::text)
FROM generate_series(1, 10000000) i;

-- F3 CPU target: 5000-row table for cross join (5000 * 5000 = 25M tuples)
CREATE TABLE fault_cpu (x int);
INSERT INTO fault_cpu SELECT generate_series(1, 5000);

-- F5 Lock target: single hot row
CREATE TABLE fault_lock (id int PRIMARY KEY, counter bigint);
INSERT INTO fault_lock VALUES (1, 0);

-- F6 WAL target: small table churned heavily
CREATE TABLE fault_wal (
  id bigserial PRIMARY KEY,
  tag text,
  ts timestamp DEFAULT now()
);

ANALYZE fault_io;
ANALYZE fault_cpu;
ANALYZE fault_lock;
ANALYZE fault_wal;

\echo === schema ready ===
SELECT 'fault_io rows', count(*) FROM fault_io
UNION ALL SELECT 'fault_cpu rows', count(*) FROM fault_cpu;
EOF
}

# ── fault firing ─────────────────────────────────────────────────────────────

fire_F1_idle() {
  echo "[setup] F1: 60 个 idle 会话"
  for i in $(seq 1 60); do
    fire_session "fault_F1_idle_${i}" "SELECT pg_sleep(7200);"
  done
}

fire_F2_idletx() {
  echo "[setup] F2: 20 个长事务 (持 xmin 阻 autovacuum)"
  # 注意: 这里 session 状态会显示为 'active' (pg_sleep 在跑), 不是 'idle in transaction'
  # 但事务效果相同 — 持 xmin 阻 autovacuum, 关联问题与真正 idle-in-tx 一致
  # 真正的 'idle in transaction' 需要客户端 hold 连接, gsql -c 模式无法实现
  for i in $(seq 1 20); do
    fire_session "fault_F2_idletx_${i}" \
      "BEGIN; SELECT 1; SELECT pg_sleep(7200);"
  done
}

## 注意 ##: $$ dollar-quote 在 4 层 shell 传递中被容器 sh 展开成 PID。
## 必须用 \\\$\\\$ 三层 escape, 才能让 gsql 最终看到真正的 $$。

fire_F3_cpu() {
  echo "[setup] F3: 8 个 CPU 饱和 worker (cross join + md5)"
  for i in $(seq 1 8); do
    # 5000 × 5000 = 2500 万 tuple, 每行 md5 计算, 反复 100 次
    fire_session "fault_F3_cpu_${i}" \
      "DO \\\$\\\$BEGIN FOR k IN 1..100 LOOP PERFORM count(*) FROM fault_cpu a CROSS JOIN fault_cpu b WHERE md5((a.x*b.x)::text) > '0'; END LOOP; END\\\$\\\$;"
  done
}

fire_F4_io() {
  echo "[setup] F4: 8 个 I/O 饱和 worker (1000 万行 Seq Scan 循环)"
  for i in $(seq 1 8); do
    fire_session "fault_F4_io_${i}" \
      "DO \\\$\\\$BEGIN FOR k IN 1..200 LOOP PERFORM count(*) FROM fault_io WHERE payload LIKE '%abc%'; END LOOP; END\\\$\\\$;"
  done
}

fire_F5_lock() {
  echo "[setup] F5: 25 个并发 UPDATE 同一热行 (id=1) - 100k 次循环"
  for i in $(seq 1 25); do
    fire_session "fault_F5_lock_${i}" \
      "DO \\\$\\\$BEGIN FOR k IN 1..100000 LOOP UPDATE fault_lock SET counter=counter+1 WHERE id=1; END LOOP; END\\\$\\\$;"
  done
}

fire_F6_wal() {
  echo "[setup] F6: 8 个 WAL 压力 worker (INSERT + UPDATE 循环) - 200k 次"
  for i in $(seq 1 8); do
    fire_session "fault_F6_wal_${i}" \
      "DO \\\$\\\$DECLARE r bigint; BEGIN FOR k IN 1..200000 LOOP INSERT INTO fault_wal(tag) VALUES (md5(random()::text)) RETURNING id INTO r; UPDATE fault_wal SET tag=md5(random()::text) WHERE id=r; END LOOP; END\\\$\\\$;"
  done
}

setup_all() {
  setup_schema
  echo
  fire_F1_idle
  fire_F2_idletx
  fire_F3_cpu
  fire_F4_io
  fire_F5_lock
  fire_F6_wal
  echo
  echo "[setup] 全部 6 类故障已发射 (~129 会话, 留 70+ 槽位安全余量)"
  echo "[setup] 等 10 秒让症状显现, 然后跑: bash $0 verify"
  echo "[setup] 或直接看 dbaa 视角: dbaa -c og /health"
}

# 安全上限: 总会话超此值就不补 (防止 daemon 解析 bug 时的雪崩)
SAFE_TOTAL_CAP=170

# 持续模式: 起 6 类故障 + 监测循环, F3-F6 掉了就补
# F1/F2 用 pg_sleep(7200) 自带 2h 寿命, 不主动补
daemon_loop() {
  setup_all
  echo
  echo "[daemon] 进入持续模式 — 每 30 秒巡检一次, 故障掉了自动补"
  echo "[daemon] 安全上限: 总会话 > ${SAFE_TOTAL_CAP} 时跳过补救 (防止雪崩)"
  echo "[daemon] 停止: bash $0 cleanup (或 kill 本进程)"
  echo

  trap 'echo; echo "[daemon] 收到 SIGINT/SIGTERM, 退出 (会话保留, 用 cleanup 清场)"; exit 0' INT TERM

  while true; do
    sleep 30

    # 用 here-doc 而不是嵌套引号, 避免 4 层 shell 转义错误
    # 用子查询包裹, 避免 OG 报 'aggregates not allowed in GROUP BY clause'
    local counts
    counts=$(docker exec -i "$OG_CONTAINER" su - omm -c "/usr/local/opengauss/bin/gsql -d ${DB_NAME} -tA" 2>/dev/null <<'SQL'
SELECT f || ':' || c FROM (
  SELECT split_part(application_name,'_',2) AS f, count(*) AS c
  FROM pg_stat_activity
  WHERE application_name LIKE 'fault_%'
  GROUP BY split_part(application_name,'_',2)
) t;
SQL
)

    local f1 f2 f3 f4 f5 f6 total
    f1=$(echo "$counts" | grep "^F1:" | cut -d: -f2); f1=${f1:-0}
    f2=$(echo "$counts" | grep "^F2:" | cut -d: -f2); f2=${f2:-0}
    f3=$(echo "$counts" | grep "^F3:" | cut -d: -f2); f3=${f3:-0}
    f4=$(echo "$counts" | grep "^F4:" | cut -d: -f2); f4=${f4:-0}
    f5=$(echo "$counts" | grep "^F5:" | cut -d: -f2); f5=${f5:-0}
    f6=$(echo "$counts" | grep "^F6:" | cut -d: -f2); f6=${f6:-0}
    total=$((f1+f2+f3+f4+f5+f6))

    echo "[daemon $(date +%H:%M:%S)] F1=$f1 F2=$f2 F3=$f3 F4=$f4 F5=$f5 F6=$f6  total=$total"

    # 硬限保护: 总数过高就跳过本轮补救
    if [ "$total" -gt "$SAFE_TOTAL_CAP" ]; then
      echo "  ⚠ total $total > $SAFE_TOTAL_CAP, 跳过本轮补救"
      continue
    fi

    # F3-F6 (短命周期) 掉到目标 80% 以下就补满
    [ "$f3" -lt 7 ]  && { echo "  → 补 F3 CPU";  fire_F3_cpu; }
    [ "$f4" -lt 7 ]  && { echo "  → 补 F4 I/O";  fire_F4_io; }
    [ "$f5" -lt 20 ] && { echo "  → 补 F5 Lock"; fire_F5_lock; }
    [ "$f6" -lt 7 ]  && { echo "  → 补 F6 WAL";  fire_F6_wal; }

    # F1/F2 用 7200s pg_sleep, 极少需要补; 异常时兜底
    [ "$f1" -lt 50 ] && { echo "  → 补 F1 idle (异常掉太多)";    fire_F1_idle; }
    [ "$f2" -lt 16 ] && { echo "  → 补 F2 long-tx (异常掉太多)"; fire_F2_idletx; }
  done
}

# ── verification ─────────────────────────────────────────────────────────────

verify() {
  echo "=== 当前连接占用 (vs max_connections=200) ==="
  gsql_exec "SELECT count(*) AS sessions, (SELECT setting::int FROM pg_settings WHERE name='max_connections') AS max FROM pg_stat_activity;"
  echo
  echo "=== 按故障 tag 分组 ==="
  gsql_exec "SELECT split_part(application_name,'_',2) AS fault, state, count(*) FROM pg_stat_activity WHERE application_name LIKE 'fault_%' GROUP BY 1,2 ORDER BY 1,2;"
  echo
  echo "=== Top 5 wait events (OG 用 pg_thread_wait_status) ==="
  gsql_exec "SELECT wait_status, count(*) FROM pg_thread_wait_status WHERE wait_status NOT IN ('none','wait cmd') GROUP BY 1 ORDER BY count(*) DESC LIMIT 5;"
  echo
  echo "=== fault_lock 行被更新次数 (热行争用证据) ==="
  gsql_exec "SELECT counter FROM fault_lock WHERE id=1;"
  echo
  echo "=== fault_wal 累积 dead tuples (autovacuum 被阻证据, 几分钟后才显著) ==="
  gsql_exec "SELECT relname, n_live_tup, n_dead_tup, last_autovacuum FROM pg_stat_user_tables WHERE relname LIKE 'fault_%';"
  echo
  echo "=== 容器 CPU/MEM ==="
  docker stats --no-stream "$OG_CONTAINER" 2>&1 | tail -2
}

# ── cleanup ──────────────────────────────────────────────────────────────────

cleanup() {
  echo "[cleanup] 终止所有 fault_* 会话..."
  gsql_exec "SELECT count(*) AS killed FROM (SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE 'fault_%' AND pid != pg_backend_pid()) t;"
  echo
  echo "[cleanup] 等待会话完全退出..."
  sleep 3
  echo
  echo "[cleanup] 删除测试表..."
  gsql_stdin <<'EOF'
DROP TABLE IF EXISTS fault_io;
DROP TABLE IF EXISTS fault_cpu;
DROP TABLE IF EXISTS fault_lock;
DROP TABLE IF EXISTS fault_wal;
EOF
  echo
  echo "[cleanup] 残留 fault_* 会话:"
  gsql_exec "SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'fault_%';"
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

  setup    起 6 类故障负载 (~176 会话, 异步, 起完即返回)
  daemon   持续模式: 起 6 类负载 + 每 30s 巡检自动补, 不停 (Ctrl-C 退出)
  verify   查 6 类症状是否触发
  cleanup  终止所有 fault_* 会话 + drop 测试表

后续验证:
  dbaa -c og /health
  dbaa -c og /llm "诊断当前最严重的问题"
USAGE
    ;;
esac
