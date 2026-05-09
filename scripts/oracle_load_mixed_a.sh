#!/bin/bash
# oracle_load_mixed_a.sh — Oracle 版 "性能级联" 4 问题混合故障
#
# 4 个相互关联的问题:
#   #1 长事务持有 undo — 50 个会话 INSERT/UPDATE 不 COMMIT, 撑大 UNDO
#   #2 硬解析冲高 — 30 个会话循环跑字面量 SQL, 触发 library cache contention
#   #3 缺索引全表扫描 — bench_ora_b 上 uid 列无索引, 30 个并发 FTS
#   #4 会话冲高 — 总 session 110+, processes 使用率 > 60%
#
# 期望 LLM 在四层诊断里:
#   一、告警主线: 会话冲高 / library cache 争用
#   二、关联问题: 长事务 + 硬解析 + 缺索引
#   三、当前对比: 与 PROFILE.md 基线对比
#   四、综合评估: 修复优先级 = COMMIT 长事务 → 用绑定变量 → 加索引
#
# 用法:
#   bash scripts/oracle_load_mixed_a.sh setup
#   bash scripts/oracle_load_mixed_a.sh verify
#   bash scripts/oracle_load_mixed_a.sh cleanup
#
# 环境: 47.251.30.180:22, Oracle 19c, ORACLE_SID=ORCLCDB, PDB=orclpdb1
# 用 OS 认证 (sqlplus / as sysdba) + ALTER SESSION SET CONTAINER=orclpdb1

set -u

SSH_HOST="root@47.251.30.180"
ORACLE_ENV='export ORACLE_HOME=/opt/oracle/product/19c/dbhome_1; export ORACLE_SID=ORCLCDB; export PATH=$ORACLE_HOME/bin:$PATH'
SQLPLUS="sqlplus -S / as sysdba"

action="${1:-setup}"

# ── helpers ──

remote() {
  ssh -o ConnectTimeout=20 "$SSH_HOST" "$@" 2>&1
}

# Run a SQL block in the orclpdb1 PDB via OS-auth as sysdba
sql_in_pdb() {
  local sql="$1"
  ssh "$SSH_HOST" "su - oracle -c '$ORACLE_ENV; $SQLPLUS' " <<EOSQL 2>&1
ALTER SESSION SET CONTAINER = orclpdb1;
$sql
EXIT;
EOSQL
}

# Spawn a background sqlplus session that opens a long tx and sleeps
spawn_long_tx() {
  local n="$1"
  for i in $(seq 1 "$n"); do
    remote "su - oracle -c '$ORACLE_ENV; nohup bash -c \"echo \\\"ALTER SESSION SET CONTAINER=orclpdb1; INSERT INTO bench_ora_a (id, status, payload) VALUES (sys_guid_seq.nextval, 99, dbms_random.string(\\\\\\\"A\\\\\\\",50)); EXEC dbms_lock.sleep(1800);\\\" | $SQLPLUS\" > /dev/null 2>&1 &'"
  done
}

# Spawn N background sessions that loop hard-parse + literal SQLs
spawn_hard_parse() {
  local n="$1"
  for i in $(seq 1 "$n"); do
    remote "su - oracle -c '$ORACLE_ENV; nohup bash -c \"while true; do echo \\\"ALTER SESSION SET CONTAINER=orclpdb1; SELECT count(*) FROM bench_ora_a WHERE status = \$((RANDOM % 100));\\\" | $SQLPLUS > /dev/null 2>&1; sleep 1; done\" > /dev/null 2>&1 &'"
  done
}

# Spawn N background sessions that loop full-table scan on bench_ora_b
spawn_seqscan() {
  local n="$1"
  for i in $(seq 1 "$n"); do
    remote "su - oracle -c '$ORACLE_ENV; nohup bash -c \"while true; do echo \\\"ALTER SESSION SET CONTAINER=orclpdb1; SELECT count(*) FROM bench_ora_b WHERE uid BETWEEN \$((RANDOM % 1000000)) AND \$((RANDOM % 1000000 + 5000));\\\" | $SQLPLUS > /dev/null 2>&1; sleep 1; done\" > /dev/null 2>&1 &'"
  done
}

# ── setup ──

