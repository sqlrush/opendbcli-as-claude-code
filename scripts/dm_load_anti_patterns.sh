#!/bin/bash
# dm_load_anti_patterns.sh — DM 4 故障场景注入
# 用法：bash scripts/dm_load_anti_patterns.sh setup [F1|F2|F3|F4|all] | verify | cleanup
#
# F1 hot row UPDATE     — 30 worker 抢同一行 UPDATE
# F2 missing index      — 2M 行表无索引，全表扫描
# F3 long tx + lock     — 长事务持有行锁不 commit
# F4 deadlock           — 多表交叉 UPDATE 引发死锁

set -u

SSH_HOST="root@47.251.30.180"
DM_USER="opendb"
DM_PASS="OpenDb2026Test"
DM_PORT="5237"
DISQL="/opt/dmdbms/bin/disql"

action="${1:-setup}"
fault="${2:-all}"

remote() { ssh -o ConnectTimeout=20 "$SSH_HOST" "$@" 2>&1; }

run_sql() {
  local sql="$1"
  remote "sudo -u dmdba $DISQL /nolog << 'EOF'
CONN $DM_USER/\"$DM_PASS\"@LOCALHOST:$DM_PORT
$sql
EXIT;
EOF"
}

setup_tables() {
  echo "=== 1. 创建 bench_dm_counter (F1 hot row 单行) ==="
  run_sql "
DROP TABLE IF EXISTS opendb.bench_dm_counter;
CREATE TABLE opendb.bench_dm_counter (id INT PRIMARY KEY, counter BIGINT NOT NULL);
INSERT INTO opendb.bench_dm_counter VALUES (1, 0);
COMMIT;
"

  echo "=== 2. 创建 bench_dm_users (F2 missing index 大表) ==="
  run_sql "
DROP TABLE IF EXISTS opendb.bench_dm_users;
CREATE TABLE opendb.bench_dm_users (
    user_id BIGINT PRIMARY KEY,
    status SMALLINT,
    name VARCHAR(50)
);
"
  echo "  -- 插入 200 万行（PK 但 status/name 无索引）..."
  run_sql "
INSERT INTO opendb.bench_dm_users
SELECT LEVEL, MOD(LEVEL, 5), 'user_' || LEVEL
FROM DUAL CONNECT BY LEVEL <= 2000000;
COMMIT;
"

  echo "=== 3. 创建 bench_dm_a/b (F4 deadlock 两表) ==="
  run_sql "
DROP TABLE IF EXISTS opendb.bench_dm_a;
DROP TABLE IF EXISTS opendb.bench_dm_b;
CREATE TABLE opendb.bench_dm_a (id INT PRIMARY KEY, v INT);
CREATE TABLE opendb.bench_dm_b (id INT PRIMARY KEY, v INT);
INSERT INTO opendb.bench_dm_a VALUES (1, 0); INSERT INTO opendb.bench_dm_a VALUES (2, 0);
INSERT INTO opendb.bench_dm_b VALUES (1, 0); INSERT INTO opendb.bench_dm_b VALUES (2, 0);
COMMIT;
"
  echo "底层表创建完成"
}

start_f1_hot_row() {
  echo "=== F1: 启动 30 个 hot row UPDATE worker ==="
  remote "mkdir -p /tmp/dm_load"
  cat > /tmp/_f1_worker.sh <<'WORKER'
#!/bin/bash
DISQL=/opt/dmdbms/bin/disql
while true; do
  cat <<SQL | $DISQL /nolog > /dev/null 2>&1
CONN opendb/"OpenDb2026Test"@LOCALHOST:5237
BEGIN
  UPDATE opendb.bench_dm_counter SET counter = counter + 1 WHERE id = 1;
  COMMIT;
END;
/
EXIT;
SQL
done
WORKER
  scp -o ConnectTimeout=15 /tmp/_f1_worker.sh root@47.251.30.180:/tmp/dm_load/f1_worker.sh > /dev/null
  remote "chmod +x /tmp/dm_load/f1_worker.sh; chown dmdba /tmp/dm_load/f1_worker.sh"
  for i in $(seq 1 30); do
    remote "sudo -u dmdba nohup bash /tmp/dm_load/f1_worker.sh > /dev/null 2>&1 &"
  done
  echo "F1 launched: 30 worker"
}

start_f2_missing_index() {
  echo "=== F2: 启动 8 个 missing-index 全表扫 worker ==="
  remote "mkdir -p /tmp/dm_load"
  cat > /tmp/_f2_worker.sh <<'WORKER'
#!/bin/bash
DISQL=/opt/dmdbms/bin/disql
while true; do
  cat <<SQL | $DISQL /nolog > /dev/null 2>&1
CONN opendb/"OpenDb2026Test"@LOCALHOST:5237
SELECT /* dm_anti_f2_missing_index */ COUNT(*) FROM opendb.bench_dm_users WHERE status = 3;
EXIT;
SQL
  sleep 0.3
done
WORKER
  scp -o ConnectTimeout=15 /tmp/_f2_worker.sh root@47.251.30.180:/tmp/dm_load/f2_worker.sh > /dev/null
  remote "chmod +x /tmp/dm_load/f2_worker.sh; chown dmdba /tmp/dm_load/f2_worker.sh"
  for i in $(seq 1 8); do
    remote "sudo -u dmdba nohup bash /tmp/dm_load/f2_worker.sh > /dev/null 2>&1 &"
  done
  echo "F2 launched: 8 worker"
}

