#!/bin/bash
# og_load_app_antipatterns.sh — OG 应用反模式合集 (零重叠于 oracle_mirror)
#
# 场景: 索引/统计都齐了, 但 SQL 写法让 planner 走 Seq Scan / 全索引扫 / Lock 串行
# 5 个反模式 (与旧 oracle_mirror 场景 0% 重叠):
#   F1 深分页 OFFSET       — ORDER BY pk LIMIT 10 OFFSET 1.5M, 索引扫穿 1.5M 行返回 10
#   F2 函数包裹谓词        — date_trunc('day', col) = '2024-01-01', sargable 失效, Seq Scan
#   F3 Hot row 争用        — 100 并发 UPDATE WHERE id=1 (单行表), 行锁串行
#   F4 前导通配符 LIKE     — payload LIKE '%xxx%', btree 失效, Seq Scan 全表
#   F5 NOT IN + NULL 陷阱  — NOT IN 子查询内有 NULL, 返回错误结果 + 极慢
#
# 用法:
#   bash scripts/og_load_app_antipatterns.sh setup
#   bash scripts/og_load_app_antipatterns.sh verify
#   bash scripts/og_load_app_antipatterns.sh cleanup
#
# 测试 /sqltune 时建议:
#   /sqltune SELECT * FROM bench_app_users ORDER BY user_id LIMIT 10 OFFSET 1500000;
#   /sqltune SELECT count(*) FROM bench_app_logs WHERE date_trunc('day', created_at) = '2024-01-15';
#   /sqltune UPDATE bench_app_counter SET counter = counter + 1 WHERE id = 1;
#   /sqltune SELECT count(*) FROM bench_app_search WHERE payload LIKE '%target%';
#   /sqltune SELECT count(*) FROM bench_app_orders WHERE buyer_id NOT IN (SELECT referrer_id FROM bench_app_users WHERE deleted_flag = 0);

set -u

SSH_HOST="root@47.251.30.180"
GSQL='/opt/opengauss/install/bin/gsql -d postgres -p 15432'
GAUSS_RUN="su - gauss -c"

action="${1:-setup}"

remote() { ssh -o ConnectTimeout=20 "$SSH_HOST" "$@" 2>&1; }
psql_on_og() { remote "$GAUSS_RUN '$GSQL -c \"$1\"'"; }
psql_on_og_file() {
  ssh "$SSH_HOST" "$GAUSS_RUN '$GSQL'" <<EOSQL
$1
EOSQL
}

