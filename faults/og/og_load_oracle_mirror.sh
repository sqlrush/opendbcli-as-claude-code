#!/bin/bash
# og_load_oracle_mirror.sh — 在 OG 上构造与 Oracle 复合故障同形态的问题
#
# 目标: 让 OG /llm 拿到与 Oracle 测试同等粒度的复合症状, 验证 v1.1.12 修复
# (/health 不再吞 Maintenance 段) 后, OG 能否产出与 Oracle 同水平的诊断.
#
# 触发的 5 个症状 (一一对应 Oracle 测试里 LLM 识别出的 5 项):
#   #1 单 SQL 主导 CPU         — 40 并发跑同一条 bench_og_hot 全扫, ASH 集中
#   #2 历史行锁争用 (已释放)   — 5 burst UPDATE 同一行 then COMMIT
#                                现在 blocktree 看不到, 但累计统计在
#   #3 ~50 活跃会话           — #1 自然带来的并发
#   #4 多个无效对象           — 创建 view 后 DROP 依赖列
#   #5 慢 SQL >2s            — bench_og_hot 全扫单次 ~3-5s
#
# 用法:
#   bash scripts/og_load_oracle_mirror.sh setup
#   bash scripts/og_load_oracle_mirror.sh verify
#   bash scripts/og_load_oracle_mirror.sh cleanup

set -u

SSH_HOST="root@47.251.30.180"
GSQL='/opt/opengauss/install/bin/gsql -d postgres -p 15432'
GAUSS_RUN="su - gauss -c"
HOT_SQL_TAG="bench_og_hot_seqscan_marker"  # 用注释 marker 让 dbe_perf 识别为同一 SQL

action="${1:-setup}"

remote() { ssh -o ConnectTimeout=20 "$SSH_HOST" "$@" 2>&1; }
psql_on_og() { remote "$GAUSS_RUN '$GSQL -c \"$1\"'"; }
psql_on_og_file() {
  ssh "$SSH_HOST" "$GAUSS_RUN '$GSQL'" <<EOSQL
$1
EOSQL
}

setup() {
  echo "[1/4] 创建 bench_og_hot (300万行, 无索引)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_og_hot CASCADE;
CREATE TABLE bench_og_hot (id SERIAL PRIMARY KEY, uid INT, payload TEXT);
INSERT INTO bench_og_hot SELECT i, MOD(i, 1000000), repeat(md5(i::text), 4)
  FROM generate_series(1, 3000000) i;
ANALYZE bench_og_hot;
"

  echo "[2/4] 起 5 burst UPDATE 制造历史行锁争用 (commit 后释放)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_og_lock CASCADE;
CREATE TABLE bench_og_lock (id INT PRIMARY KEY, counter INT);
INSERT INTO bench_og_lock VALUES (1, 0);
"
  for i in 1 2 3 4 5; do
    remote "$GAUSS_RUN 'echo \"BEGIN; UPDATE bench_og_lock SET counter = counter + 1 WHERE id = 1; SELECT pg_sleep(2); COMMIT;\" | $GSQL > /dev/null 2>&1 &'"
  done
  echo "  -- 等 burst 释放, 行锁统计留下..."
  sleep 8
  psql_on_og "SELECT 'lock_stats_after_burst' AS phase, count(*) AS held FROM pg_locks WHERE locktype='transactionid';"

  echo "[3/4] 创建 4 个 invalid object (view 引用即将被 DROP 的字段)..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_og_view_src CASCADE;
CREATE TABLE bench_og_view_src (id INT, name TEXT, dept TEXT, salary NUMERIC);
INSERT INTO bench_og_view_src SELECT i, 'name'||i, 'dept'||(i%5), i*100 FROM generate_series(1,100) i;
CREATE OR REPLACE VIEW bench_og_view_a AS SELECT id, name FROM bench_og_view_src;
CREATE OR REPLACE VIEW bench_og_view_b AS SELECT id, dept, salary FROM bench_og_view_src;
CREATE OR REPLACE VIEW bench_og_view_c AS SELECT dept, sum(salary) AS total FROM bench_og_view_src GROUP BY dept;
CREATE OR REPLACE VIEW bench_og_view_d AS SELECT * FROM bench_og_view_b WHERE salary > 5000;
"
  # 写一个跨视图依赖的 SQL, 使部分对象状态 invalid
  psql_on_og_file "ALTER TABLE bench_og_view_src DROP COLUMN salary CASCADE;" || true
  psql_on_og "SELECT viewname FROM pg_views WHERE viewname LIKE 'bench_og_view_%';"

  echo "[4/4] 起 40 个并发循环跑同一条 bench_og_hot 全扫 SQL (主导 CPU + 慢 SQL)..."
  ssh "$SSH_HOST" 'mkdir -p /tmp/oghotload && cat > /tmp/oghotload/hot_loop.sh' <<'SH'
#!/bin/bash
# 同一条 SQL 文本 (含相同 marker), dbe_perf 会聚合为一条 sql_id
while true; do
  echo "/* bench_og_hot_seqscan_marker */ SELECT count(*) FROM bench_og_hot WHERE uid = 12345;" \
    | /opt/opengauss/install/bin/gsql -d postgres -p 15432 -U gauss > /dev/null 2>&1
  sleep 0.5
done
SH
  ssh "$SSH_HOST" "chown gauss:dbgrp /tmp/oghotload/hot_loop.sh 2>/dev/null; chmod +x /tmp/oghotload/hot_loop.sh"
  for i in $(seq 1 40); do
    remote "$GAUSS_RUN 'nohup bash /tmp/oghotload/hot_loop.sh > /dev/null 2>&1 &'"
  done
  sleep 3

  echo ""
  echo "造压完成. 预期: 40 active session 集中跑同 SQL, 历史行锁统计可见,"
  echo "              4 个 view 中部分 invalid, archive_mode=off (默认)."
}

