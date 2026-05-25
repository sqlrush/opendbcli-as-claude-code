#!/bin/bash
# oracle_load_complex.sh — Oracle 多因素关联复杂负载（对齐 pg_load_complex.sh）
# 场景: 锁等待链 + I/O冲高 + Temp溢出 + Redo冲高 + 活跃会话堆积
# 用法: bash /tmp/oracle_load_complex.sh        (启动)
#       bash /tmp/oracle_load_complex.sh stop    (停止)
#
# 运行环境: 测试机上以 oracle 用户跑 sqlplus。需要通过 su - oracle 调用。

ORA_CONN="system/OraclePass123@localhost:1521/ORCLPDB1"
PID_FILE=/tmp/ora_load_pids
LOG_DIR=/tmp/ora_load_logs

run_sql() {
  # $1 = 客户端标识（写入 v$session.client_info 便于识别/清理）
  # stdin = SQL
  local tag="$1"
  local pre="BEGIN DBMS_APPLICATION_INFO.SET_CLIENT_INFO('$tag'); END;
/
"
  # 以 oracle 用户执行 sqlplus
  (echo "$pre"; cat) | su - oracle -c "sqlplus -S $ORA_CONN" 2>&1
}

run_sql_loop() {
  # 无限循环执行一段 SQL（每次新建 sqlplus 会话）
  local tag="$1"
  local sql="$2"
  local gap="${3:-1}"
  while true; do
    printf '%s\n' "$sql" | run_sql "$tag" > "$LOG_DIR/$tag.log" 2>&1
    sleep "$gap"
  done
}

# ── 停止模式 ──
if [ "$1" = "stop" ]; then
    echo "=== 停止负载 ==="
    if [ -f "$PID_FILE" ]; then
      xargs -a "$PID_FILE" kill 2>/dev/null
      xargs -a "$PID_FILE" kill -9 2>/dev/null
    fi
    # 清理残留会话（包含根阻塞者和被挂起的 waiter）
    cat <<'SQL' | run_sql "load_cleanup"
SET SERVEROUTPUT ON
BEGIN
  FOR s IN (SELECT sid, serial# FROM v$session
             WHERE client_info LIKE 'load_%'
               AND username = 'SYSTEM') LOOP
    BEGIN
      EXECUTE IMMEDIATE 'ALTER SYSTEM KILL SESSION ''' || s.sid || ',' || s.serial# || ''' IMMEDIATE';
    EXCEPTION WHEN OTHERS THEN NULL;
    END;
  END LOOP;
END;
/
-- 清理表（忽略不存在）
BEGIN EXECUTE IMMEDIATE 'DROP TABLE load_orders PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP TABLE load_products PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP TABLE load_logs PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/
EXIT
SQL
    rm -f "$PID_FILE"
    rm -rf "$LOG_DIR"
    echo "=== 已停止并清理 ==="
    exit 0
fi

mkdir -p "$LOG_DIR"
: > "$PID_FILE"

echo "=== 1/5 准备测试数据 ==="

cat <<'SQL' | run_sql "load_setup" | tail -15
WHENEVER SQLERROR CONTINUE
BEGIN EXECUTE IMMEDIATE 'DROP TABLE load_orders PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP TABLE load_products PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP TABLE load_logs PURGE'; EXCEPTION WHEN OTHERS THEN NULL; END;
/

CREATE TABLE load_orders (
    id          NUMBER PRIMARY KEY,
    customer_id NUMBER NOT NULL,
    product_id  NUMBER NOT NULL,
    amount      NUMBER(12,2),
    status      VARCHAR2(30) DEFAULT 'pending',
    memo        VARCHAR2(300),
    created_at  DATE DEFAULT SYSDATE
);

CREATE TABLE load_products (
    id          NUMBER PRIMARY KEY,
    name        VARCHAR2(50),
    category    VARCHAR2(30),
    price       NUMBER(10,2),
    description VARCHAR2(1000)
);

CREATE TABLE load_logs (
    id          NUMBER GENERATED ALWAYS AS IDENTITY,
    session_id  NUMBER,
    action      VARCHAR2(100),
    payload     VARCHAR2(500),
    created_at  DATE DEFAULT SYSDATE
);