setup() {
  echo "=== F1: 深分页 OFFSET — bench_app_users (BIGINT user_id PK, 2M 行) ==="
  psql_on_og_file "
DROP TABLE IF EXISTS bench_app_users CASCADE;
CREATE TABLE bench_app_users (
    user_id   BIGINT PRIMARY KEY,
    status    SMALLINT,
    deleted_flag SMALLINT,
    name      TEXT
);
INSERT INTO bench_app_users
SELECT i, MOD(i, 5)::SMALLINT, 0::SMALLINT, 'user_'||i
FROM generate_series(1, 2000000) i;
CREATE INDEX idx_users_user_id ON bench_app_users(user_id);
ANALYZE bench_app_users;
"

  echo "=== F2: 函数包裹谓词 — bench_app_logs (TIMESTAMP created_at, idx_logs_created_at) ==="
  psql_on_og_file "
DROP TABLE IF EXISTS bench_app_logs CASCADE;
CREATE TABLE bench_app_logs (
    id          BIGSERIAL PRIMARY KEY,
    created_at  TIMESTAMP NOT NULL,
    level       TEXT,
    message     TEXT
);
INSERT INTO bench_app_logs (created_at, level, message)
SELECT
    TIMESTAMP '2024-01-01 00:00:00' + (i || ' seconds')::INTERVAL,
    CASE MOD(i, 4) WHEN 0 THEN 'ERROR' WHEN 1 THEN 'WARN' ELSE 'INFO' END,
    'log message '||i
FROM generate_series(1, 1500000) i;
CREATE INDEX idx_logs_created_at ON bench_app_logs(created_at);
ANALYZE bench_app_logs;
"

  echo "=== F3: Hot row 争用 — bench_app_counter (单行表) ==="
  psql_on_og_file "
DROP TABLE IF EXISTS bench_app_counter CASCADE;
CREATE TABLE bench_app_counter (
    id      INT PRIMARY KEY,
    counter BIGINT NOT NULL
);
INSERT INTO bench_app_counter VALUES (1, 0);
ANALYZE bench_app_counter;
"

  echo "=== F4: 前导通配符 LIKE — bench_app_search (payload TEXT, idx_search_payload) ==="
  psql_on_og_file "
DROP TABLE IF EXISTS bench_app_search CASCADE;
CREATE TABLE bench_app_search (
    id      BIGSERIAL PRIMARY KEY,
    name    TEXT,
    payload TEXT
);
INSERT INTO bench_app_search (name, payload)
SELECT
    'name_'||i,
    CASE WHEN MOD(i, 1000) = 0 THEN 'before_target_after' ELSE md5(i::TEXT) || '_' || md5((i+1)::TEXT) END
FROM generate_series(1, 1000000) i;
CREATE INDEX idx_search_payload ON bench_app_search(payload);
ANALYZE bench_app_search;
"

  echo "=== F5: NOT IN + NULL 陷阱 — bench_app_orders + users.referrer_id 含 NULL ==="
  psql_on_og_file "
DROP TABLE IF EXISTS bench_app_orders CASCADE;
CREATE TABLE bench_app_orders (
    order_id    BIGSERIAL PRIMARY KEY,
    buyer_id    BIGINT NOT NULL,
    amount      NUMERIC(10,2),
    status      SMALLINT
);
INSERT INTO bench_app_orders (buyer_id, amount, status)
SELECT MOD(i, 1000000) + 1, (random() * 1000)::NUMERIC(10,2), MOD(i, 5)::SMALLINT
FROM generate_series(1, 800000) i;
CREATE INDEX idx_orders_buyer_id ON bench_app_orders(buyer_id);
ANALYZE bench_app_orders;
-- 在 users 表加可空 referrer_id, ~14% NULL → NOT IN 真陷阱
ALTER TABLE bench_app_users ADD COLUMN IF NOT EXISTS referrer_id BIGINT;
UPDATE bench_app_users SET referrer_id = MOD(user_id, 100000) + 1 WHERE MOD(user_id, 7) != 0;
ANALYZE bench_app_users;
"

  echo ""
  echo "=== 启动 4 路慢查询负载 (持续 active session, 让 sentinel 抓得到) ==="
  ssh "$SSH_HOST" 'mkdir -p /tmp/og_app_anti'

  # F1 worker (深分页 OFFSET — 大 OFFSET 让索引扫穿 1.5M 行)
  ssh "$SSH_HOST" 'cat > /tmp/og_app_anti/f1_worker.sh' <<'SH'
#!/bin/bash
while true; do
  echo "/* anti_f1_deep_offset */ SELECT user_id, name FROM bench_app_users ORDER BY user_id LIMIT 10 OFFSET 1500000;" \
    | /opt/opengauss/install/bin/gsql -d postgres -p 15432 -U gauss > /dev/null 2>&1
  sleep 0.3
done
SH

  # F2 worker (函数包裹谓词)
  ssh "$SSH_HOST" 'cat > /tmp/og_app_anti/f2_worker.sh' <<'SH'
#!/bin/bash
while true; do
  echo "/* anti_f2_func_predicate */ SELECT count(*) FROM bench_app_logs WHERE date_trunc('day', created_at) = '2024-01-15';" \
    | /opt/opengauss/install/bin/gsql -d postgres -p 15432 -U gauss > /dev/null 2>&1
  sleep 0.3
done
SH

  # F3 worker (hot row update 争用)
  ssh "$SSH_HOST" 'cat > /tmp/og_app_anti/f3_worker.sh' <<'SH'
#!/bin/bash
while true; do
  echo "/* anti_f3_hot_row */ BEGIN; UPDATE bench_app_counter SET counter = counter + 1 WHERE id = 1; SELECT pg_sleep(0.05); COMMIT;" \
    | /opt/opengauss/install/bin/gsql -d postgres -p 15432 -U gauss > /dev/null 2>&1
done
SH

  # F4 worker (前导通配符 LIKE)
  ssh "$SSH_HOST" 'cat > /tmp/og_app_anti/f4_worker.sh' <<'SH'
#!/bin/bash
while true; do
  echo "/* anti_f4_leading_wildcard */ SELECT count(*) FROM bench_app_search WHERE payload LIKE '%target%';" \
    | /opt/opengauss/install/bin/gsql -d postgres -p 15432 -U gauss > /dev/null 2>&1
  sleep 0.5
done
SH

  ssh "$SSH_HOST" "chown gauss:dbgrp /tmp/og_app_anti/*.sh 2>/dev/null; chmod +x /tmp/og_app_anti/*.sh"

  echo "  -- 启动 F1 (隐式类型转换) × 8 worker..."
  for i in $(seq 1 8); do
    remote "$GAUSS_RUN 'nohup bash /tmp/og_app_anti/f1_worker.sh > /dev/null 2>&1 &'"
  done

  echo "  -- 启动 F2 (函数包裹谓词) × 6 worker..."
  for i in $(seq 1 6); do
    remote "$GAUSS_RUN 'nohup bash /tmp/og_app_anti/f2_worker.sh > /dev/null 2>&1 &'"
  done

  echo "  -- 启动 F3 (hot row 争用) × 30 worker..."
  for i in $(seq 1 30); do
    remote "$GAUSS_RUN 'nohup bash /tmp/og_app_anti/f3_worker.sh > /dev/null 2>&1 &'"
  done

  echo "  -- 启动 F4 (前导通配符 LIKE) × 4 worker..."
  for i in $(seq 1 4); do
    remote "$GAUSS_RUN 'nohup bash /tmp/og_app_anti/f4_worker.sh > /dev/null 2>&1 &'"
  done

  sleep 5
  echo ""
  echo "造压完成. 总并发 ~48 (F1=8 / F2=6 / F3=30 / F4=4)."
  echo "F5 (NOT IN + NULL) 只造数据不造负载, 用 /sqltune 直接验证语义错误."
}

