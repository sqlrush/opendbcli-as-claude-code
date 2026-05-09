#!/bin/bash
# pg_load_complex.sh — PG 多因素关联复杂负载
# 场景: 锁等待链 + I/O冲高 + Temp溢出 + WAL冲高 + 活跃会话堆积
# 用法: bash /tmp/pg_load_complex.sh        (启动)
#       bash /tmp/pg_load_complex.sh stop    (停止)

export PGPASSWORD='YourPgPass123!'
PG="psql -h 127.0.0.1 -p 5432 -U postgres -d postgres"

# ── 停止模式 ──
if [ "$1" = "stop" ]; then
    echo "=== 停止负载 ==="
    cat /tmp/pg_load_pids 2>/dev/null | xargs kill 2>/dev/null
    $PG -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE 'load_%' AND pid <> pg_backend_pid();" 2>/dev/null
    $PG -c "DROP TABLE IF EXISTS load_orders, load_products, load_logs CASCADE;" 2>/dev/null
    rm -f /tmp/pg_load_pids
    echo "=== 已停止并清理 ==="
    exit 0
fi

echo "=== 1/5 准备测试数据 ==="

$PG -q << 'SQL'
DROP TABLE IF EXISTS load_orders, load_products, load_logs CASCADE;

CREATE TABLE load_orders (
    id          SERIAL PRIMARY KEY,
    customer_id INT NOT NULL,
    product_id  INT NOT NULL,
    amount      NUMERIC(12,2),
    status      TEXT DEFAULT 'pending',
    memo        TEXT,
    created_at  TIMESTAMP DEFAULT now()
);

CREATE TABLE load_products (
    id          SERIAL PRIMARY KEY,
    name        TEXT,
    category    TEXT,
    price       NUMERIC(10,2),
    description TEXT
);

CREATE TABLE load_logs (
    id          SERIAL PRIMARY KEY,
    session_id  INT,
    action      TEXT,
    payload     TEXT,
    created_at  TIMESTAMP DEFAULT now()
);

INSERT INTO load_orders (customer_id, product_id, amount, status, memo)
SELECT
    (random()*10000)::INT,
    (random()*100000)::INT,
    (random()*9999)::NUMERIC(12,2),
    CASE (random()*4)::INT WHEN 0 THEN 'pending' WHEN 1 THEN 'processing' WHEN 2 THEN 'shipped' ELSE 'completed' END,
    repeat('x', (random()*200)::INT)
FROM generate_series(1, 1000000);

INSERT INTO load_products (name, category, price, description)
SELECT 'product_' || i, 'cat_' || (i % 50), (random()*999)::NUMERIC(10,2), repeat('desc_payload_', 50)
FROM generate_series(1, 100000) AS i;

ANALYZE load_orders;
ANALYZE load_products;
SQL
echo "   load_orders(100万行), load_products(10万行), load_logs 就绪"

# ── PID 文件 ──
> /tmp/pg_load_pids

# ── 因素1: 锁争用链 (根阻塞者 + 等待者) ──
echo "=== 2/5 锁争用链 ==="

# 根阻塞者: BEGIN → UPDATE → pg_sleep(永远不提交)
$PG -c "SET application_name='load_lock_root'" << 'SQL' &
BEGIN;
UPDATE load_orders SET status = 'LOCKED_ROOT' WHERE id BETWEEN 1 AND 200;
SELECT pg_sleep(86400);
ROLLBACK;
SQL
echo $! >> /tmp/pg_load_pids
sleep 2

