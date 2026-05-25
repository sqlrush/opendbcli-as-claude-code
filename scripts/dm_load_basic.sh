#!/bin/bash
# dm_load_basic.sh — DM 基础故障注入脚本
# 用途：构造 hot row UPDATE 故障，验证 V$LOCK / V$TRXWAIT 能看到锁等待
# 用法：bash scripts/dm_load_basic.sh setup | verify | cleanup

set -u

SSH_HOST="root@47.251.30.180"
DM_USER="opendb"
DM_PASS="OpenDb2026Test"
DM_PORT="5237"
DISQL="/opt/dmdbms/bin/disql"

action="${1:-setup}"

remote() { ssh -o ConnectTimeout=20 "$SSH_HOST" "$@" 2>&1; }

run_sql() {
  local sql="$1"
  remote "sudo -u dmdba $DISQL /nolog << 'EOF'
CONN $DM_USER/\"$DM_PASS\"@LOCALHOST:$DM_PORT
$sql
EXIT;
EOF"
}

setup() {
  echo "=== 1. 创建 hot row 表 (单行计数器) ==="
  run_sql "
DROP TABLE IF EXISTS opendb.bench_dm_counter;
CREATE TABLE opendb.bench_dm_counter (
    id      INT PRIMARY KEY,
    counter BIGINT NOT NULL
);
INSERT INTO opendb.bench_dm_counter VALUES (1, 0);
COMMIT;
SELECT COUNT(*) AS row_count FROM opendb.bench_dm_counter;
"

  echo
  echo "=== 2. 启动 hot row 并发 UPDATE (10 个 worker) ==="
  # 用 base64 传 worker 脚本，避开嵌套 heredoc + 转义混乱
  cat > /tmp/_dm_worker.sh <<'WORKER'
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
  sleep 0.05
done
WORKER
  remote "mkdir -p /tmp/dm_load"
  scp -o ConnectTimeout=15 /tmp/_dm_worker.sh root@47.251.30.180:/tmp/dm_load/hot_row_worker.sh > /dev/null
  remote "chmod +x /tmp/dm_load/hot_row_worker.sh; chown dmdba /tmp/dm_load/hot_row_worker.sh"
  echo "  -- 启动 10 个 worker..."
  for i in 1 2 3 4 5 6 7 8 9 10; do
    remote "sudo -u dmdba nohup bash /tmp/dm_load/hot_row_worker.sh > /dev/null 2>&1 &"
  done
  sleep 5

  echo
  echo "造压完成 — 10 个 worker 持续抢 id=1 的行锁"
}

verify() {
  echo "=== 当前会话状态 ==="
  run_sql "
SELECT STATE, COUNT(*) AS cnt FROM V\$SESSIONS GROUP BY STATE;
"
  echo
  echo "=== 锁等待 (V\$LOCK BLOCKED=1) ==="
  run_sql "
SELECT TRX_ID, TID, LTYPE, LMODE, BLOCKED, TABLE_ID
FROM V\$LOCK WHERE BLOCKED = 1
ORDER BY TID
LIMIT 10;
"
  echo
  echo "=== 事务等待 (V\$TRXWAIT) ==="
  run_sql "
SELECT * FROM V\$TRXWAIT WHERE ROWNUM <= 10;
"
  echo
  echo "=== bench_dm_counter 当前值 ==="
  run_sql "
SELECT counter FROM opendb.bench_dm_counter WHERE id = 1;
"
}

cleanup() {
  echo "[cleanup] 杀 worker 进程..."
  remote "pkill -9 -f 'hot_row_worker.sh' 2>/dev/null || true"
  sleep 3
  echo "[cleanup] 删表..."
  run_sql "
DROP TABLE IF EXISTS opendb.bench_dm_counter;
COMMIT;
"
  remote "rm -rf /tmp/dm_load"
  echo "[cleanup] 完成."
}

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup|verify|cleanup}"; exit 1 ;;
esac