verify() {
  echo "=== #3 当前活跃会话数 (期望 ~40+) ==="
  psql_on_og "SELECT state, COUNT(*) FROM pg_stat_activity GROUP BY state ORDER BY 2 DESC;"
  echo ""
  echo "=== #1 单 SQL 主导 (期望同一 sql_id 占 dbe_perf.statement 高执行次数) ==="
  psql_on_og "SELECT unique_sql_id, n_calls, ROUND(total_elapse_time::numeric/n_calls/1000,2) AS avg_ms FROM dbe_perf.statement WHERE query LIKE '%bench_og_hot_seqscan_marker%' ORDER BY n_calls DESC LIMIT 3;"
  echo ""
  echo "=== #2 历史行锁: 当前无阻塞链 (blocktree 应空) ==="
  psql_on_og "SELECT count(*) AS current_blocked FROM pg_stat_activity WHERE wait_event_type IN ('Lock','LWLock') AND wait_event = 'transactionid';"
  echo ""
  echo "=== #4 invalid view (期望部分 view 的 ev_action 为损坏) ==="
  psql_on_og "SELECT viewname, definition FROM pg_views WHERE viewname LIKE 'bench_og_view_%';"
  echo ""
  echo "=== #5 慢 SQL 阈值 (期望 bench_og_hot 单次执行 >2s) ==="
  psql_on_og "SELECT unique_sql_id, ROUND(max_elapse_time::numeric/1000,2) AS max_ms, n_calls FROM dbe_perf.statement WHERE query LIKE '%bench_og_hot_seqscan_marker%';"
  echo ""
  echo "=== 综合: archive_mode + dead tuples ==="
  psql_on_og "SHOW archive_mode;"
  psql_on_og "SELECT relname, n_dead_tup FROM pg_stat_user_tables WHERE n_dead_tup > 0 ORDER BY n_dead_tup DESC LIMIT 5;"
}

cleanup() {
  echo "[cleanup] 杀掉 hot_loop 后台进程..."
  remote "pkill -9 -f 'hot_loop.sh' 2>/dev/null || true"
  remote "pkill -9 -f 'bench_og_hot_seqscan_marker' 2>/dev/null || true"
  sleep 3

  echo "[cleanup] 删 bench_og_* 表/视图..."
  psql_on_og_file "
DROP VIEW IF EXISTS bench_og_view_a, bench_og_view_b, bench_og_view_c, bench_og_view_d CASCADE;
DROP TABLE IF EXISTS bench_og_hot, bench_og_lock, bench_og_view_src CASCADE;
"
  remote "rm -rf /tmp/oghotload 2>/dev/null"
  echo "[cleanup] 完成."
}

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup|verify|cleanup}"; exit 1 ;;
esac