# 6 个等待者: 尝试更新同一批行 → 被阻塞
for i in 1 2 3 4 5 6; do
  (
    while true; do
      $PG -c "SET application_name='load_lock_waiter_$i'" -c "UPDATE load_orders SET status='wait_$i' WHERE id = $((i * 30 + 1));" 2>/dev/null
      sleep 1
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done
echo "   1 根阻塞者(持锁不放) + 6 等待者(争用同一批行)"

# ── 因素2: I/O 冲高 (全表扫描 + hash join) ──
echo "=== 3/5 I/O 冲高 ==="

for i in 1 2 3 4; do
  (
    while true; do
      $PG -c "SET application_name='load_fullscan_$i'" -c "SET work_mem='2MB'" -c "
        SELECT o.customer_id, p.category, SUM(o.amount), COUNT(*)
        FROM load_orders o
        JOIN load_products p ON o.product_id = p.id
        WHERE o.status IN ('pending','processing')
        GROUP BY o.customer_id, p.category
        HAVING SUM(o.amount) > 500
        ORDER BY 3 DESC LIMIT 100;" > /dev/null 2>&1
      sleep 0.5
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done
echo "   4 个会话: 全表扫描 + hash join (work_mem=2MB 强制溢出)"

# ── 因素3: Temp 溢出 + 大排序 ──
echo "=== 4/5 Temp 溢出 ==="

for i in 1 2; do
  (
    while true; do
      $PG -c "SET application_name='load_bigsort_$i'" -c "SET work_mem='1MB'" -c "
        SELECT * FROM load_orders ORDER BY memo, amount DESC, created_at LIMIT 50000;" > /dev/null 2>&1
      sleep 0.5
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done

# 子查询嵌套 → 多层 temp
(
  while true; do
    $PG -c "SET application_name='load_nested_temp'" -c "SET work_mem='1MB'" -c "
      WITH big AS (
        SELECT customer_id, SUM(amount) AS total, COUNT(*) AS cnt
        FROM load_orders GROUP BY customer_id
      ),
      ranked AS (
        SELECT *, ROW_NUMBER() OVER (ORDER BY total DESC) AS rn FROM big
      )
      SELECT r.*, p.category, p.price
      FROM ranked r
      JOIN load_products p ON r.customer_id % 100000 = p.id
      WHERE r.rn <= 5000
      ORDER BY p.price DESC;" > /dev/null 2>&1
    sleep 1
  done
) &
echo $! >> /tmp/pg_load_pids
echo "   2 大排序 + 1 嵌套子查询 (work_mem=1MB 强制 temp 溢出)"

# ── 因素4: WAL 写入冲高 ──
echo "=== 5/5 WAL 写入冲高 ==="

for i in 1 2 3; do
  (
    while true; do
      $PG -c "SET application_name='load_writer_$i'" -c "
        INSERT INTO load_logs (session_id, action, payload)
        SELECT $i, 'wal_write_' || gs, repeat('wal_data_', 30)
        FROM generate_series(1, 300) gs;" > /dev/null 2>&1
      $PG -c "SET application_name='load_writer_$i'" -c "
        UPDATE load_orders SET amount = amount + 0.001
        WHERE id BETWEEN (($i-1)*3000+501) AND ($i*3000+500)
          AND customer_id % 5 = 0;" > /dev/null 2>&1
      sleep 0.3
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done
echo "   3 个写入会话: INSERT 300行/轮 + UPDATE ~1800行/轮"

echo ""
echo "============================================"
echo "  负载已就绪 — 共 ~16 个活跃会话"
echo "============================================"
echo "  因素1: 锁争用链  (1根阻塞 + 6等待)"
echo "  因素2: I/O冲高    (4全表扫描+hash join)"
echo "  因素3: Temp溢出   (2大排序 + 1嵌套子查询)"
echo "  因素4: WAL冲高    (3持续写入)"
echo ""
echo "  关联效应:"
echo "  锁等待→事务堆积→活跃会话↑"
echo "  全表扫描→I/O等待→查询变慢→会话进一步堆积"
echo "  hash join+sort→temp溢出→磁盘I/O↑→全局变慢"
echo "  持续写入→WAL↑→checkpoint压力→I/O竞争加剧"
echo ""
echo "  停止: bash /tmp/pg_load_complex.sh stop"
echo "============================================"

wait