-- 100 万行 load_orders（分 10 批 INSERT /*+ APPEND */ ）
DECLARE
  n NUMBER := 100000;
BEGIN
  FOR b IN 0 .. 9 LOOP
    INSERT /*+ APPEND */ INTO load_orders (id, customer_id, product_id, amount, status, memo)
    SELECT b*n + LEVEL,
           MOD(b*n + LEVEL, 10000),
           MOD(b*n + LEVEL, 100000),
           ROUND(DBMS_RANDOM.VALUE(1,9999),2),
           CASE MOD(LEVEL,4) WHEN 0 THEN 'pending' WHEN 1 THEN 'processing' WHEN 2 THEN 'shipped' ELSE 'completed' END,
           RPAD('x', MOD(LEVEL,200)+1, 'x')
    FROM dual CONNECT BY LEVEL <= n;
    COMMIT;
  END LOOP;
END;
/

INSERT /*+ APPEND */ INTO load_products (id, name, category, price, description)
SELECT LEVEL, 'product_' || LEVEL, 'cat_' || MOD(LEVEL,50),
       ROUND(DBMS_RANDOM.VALUE(1,999),2),
       RPAD('desc_payload_', 500, 'x')
FROM dual CONNECT BY LEVEL <= 100000;
COMMIT;

EXEC DBMS_STATS.GATHER_TABLE_STATS(USER, 'LOAD_ORDERS');
EXEC DBMS_STATS.GATHER_TABLE_STATS(USER, 'LOAD_PRODUCTS');

SELECT COUNT(*) AS orders_cnt FROM load_orders;
SELECT COUNT(*) AS products_cnt FROM load_products;
EXIT
SQL
echo "   load_orders(100万行), load_products(10万行), load_logs 就绪"

# ── 因素1: 锁争用链 ──
echo "=== 2/5 锁争用链 ==="

# 根阻塞者: UPDATE 后 sleep 不提交（持有 TX enq）
(
  cat <<'SQL' | run_sql "load_lock_root" > "$LOG_DIR/load_lock_root.log" 2>&1
SET TIMING OFF
UPDATE load_orders SET status = 'LOCKED_ROOT' WHERE id BETWEEN 1 AND 200;
BEGIN
  FOR i IN 1..24 LOOP
    DBMS_LOCK.SLEEP(3600);
  END LOOP;
END;
/
ROLLBACK;
EXIT
SQL
) &
echo $! >> "$PID_FILE"
sleep 3

# 6 等待者: 各自 UPDATE 被阻塞行 → 挂在 TX enq 上（不会退出，形成排队）
for i in 1 2 3 4 5 6; do
  target_id=$((i * 30 + 1))
  (
    while true; do
      printf 'UPDATE load_orders SET status = '\''wait_%d'\'' WHERE id = %d;\nCOMMIT;\nEXIT\n' "$i" "$target_id" | \
        run_sql "load_lock_waiter_$i" > "$LOG_DIR/load_lock_waiter_$i.log" 2>&1
      sleep 1
    done
  ) &
  echo $! >> "$PID_FILE"
done
echo "   1 根阻塞者(持锁不放) + 6 等待者(争用 id∈[1,200])"

# ── 因素2: I/O 冲高 ──
echo "=== 3/5 I/O 冲高 ==="

FULLSCAN_SQL=$(cat <<'SQL'
ALTER SESSION SET workarea_size_policy = 'MANUAL';
ALTER SESSION SET hash_area_size = 65536;
ALTER SESSION SET sort_area_size = 65536;
SELECT /*+ FULL(o) FULL(p) USE_HASH(p) */
       o.customer_id, p.category, SUM(o.amount), COUNT(*)
FROM load_orders o
JOIN load_products p ON o.product_id = p.id
WHERE o.status IN ('pending','processing')
GROUP BY o.customer_id, p.category
HAVING SUM(o.amount) > 500
ORDER BY 3 DESC
FETCH FIRST 100 ROWS ONLY;
EXIT
SQL
)

for i in 1 2 3 4; do
  (
    run_sql_loop "load_fullscan_$i" "$FULLSCAN_SQL" 0.5
  ) &
  echo $! >> "$PID_FILE"
