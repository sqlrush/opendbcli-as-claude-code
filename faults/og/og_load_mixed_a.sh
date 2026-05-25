#!/bin/bash
# og_load_mixed_a.sh — 构造 OpenGauss "性能级联" 4 问题混合故障
#
# 4 个相互关联的问题（这是一条因果链，不是 4 个独立场景）:
#   #1 长事务 idle in transaction — 60 个 BEGIN; pg_sleep(1800); 会话
#   #2 autovacuum 被阻塞 — bench_mix_a 反复 UPDATE 攒 dead_tup, #1 的长事务
#      持有 xmin 阻止回收
#   #3 慢查询 + 缺索引 — bench_mix_b 上 WHERE col 无索引，触发 SeqScan，
#      后台 50 个高频调用 session
#   #4 连接数冲高 — #1 + #3 合计约 110 个 session，触发 max_connections 80%
#
# 期望 LLM 在四层诊断里:
#   一、告警主线: 连接数冲高（最显眼现象）
#   二、关联问题: 长事务 + dead_tup 累积 + 缺索引慢查询
#   三、当前对比: 与 PROFILE.md 的基线对比
#   四、综合评估: 修复优先级 = 先 kill 长事务 → autovacuum 自然恢复 → 加索引
#
# 用法:
#   bash scripts/og_load_mixed_a.sh setup    # 起负载
#   bash scripts/og_load_mixed_a.sh verify   # 检查 4 个症状是否触发
#   bash scripts/og_load_mixed_a.sh cleanup  # 清理 (只清自己的, 不动 bench_t*)
#
# 环境: 47.251.30.180:22, gauss:OpenGauss@2026 (本地管理用)
# ssh root@47.251.30.180 免密

set -u

SSH_HOST="root@47.251.30.180"
GSQL='/opt/opengauss/install/bin/gsql -d postgres -p 15432'
GAUSS_RUN="su - gauss -c"
SLEEP_TAG=1800   # 用 1800 区分本脚本的 pg_sleep 与 og_load_complex.sh 的 1500

action="${1:-setup}"

# ── helpers ──

remote() {
  ssh -o ConnectTimeout=10 "$SSH_HOST" "$@" 2>&1
}

psql_on_og() {
  remote "$GAUSS_RUN '$GSQL -c \"$1\"'"
}

psql_on_og_file() {
  ssh "$SSH_HOST" "$GAUSS_RUN '$GSQL'" <<EOSQL
$1
EOSQL
}

# ── setup ──

setup() {
  echo "[1/4] bench_mix_a: 100万行 + 多次 UPDATE 攒 dead_tup..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_mix_a CASCADE;
CREATE TABLE bench_mix_a (id SERIAL PRIMARY KEY, status INT, payload TEXT)
  WITH (autovacuum_enabled = true);
INSERT INTO bench_mix_a SELECT i, 0, md5(i::text) FROM generate_series(1, 1000000) i;
UPDATE bench_mix_a SET status = status + 1;
UPDATE bench_mix_a SET status = status + 1;
UPDATE bench_mix_a SET status = status + 1;
UPDATE bench_mix_a SET status = status + 1;
ANALYZE bench_mix_a;
"

  echo "[2/4] bench_mix_b: 200万行, WHERE 列无索引（触发 SeqScan）..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_mix_b CASCADE;
CREATE TABLE bench_mix_b (id SERIAL PRIMARY KEY, uid INT, name TEXT);
-- 注意: 故意不在 uid 上建索引
INSERT INTO bench_mix_b SELECT i, (random()*1000000)::int, md5(i::text)
  FROM generate_series(1, 2000000) i;
ANALYZE bench_mix_b;
"

  echo "[3/4] 起 60 个长事务 idle in transaction (pg_sleep($SLEEP_TAG))..."
  for i in $(seq 1 60); do
    remote "$GAUSS_RUN 'nohup bash -c \"( echo \\\"BEGIN; SELECT 1; SELECT pg_sleep($SLEEP_TAG);\\\" | $GSQL ) \" > /dev/null 2>&1 &'"
  done
  sleep 3

  echo "[4/4] 起 50 个后台慢查询 session (循环跑 SeqScan 在 bench_mix_b 上)..."
  for i in $(seq 1 50); do
    remote "$GAUSS_RUN 'nohup bash -c \"while true; do echo \\\"SELECT COUNT(*) FROM bench_mix_b WHERE uid BETWEEN $((RANDOM % 1000000)) AND $((RANDOM % 1000000 + 5000));\\\" | $GSQL > /dev/null 2>&1; sleep 1; done\" > /dev/null 2>&1 &'"
  done
  sleep 2

  echo ""
  echo "造压完成。预期: idle-in-tx≈60, active≈50, total≈110+ sessions, dead_pct(bench_mix_a)>10%"
  echo "用 verify 检查所有 4 个症状是否都触发。"
}

