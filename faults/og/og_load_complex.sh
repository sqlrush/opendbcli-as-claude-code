#!/bin/bash
# og_load_complex.sh — 为 v1.1.09 benchmark 构造 OpenGauss 复杂诊断场景
#
# 触发的 6 个场景：
#   1) XID wraparound 风险 + VACUUM 被长事务阻塞
#   2) Index bloat 导致查询退化到 SeqScan
#   3) idle in transaction 堆积（连接泄漏模拟）
#   4) WAL 冲高 + max_wal_size 过小 → checkpoint 过频
#   5) 统计信息过期导致 planner 走错计划
#   8) TOAST bloat（heap 已 VACUUM 但 TOAST 未回收）
#
# 用法:
#   bash scripts/og_load_complex.sh setup    # 造压
#   bash scripts/og_load_complex.sh verify   # 查症状是否触发
#   bash scripts/og_load_complex.sh cleanup  # 清理
#
# 环境: 47.251.30.180:15432, gauss:OpenGauss@2026 (本地管理用; 远程用 opendb:GaussPass012new)
# ssh root@47.251.30.180 免密

set -u

SSH_HOST="root@47.251.30.180"
GSQL='/opt/opengauss/install/bin/gsql -d postgres -p 15432'
GAUSS_RUN="su - gauss -c"

action="${1:-setup}"

# ── helpers ──

remote() {
  ssh -o ConnectTimeout=10 "$SSH_HOST" "$@" 2>&1
}

psql_on_og() {
  remote "$GAUSS_RUN '$GSQL -c \"$1\"'"
}

psql_on_og_file() {
  # Stream a SQL file via stdin (for multi-statement blocks)
  ssh "$SSH_HOST" "$GAUSS_RUN '$GSQL'" <<EOSQL
$1
EOSQL
}

# ── setup ──

setup() {
  echo "[1/6] XID wraparound + VACUUM 阻塞 (长事务 + 大表 UPDATE)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_t1;
CREATE TABLE bench_t1 (id SERIAL PRIMARY KEY, data TEXT);
INSERT INTO bench_t1 SELECT i, md5(random()::text) FROM generate_series(1,500000) i;
UPDATE bench_t1 SET data = md5(random()::text) WHERE id < 50000;
"
  # Background long transaction (25 min) holding xmin to block VACUUM.
  remote "$GAUSS_RUN 'nohup bash -c \"( echo \\\"BEGIN; SELECT 1; SELECT pg_sleep(1500);\\\" | $GSQL ) \" > /tmp/bench_t1_longtx.log 2>&1 &'"
  sleep 2

  echo "[2/6] Index bloat (关闭 autovacuum + 反复 UPDATE)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_t2 CASCADE;
CREATE TABLE bench_t2 (uid INT, status INT, payload TEXT) WITH (autovacuum_enabled = false);
CREATE INDEX idx_bench_t2_uid ON bench_t2(uid);
INSERT INTO bench_t2 SELECT i, 1, md5(i::text) FROM generate_series(1,500000) i;
UPDATE bench_t2 SET status = status + 1;
UPDATE bench_t2 SET status = status + 1;
UPDATE bench_t2 SET status = status + 1;
UPDATE bench_t2 SET status = status + 1;
UPDATE bench_t2 SET status = status + 1;
"

  echo "[3/6] idle in transaction 堆积 (50 个后台会话)..."
  for i in $(seq 1 50); do
    remote "$GAUSS_RUN 'nohup bash -c \"( echo \\\"BEGIN; SELECT 1; SELECT pg_sleep(1500);\\\" | $GSQL ) \" > /dev/null 2>&1 &'"
  done
  sleep 3

  echo "[4/6] WAL 冲高 (max_wal_size=80MB + 大批量 INSERT)..."
  psql_on_og_file "
ALTER SYSTEM SET max_wal_size = '80MB';
SELECT pg_reload_conf();
DROP TABLE IF EXISTS bench_t4;
CREATE TABLE bench_t4 (id SERIAL, data TEXT);
INSERT INTO bench_t4 SELECT i, repeat(md5(i::text), 10) FROM generate_series(1,1000000) i;
"

  echo "[5/6] 统计信息过期 (大表建索引但不 ANALYZE)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_t5 CASCADE;
CREATE TABLE bench_t5 (uid INT, name TEXT) WITH (autovacuum_enabled = false);
CREATE INDEX idx_bench_t5_uid ON bench_t5(uid);
INSERT INTO bench_t5 SELECT i, md5(i::text) FROM generate_series(1,1000000) i;
"

  echo "[6/6] TOAST bloat (宽表反复 UPDATE 大字段; heap VACUUM 过但 TOAST 不收缩)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_t8 CASCADE;
CREATE TABLE bench_t8 (id SERIAL PRIMARY KEY, content TEXT);
INSERT INTO bench_t8 SELECT i, repeat(md5(i::text), 200) FROM generate_series(1,10000) i;
UPDATE bench_t8 SET content = repeat(md5(random()::text), 200);
UPDATE bench_t8 SET content = repeat(md5(random()::text), 200);
UPDATE bench_t8 SET content = repeat(md5(random()::text), 200);
UPDATE bench_t8 SET content = repeat(md5(random()::text), 200);
VACUUM bench_t8;
"
  echo "造压完成。用 verify 检查症状是否触发。"
}

# ── verify ──

verify() {
  echo "=== 场景 1: XID age ==="
  psql_on_og "SELECT datname, age(datfrozenxid) AS xid_age FROM pg_database ORDER BY xid_age DESC LIMIT 5;"
  echo ""
  echo "=== 场景 1/3: 长事务 + idle in transaction 数量 ==="
  psql_on_og "SELECT state, COUNT(*) FROM pg_stat_activity WHERE state IS NOT NULL GROUP BY state;"
  echo ""
  echo "=== 场景 2: bench_t2 dead tuple 比例 ==="
  psql_on_og "SELECT relname, n_live_tup, n_dead_tup, ROUND(100.0*n_dead_tup/NULLIF(n_live_tup,0), 1) AS dead_pct FROM pg_stat_all_tables WHERE relname LIKE 'bench_%';"
  echo ""
  echo "=== 场景 4: max_wal_size 当前值 + checkpoint 状态 ==="
  psql_on_og "SHOW max_wal_size;"
  psql_on_og "SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter;"
  echo ""
  echo "=== 场景 5: bench_t5 execution plan (应为 SeqScan) ==="
  psql_on_og "EXPLAIN SELECT * FROM bench_t5 WHERE uid = 123;"
  echo ""
  echo "=== 场景 8: bench_t8 TOAST 大小 ==="
  psql_on_og "SELECT pg_size_pretty(pg_relation_size('bench_t8')) AS main, pg_size_pretty(pg_total_relation_size('bench_t8')) AS total;"
}

# ── cleanup ──

cleanup() {
  echo "[cleanup] 终止长事务和 idle 连接..."
  remote "pkill -f 'pg_sleep(1500)' || true"
  sleep 1

  echo "[cleanup] 删除 bench_ 表 + 重置 max_wal_size..."
  psql_on_og_file "
ALTER SYSTEM RESET max_wal_size;
SELECT pg_reload_conf();
DROP TABLE IF EXISTS bench_t1, bench_t2, bench_t4, bench_t5, bench_t8 CASCADE;
"
  echo "[cleanup] 完成。"
}

# ── main ──

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup|verify|cleanup}"; exit 1 ;;
esac