verify() {
  echo "=== 当前活跃会话 (期望 ~40+) ==="
  psql_on_og "SELECT state, count(*) FROM pg_stat_activity GROUP BY state ORDER BY 2 DESC;"
  echo ""
  echo "=== F1 深分页 — 期望 SQL 索引扫穿 1.5M 行 (cost ~50000) ==="
  psql_on_og "SELECT unique_sql_id, n_calls, ROUND(total_elapse_time::numeric/n_calls/1000,2) AS avg_ms FROM dbe_perf.statement WHERE query LIKE '%anti_f1_deep_offset%' ORDER BY n_calls DESC LIMIT 3;"
  echo ""
  echo "=== F2 函数包裹谓词 — 期望 SQL 走 Seq Scan (date_trunc 包住 idx 列) ==="
  psql_on_og "SELECT unique_sql_id, n_calls, ROUND(total_elapse_time::numeric/n_calls/1000,2) AS avg_ms FROM dbe_perf.statement WHERE query LIKE '%anti_f2_func_predicate%' ORDER BY n_calls DESC LIMIT 3;"
  echo ""
  echo "=== F3 Hot row 争用 — 期望大量 transactionid 等待 ==="
  psql_on_og "SELECT count(*) AS lock_waiters FROM pg_stat_activity WHERE wait_event = 'transactionid';"
  psql_on_og "SELECT unique_sql_id, n_calls, ROUND(total_elapse_time::numeric/n_calls/1000,2) AS avg_ms FROM dbe_perf.statement WHERE query LIKE '%anti_f3_hot_row%' ORDER BY n_calls DESC LIMIT 3;"
  echo ""
  echo "=== F4 前导通配符 LIKE — 期望 Seq Scan 全 100 万行 ==="
  psql_on_og "SELECT unique_sql_id, n_calls, ROUND(max_elapse_time::numeric/1000,2) AS max_ms FROM dbe_perf.statement WHERE query LIKE '%anti_f4_leading_wildcard%' ORDER BY n_calls DESC LIMIT 3;"
  echo ""
  echo "=== F5 NOT IN + NULL — 检查 users.referrer_id 是否含 NULL (期望 ~14%) ==="
  psql_on_og "SELECT count(*) AS null_rows FROM bench_app_users WHERE referrer_id IS NULL;"
  echo ""
  echo "=== EXPLAIN 抽样: F1 是否真扫穿 1.5M 行 ==="
  psql_on_og "EXPLAIN SELECT user_id, name FROM bench_app_users ORDER BY user_id LIMIT 10 OFFSET 1500000;"
  echo ""
  echo "=== EXPLAIN 抽样: F2 是否真走 Seq Scan ==="
  psql_on_og "EXPLAIN SELECT count(*) FROM bench_app_logs WHERE date_trunc('day', created_at) = '2024-01-15';"
}

cleanup() {
  echo "[cleanup] 杀掉 worker 后台进程..."
  remote "pkill -9 -f 'og_app_anti' 2>/dev/null || true"
  remote "pkill -9 -f 'anti_f[1-5]_' 2>/dev/null || true"
  sleep 3

  echo "[cleanup] 删 bench_app_* 表..."
  psql_on_og_file "
DROP TABLE IF EXISTS bench_app_users CASCADE;
DROP TABLE IF EXISTS bench_app_logs CASCADE;
DROP TABLE IF EXISTS bench_app_counter CASCADE;
DROP TABLE IF EXISTS bench_app_search CASCADE;
DROP TABLE IF EXISTS bench_app_orders CASCADE;
"
  remote "rm -rf /tmp/og_app_anti 2>/dev/null"
  echo "[cleanup] 完成."
}

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup|verify|cleanup}"; exit 1 ;;
esac