done
echo "   4 个会话: 全表扫描 + hash join (hash_area_size=64K 强制溢出)"

# ── 因素3: Temp 溢出 + 大排序 ──
echo "=== 4/5 Temp 溢出 ==="

BIGSORT_SQL=$(cat <<'SQL'
ALTER SESSION SET workarea_size_policy = 'MANUAL';
ALTER SESSION SET sort_area_size = 65536;
ALTER SESSION SET hash_area_size = 65536;
SELECT * FROM (
  SELECT * FROM load_orders ORDER BY memo, amount DESC, created_at
) WHERE ROWNUM <= 50000;
EXIT
SQL
)

for i in 1 2; do
  (
    run_sql_loop "load_bigsort_$i" "$BIGSORT_SQL" 0.5
  ) &
  echo $! >> "$PID_FILE"
done

NESTED_SQL=$(cat <<'SQL'
ALTER SESSION SET workarea_size_policy = 'MANUAL';
ALTER SESSION SET sort_area_size = 65536;
ALTER SESSION SET hash_area_size = 65536;
WITH big AS (
  SELECT customer_id, SUM(amount) AS total, COUNT(*) AS cnt
  FROM load_orders GROUP BY customer_id
),
ranked AS (
  SELECT big.*, ROW_NUMBER() OVER (ORDER BY total DESC) AS rn FROM big
)
SELECT r.customer_id, r.total, r.cnt, p.category, p.price
FROM ranked r
JOIN load_products p ON MOD(r.customer_id, 100000) = p.id
WHERE r.rn <= 5000
ORDER BY p.price DESC;
EXIT
SQL
)

(
  run_sql_loop "load_nested_temp" "$NESTED_SQL" 1
) &
echo $! >> "$PID_FILE"
echo "   2 大排序 + 1 嵌套子查询 (sort/hash_area=64K 强制 temp 溢出)"

# ── 因素4: Redo/Undo 写入冲高 ──
echo "=== 5/5 Redo 写入冲高 ==="

for i in 1 2 3; do
  lo=$((($i - 1) * 3000 + 501))
  hi=$(($i * 3000 + 500))
  WRITER_SQL=$(cat <<SQL
INSERT INTO load_logs (session_id, action, payload)
SELECT $i, 'redo_write_' || LEVEL, RPAD('redo_data_', 300, 'x')
FROM dual CONNECT BY LEVEL <= 300;
COMMIT;

UPDATE load_orders SET amount = amount + 0.001
WHERE id BETWEEN $lo AND $hi
  AND MOD(customer_id, 5) = 0;
COMMIT;
EXIT
SQL
)
  (
    run_sql_loop "load_writer_$i" "$WRITER_SQL" 0.3
  ) &
  echo $! >> "$PID_FILE"
done
echo "   3 个写入会话: INSERT 300行/轮 + UPDATE ~1800行/轮"

# ── Janitor: 每 2 分钟 TRUNCATE load_logs，防止表/表空间膨胀 ──
(
  while true; do
    sleep 120
    printf 'TRUNCATE TABLE load_logs;\nEXIT\n' | run_sql "load_janitor" > "$LOG_DIR/load_janitor.log" 2>&1
  done
) &
echo $! >> "$PID_FILE"
echo "   Janitor: 每 120 秒 TRUNCATE load_logs (控制空间)"

echo ""
echo "============================================"
echo "  负载已就绪 — 共 ~16 个活跃/阻塞会话"
echo "============================================"
echo "  因素1: 锁争用链   (1根阻塞 + 6等待)"
echo "  因素2: I/O冲高     (4全表扫描+hash join)"
echo "  因素3: Temp溢出    (2大排序 + 1嵌套子查询)"
echo "  因素4: Redo冲高    (3持续写入)"
echo ""
echo "  关联效应:"
echo "  TX enq 锁等待 → 事务堆积 → 活跃会话↑"
echo "  全表扫描 → db file scattered read I/O等待 → 变慢"
echo "  hash/sort 强制 64K → direct path write temp → I/O 竞争"
echo "  持续 INSERT/UPDATE → log file sync / log buffer 压力"
echo ""
echo "  停止: bash /tmp/oracle_load_complex.sh stop"
echo "============================================"