# ── verify ──

verify() {
  echo "=== 症状 #4: 连接数冲高 (期望 total ≥ 110) ==="
  psql_on_og "SELECT state, COUNT(*) FROM pg_stat_activity WHERE state IS NOT NULL GROUP BY state ORDER BY 2 DESC;"
  psql_on_og "SELECT COUNT(*) AS total_sessions FROM pg_stat_activity;"
  echo ""

  echo "=== 症状 #1: 长事务 idle in transaction (期望 ≥ 60 个 pg_sleep($SLEEP_TAG)) ==="
  psql_on_og "SELECT COUNT(*) AS idle_in_tx FROM pg_stat_activity WHERE state = 'idle in transaction' AND query LIKE '%pg_sleep($SLEEP_TAG)%';"
  psql_on_og "SELECT pid, usename, state, EXTRACT(EPOCH FROM now()-xact_start)::int AS xact_age_s FROM pg_stat_activity WHERE state = 'idle in transaction' AND query LIKE '%pg_sleep($SLEEP_TAG)%' ORDER BY xact_age_s DESC LIMIT 5;"
  echo ""

  echo "=== 症状 #2: bench_mix_a dead_tup 累积（期望 dead_pct > 10%） ==="
  psql_on_og "SELECT relname, n_live_tup, n_dead_tup, ROUND(100.0*n_dead_tup/NULLIF(n_live_tup,0), 1) AS dead_pct, last_autovacuum FROM pg_stat_all_tables WHERE relname LIKE 'bench_mix_%';"
  echo ""

  echo "=== 症状 #3: bench_mix_b 缺索引 + SeqScan ==="
  psql_on_og "EXPLAIN (COSTS OFF) SELECT COUNT(*) FROM bench_mix_b WHERE uid BETWEEN 100000 AND 105000;"
  echo "  -- top SQL hitting bench_mix_b:"
  psql_on_og "SELECT calls, ROUND(total_time::numeric, 0) AS total_ms, query FROM dbe_perf.statement WHERE query LIKE '%bench_mix_b%' ORDER BY calls DESC LIMIT 5;"
  echo ""

  echo "=== 综合: 等待事件 top (LWLock / IO / IPC 期望出现) ==="
  psql_on_og "SELECT wait_event_type, wait_event, COUNT(*) FROM pg_stat_activity WHERE wait_event IS NOT NULL GROUP BY 1,2 ORDER BY 3 DESC LIMIT 8;"
}

# ── cleanup ──

cleanup() {
  echo "[cleanup] 终止本脚本起的 idle in tx (pg_sleep($SLEEP_TAG)) 和后台慢查询 loop..."
  remote "pkill -f 'pg_sleep($SLEEP_TAG)' 2>/dev/null || true"
  remote "pkill -f 'bench_mix_b WHERE uid BETWEEN' 2>/dev/null || true"
  # 兜底: 杀掉所有 bench_mix_b 相关的 psql 循环 wrapper
  remote "pkill -f 'while true' 2>/dev/null || true"
  sleep 2

  echo "[cleanup] 删除 bench_mix_* 表..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_mix_a, bench_mix_b CASCADE;
"
  echo "[cleanup] 完成。bench_t* 表（来自 og_load_complex.sh）未动。"
}

# ── main ──

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup|verify|cleanup}"; exit 1 ;;
esac