start_f3_long_tx() {
  echo "=== F3: 启动 5 个长事务持锁 worker ==="
  remote "mkdir -p /tmp/dm_load"
  cat > /tmp/_f3_worker.sh <<'WORKER'
#!/bin/bash
DISQL=/opt/dmdbms/bin/disql
while true; do
  ROW_ID=$((RANDOM % 100 + 1))
  cat <<SQL | $DISQL /nolog > /dev/null 2>&1
CONN opendb/"OpenDb2026Test"@LOCALHOST:5237
BEGIN
  UPDATE opendb.bench_dm_users SET status = MOD(status + 1, 5) WHERE user_id = $ROW_ID;
  DBMS_LOCK.SLEEP(30);
  COMMIT;
END;
/
EXIT;
SQL
done
WORKER
  scp -o ConnectTimeout=15 /tmp/_f3_worker.sh root@47.251.30.180:/tmp/dm_load/f3_worker.sh > /dev/null
  remote "chmod +x /tmp/dm_load/f3_worker.sh; chown dmdba /tmp/dm_load/f3_worker.sh"
  for i in $(seq 1 5); do
    remote "sudo -u dmdba nohup bash /tmp/dm_load/f3_worker.sh > /dev/null 2>&1 &"
  done
  echo "F3 launched: 5 worker (each holds row lock 30s)"
}

start_f4_deadlock() {
  echo "=== F4: 启动 8 个交叉 UPDATE 死锁 worker ==="
  remote "mkdir -p /tmp/dm_load"
  cat > /tmp/_f4_worker_a.sh <<'WORKER'
#!/bin/bash
DISQL=/opt/dmdbms/bin/disql
while true; do
  cat <<SQL | $DISQL /nolog > /dev/null 2>&1
CONN opendb/"OpenDb2026Test"@LOCALHOST:5237
BEGIN
  UPDATE opendb.bench_dm_a SET v = v + 1 WHERE id = 1;
  DBMS_LOCK.SLEEP(0.5);
  UPDATE opendb.bench_dm_b SET v = v + 1 WHERE id = 1;
  COMMIT;
END;
/
EXIT;
SQL
done
WORKER
  cat > /tmp/_f4_worker_b.sh <<'WORKER'
#!/bin/bash
DISQL=/opt/dmdbms/bin/disql
while true; do
  cat <<SQL | $DISQL /nolog > /dev/null 2>&1
CONN opendb/"OpenDb2026Test"@LOCALHOST:5237
BEGIN
  UPDATE opendb.bench_dm_b SET v = v + 1 WHERE id = 1;
  DBMS_LOCK.SLEEP(0.5);
  UPDATE opendb.bench_dm_a SET v = v + 1 WHERE id = 1;
  COMMIT;
END;
/
EXIT;
SQL
done
WORKER
  scp -o ConnectTimeout=15 /tmp/_f4_worker_a.sh root@47.251.30.180:/tmp/dm_load/f4_worker_a.sh > /dev/null
  scp -o ConnectTimeout=15 /tmp/_f4_worker_b.sh root@47.251.30.180:/tmp/dm_load/f4_worker_b.sh > /dev/null
  remote "chmod +x /tmp/dm_load/f4_worker_*.sh; chown dmdba /tmp/dm_load/f4_worker_*.sh"
  for i in 1 2 3 4; do
    remote "sudo -u dmdba nohup bash /tmp/dm_load/f4_worker_a.sh > /dev/null 2>&1 &"
    remote "sudo -u dmdba nohup bash /tmp/dm_load/f4_worker_b.sh > /dev/null 2>&1 &"
  done
  echo "F4 launched: 8 worker (4×A + 4×B 反向 UPDATE 必死锁)"
}

setup() {
  setup_tables
  case "$fault" in
    F1)  start_f1_hot_row ;;
    F2)  start_f2_missing_index ;;
    F3)  start_f3_long_tx ;;
    F4)  start_f4_deadlock ;;
    all) start_f1_hot_row; start_f2_missing_index; start_f3_long_tx; start_f4_deadlock ;;
    *)   echo "Usage: setup [F1|F2|F3|F4|all]"; exit 1 ;;
  esac
  sleep 5
  echo
  echo "故障已启动. 用 verify 看现场状态. /llm 跑诊断."
}

verify() {
  echo "=== 当前 DM 状态快照 ==="
  run_sql "
SELECT '---active sessions---' AS h FROM DUAL;
SELECT STATE, COUNT(*) AS cnt FROM V\$SESSIONS GROUP BY STATE;
SELECT '---blocked locks---' AS h FROM DUAL;
SELECT TRX_ID, BLOCKED, LTYPE, LMODE, TABLE_ID FROM V\$LOCK WHERE BLOCKED = 1 LIMIT 20;
SELECT '---deadlock count---' AS h FROM DUAL;
SELECT COUNT(*) AS deadlock_total FROM V\$DEADLOCK_HISTORY;
SELECT '---long sql---' AS h FROM DUAL;
SELECT COUNT(*) AS long_sql FROM V\$LONG_EXEC_SQLS;
SELECT '---bench_dm_counter---' AS h FROM DUAL;
SELECT counter FROM opendb.bench_dm_counter WHERE id = 1;
"
}

cleanup() {
  echo "[cleanup] 杀所有 worker..."
  remote "pkill -9 -f 'f[1-4]_worker' 2>/dev/null || true"
  sleep 3
  echo "[cleanup] 删表 + 清 worker 文件..."
  run_sql "
DROP TABLE IF EXISTS opendb.bench_dm_counter;
DROP TABLE IF EXISTS opendb.bench_dm_users;
DROP TABLE IF EXISTS opendb.bench_dm_a;
DROP TABLE IF EXISTS opendb.bench_dm_b;
COMMIT;
"
  remote "rm -rf /tmp/dm_load"
  echo "[cleanup] 完成."
}

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup [F1|F2|F3|F4|all]|verify|cleanup}"; exit 1 ;;
esac
