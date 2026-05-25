#!/bin/sh
set -eu

payload=$(cat)

json_string() {
  key="$1"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$payload" | jq -r "$key // empty" 2>/dev/null || true
    return
  fi
  flat=$(printf '%s' "$payload" | tr '\n' ' ')
  name=$(printf '%s' "$key" | sed 's/^.*\.//')
  printf '%s' "$flat" | sed -n 's/.*"'"$name"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

json_number() {
  key="$1"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$payload" | jq -r "$key // empty" 2>/dev/null || true
    return
  fi
  flat=$(printf '%s' "$payload" | tr '\n' ' ')
  name=$(printf '%s' "$key" | sed 's/^.*\.//')
  printf '%s' "$flat" | sed -n 's/.*"'"$name"'"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p'
}

min_seconds=$(json_number '.params.min_seconds')
limit=$(json_number '.params.limit')
dbcli=$(json_string '.params.dbcli')
host=$(json_string '.context.host')
port=$(json_number '.context.port')
database=$(json_string '.context.database')
user=$(json_string '.context.user')
connection=$(json_string '.context.connection')

[ -n "$min_seconds" ] || min_seconds=30
[ -n "$limit" ] || limit=10
[ -n "$host" ] || host=127.0.0.1
[ -n "$port" ] || port=5432
[ -n "$database" ] || database=postgres
[ -n "$user" ] || user=omm

case "$min_seconds" in *[!0-9]*|'') min_seconds=30 ;; esac
case "$limit" in *[!0-9]*|'') limit=10 ;; esac

if [ -z "$dbcli" ]; then
  if command -v gsql >/dev/null 2>&1; then
    dbcli=$(command -v gsql)
  elif command -v psql >/dev/null 2>&1; then
    dbcli=$(command -v psql)
  else
    printf '# Wait Chain Triage\n\n'
    printf '无法执行数据库采集：当前 DBAA 外部 skill 沙箱的 PATH 中未找到 `gsql` 或 `psql`。\n\n'
    printf '## 处理建议\n\n'
    printf '1. 在客户沙箱/服务器中安装 openGauss/GaussDB 客户端 `gsql`，或安装兼容的 `psql`。\n'
    printf '2. 确认 DBAA 配置 `external_skills.env_allowlist` 允许传入客户端所在 PATH。\n'
    printf '3. 或运行时显式传入客户端路径：\n\n'
    printf '```text\n'
    printf '/skills run shell_wait_chain_triage {"dbcli":"/path/to/gsql"}\n'
    printf '```\n\n'
    printf '安全说明：本 skill 只执行 SELECT；数据库认证由客户自己的沙箱、.pgpass 或 secret manager 负责。\n'
    exit 0
  fi
fi

run_sql() {
  sql="$1"
  base=$(basename "$dbcli")
  if [ "$base" = "gsql" ]; then
    "$dbcli" -X -q -t -A -F '|' -h "$host" -p "$port" -d "$database" -U "$user" -c "$sql"
  else
    "$dbcli" -X -qAt -v ON_ERROR_STOP=1 -F '|' -h "$host" -p "$port" -d "$database" -U "$user" -c "$sql"
  fi
}

print_section() {
  title="$1"
  printf '\n## %s\n\n' "$title"
}

printf '# Wait Chain Triage\n\n'
printf -- '- connection: %s\n' "${connection:-unknown}"
printf -- '- target: %s:%s/%s as %s\n' "$host" "$port" "$database" "$user"
printf -- '- min_seconds: %s\n' "$min_seconds"
printf -- '- limit: %s\n' "$limit"

print_section '1. Session State Distribution'
run_sql "SELECT COALESCE(state,'<null>') AS state, COUNT(*) AS sessions
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
GROUP BY COALESCE(state,'<null>')
ORDER BY sessions DESC;"

print_section '2. Wait Event Distribution'
run_sql "SELECT
  CASE WHEN waiting THEN 'Lock' ELSE 'CPU' END AS wait_type,
  CASE WHEN waiting THEN 'lock_wait' WHEN enqueue <> '' THEN enqueue ELSE 'On CPU' END AS wait_event,
  COUNT(*) AS sessions
FROM pg_stat_activity
WHERE state = 'active' AND pid <> pg_backend_pid()
GROUP BY wait_type, wait_event
ORDER BY sessions DESC
LIMIT $limit;"

print_section '3. Blocking Chain'
run_sql "SELECT
  blocked.pid AS blocked_pid,
  LEFT(blocked.query, 90) AS blocked_query,
  blocker.pid AS blocker_pid,
  LEFT(blocker.query, 90) AS blocker_query,
  bl.mode AS waiting_mode,
  kl.mode AS blocking_mode
FROM pg_locks bl
JOIN pg_stat_activity blocked ON blocked.pid = bl.pid
JOIN pg_locks kl
  ON kl.locktype = bl.locktype
 AND COALESCE(kl.database::text,'') = COALESCE(bl.database::text,'')
 AND COALESCE(kl.relation::text,'') = COALESCE(bl.relation::text,'')
 AND COALESCE(kl.page::text,'') = COALESCE(bl.page::text,'')
 AND COALESCE(kl.tuple::text,'') = COALESCE(bl.tuple::text,'')
 AND COALESCE(kl.virtualxid::text,'') = COALESCE(bl.virtualxid::text,'')
 AND COALESCE(kl.transactionid::text,'') = COALESCE(bl.transactionid::text,'')
 AND kl.pid <> bl.pid
JOIN pg_stat_activity blocker ON blocker.pid = kl.pid
WHERE NOT bl.granted AND kl.granted
ORDER BY blocked.query_start NULLS LAST
LIMIT $limit;"

print_section '4. Long Transactions / Long Active SQL'
run_sql "SELECT
  pid,
  usename,
  datname,
  state,
  ROUND(EXTRACT(EPOCH FROM clock_timestamp() - COALESCE(xact_start, query_start))::numeric, 1) AS age_sec,
  LEFT(query, 120) AS query
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND COALESCE(xact_start, query_start) IS NOT NULL
  AND clock_timestamp() - COALESCE(xact_start, query_start) > interval '$min_seconds seconds'
ORDER BY COALESCE(xact_start, query_start)
LIMIT $limit;"

printf '\n## Interpretation\n\n'
printf -- '- 如果 Blocking Chain 有结果，优先定位 blocker_pid 对应事务来源，确认是否为业务长事务或 DDL。\n'
printf -- '- 如果 Wait Event 全为 CPU/On CPU 且无业务会话，通常不是在线等待故障。\n'
printf -- '- 本 skill 只读采集，不会终止会话；如需处置，请由 DBA 复核后使用 DBAA 内置 /kill 或变更流程。\n'