setup() {
  echo "[1/4] 创建 bench_ora_a (50万行) + bench_ora_b (100万行, 缺索引)..."
  sql_in_pdb "
DROP TABLE bench_ora_a CASCADE CONSTRAINTS PURGE;
DROP TABLE bench_ora_b CASCADE CONSTRAINTS PURGE;
DROP SEQUENCE sys_guid_seq;
CREATE SEQUENCE sys_guid_seq START WITH 1000001 INCREMENT BY 1 NOCACHE;
CREATE TABLE bench_ora_a (id NUMBER PRIMARY KEY, status NUMBER, payload VARCHAR2(200));
INSERT INTO bench_ora_a
  SELECT level, MOD(level,100), dbms_random.string('A',100)
  FROM dual CONNECT BY level <= 500000;
COMMIT;
CREATE TABLE bench_ora_b (id NUMBER PRIMARY KEY, uid NUMBER, name VARCHAR2(100));
INSERT INTO bench_ora_b
  SELECT level, MOD(level, 1000000), 'name_'||level
  FROM dual CONNECT BY level <= 1000000;
COMMIT;
EXEC DBMS_STATS.GATHER_TABLE_STATS('SYSTEM','BENCH_ORA_A');
EXEC DBMS_STATS.GATHER_TABLE_STATS('SYSTEM','BENCH_ORA_B');
" | tail -15

  echo ""
  echo "[2/4] 起 50 个长事务持 undo (INSERT then dbms_lock.sleep(1800))..."
  spawn_long_tx 50
  sleep 3

  echo "[3/4] 起 30 个硬解析 session (字面量 SQL 循环)..."
  spawn_hard_parse 30
  sleep 2

  echo "[4/4] 起 30 个全表扫描 session (bench_ora_b SeqScan 循环)..."
  spawn_seqscan 30
  sleep 3

  echo ""
  echo "造压完成。预期: total session 110+, 长事务 50, 硬解析率冲高, FTS 频繁"
  echo "用 verify 检查。"
}

# ── verify ──

verify() {
  echo "=== 症状 #4: 会话/进程总数 ==="
  sql_in_pdb "
SELECT
  (SELECT COUNT(*) FROM v\$session WHERE type='USER') AS user_sessions,
  (SELECT COUNT(*) FROM v\$session WHERE type='USER' AND status='ACTIVE') AS active_sessions,
  (SELECT VALUE FROM v\$parameter WHERE name='processes') AS max_processes,
  (SELECT COUNT(*) FROM v\$process) AS used_processes
FROM dual;
" | tail -10
  echo ""

  echo "=== 症状 #1: 长事务 (按 v\$transaction start_time) ==="
  sql_in_pdb "
SELECT COUNT(*) AS long_tx_count, MIN(start_time) AS oldest_tx_start
FROM v\$transaction;
" | tail -8
  echo ""

  echo "=== 症状 #2: 硬解析比例 (期望 > 5%) ==="
  sql_in_pdb "
SELECT
  ROUND(hp.value / NULLIF(p.value, 0) * 100, 2) AS hard_parse_pct,
  hp.value AS hard_parses,
  p.value AS total_parses
FROM
  (SELECT value FROM v\$sysstat WHERE name='parse count (hard)') hp,
  (SELECT value FROM v\$sysstat WHERE name='parse count (total)') p;
" | tail -8
  echo ""

  echo "=== 症状 #3: bench_ora_b 全表扫描计划 ==="
  sql_in_pdb "
EXPLAIN PLAN FOR SELECT COUNT(*) FROM bench_ora_b WHERE uid BETWEEN 100000 AND 105000;
SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY(format=>'BASIC'));
" | tail -10
  echo ""

  echo "=== 综合: top wait events (期望 library cache / buffer busy / row cache) ==="
  sql_in_pdb "
SELECT event, total_waits, time_waited
FROM (
  SELECT event, total_waits, time_waited
  FROM v\$system_event
  WHERE wait_class != 'Idle'
  ORDER BY time_waited DESC
)
WHERE ROWNUM <= 8;
" | tail -12
}

# ── cleanup ──

cleanup() {
  echo "[cleanup] 杀掉本脚本起的后台 sqlplus 进程..."
  remote "pkill -9 -f 'dbms_lock.sleep(1800)' 2>/dev/null || true"
  remote "pkill -9 -f 'bench_ora_b WHERE uid BETWEEN' 2>/dev/null || true"
  remote "pkill -9 -f 'bench_ora_a WHERE status' 2>/dev/null || true"
  remote "pkill -9 -f 'while true' 2>/dev/null || true"
  sleep 3

  echo "[cleanup] 终止 PDB 内仍在跑的 user 会话 (bench_ora_*)..."
  sql_in_pdb "
BEGIN
  FOR r IN (
    SELECT sid, serial# FROM v\$session
    WHERE username = 'SYSTEM'
      AND program LIKE 'sqlplus%'
      AND status = 'ACTIVE'
  ) LOOP
    BEGIN
      EXECUTE IMMEDIATE 'ALTER SYSTEM KILL SESSION '''||r.sid||','||r.serial#||''' IMMEDIATE';
    EXCEPTION WHEN OTHERS THEN NULL;
    END;
  END LOOP;
END;
/
" | tail -3

  echo "[cleanup] 删除 bench_ora_* 表..."
  sql_in_pdb "
DROP TABLE bench_ora_a CASCADE CONSTRAINTS PURGE;
DROP TABLE bench_ora_b CASCADE CONSTRAINTS PURGE;
DROP SEQUENCE sys_guid_seq;
" | tail -5

  echo "[cleanup] 完成。"
}

# ── main ──

case "$action" in
  setup)   setup ;;
  verify)  verify ;;
  cleanup) cleanup ;;
  *)       echo "Usage: $0 {setup|verify|cleanup}"; exit 1 ;;
esac
