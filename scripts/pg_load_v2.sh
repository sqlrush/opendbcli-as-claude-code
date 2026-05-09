#!/bin/bash
# pg_load_v2.sh — PG 多因素关联复杂负载（可靠版）
# 停止: bash /tmp/pg_load_v2.sh stop

export PGPASSWORD='YourPgPass123!'
H=127.0.0.1 P=5432 U=postgres D=postgres

pg() { psql -h $H -p $P -U $U -d $D "$@"; }

if [ "$1" = "stop" ]; then
    kill $(cat /tmp/pg_load_pids 2>/dev/null) 2>/dev/null
    sleep 1
    kill -9 $(cat /tmp/pg_load_pids 2>/dev/null) 2>/dev/null
    pg -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE 'load_%' AND pid<>pg_backend_pid();" 2>/dev/null
    pg -c "DROP TABLE IF EXISTS load_orders, load_products, load_logs CASCADE;" 2>/dev/null
    rm -f /tmp/pg_load_pids
    echo "stopped and cleaned"
    exit 0
fi

> /tmp/pg_load_pids

# ── 因素1: 锁争用链 ──
echo "[1/4] 锁争用链: 1根阻塞者 + 6等待者"

# 根阻塞者 — 单个 psql 连接，BEGIN+UPDATE+SLEEP 在同一个 session
pg -v ON_ERROR_STOP=1 << 'SQL' &
SET application_name = 'load_lock_root';
BEGIN;
UPDATE load_orders SET status = 'LOCKED' WHERE id BETWEEN 1 AND 200;
SELECT pg_sleep(86400);
ROLLBACK;
SQL
echo $! >> /tmp/pg_load_pids
sleep 2

for i in 1 2 3 4 5 6; do
  ( while true; do
      pg << EOF
SET application_name = 'load_lock_waiter_$i';
SET statement_timeout = '25s';
UPDATE load_orders SET status = 'w$i' WHERE id = $((i * 30));
EOF
      sleep 1
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done

# ── 因素2: I/O 冲高 (全表扫描 + hash join) ──
echo "[2/4] I/O冲高: 4个全表扫描+hash join"

for i in 1 2 3 4; do
  ( while true; do
      pg << 'SQL' > /dev/null 2>&1
SET application_name = 'load_fullscan';
SET work_mem = '2MB';
SELECT o.customer_id, p.category, SUM(o.amount), COUNT(*)
FROM load_orders o
JOIN load_products p ON o.product_id = p.id
WHERE o.status IN ('pending','processing')
GROUP BY o.customer_id, p.category
HAVING SUM(o.amount) > 500
ORDER BY 3 DESC LIMIT 100;
SQL
      sleep 0.5
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done

# ── 因素3: Temp 溢出 ──
echo "[3/4] Temp溢出: 2大排序 + 1嵌套CTE"

for i in 1 2; do
  ( while true; do
      pg << 'SQL' > /dev/null 2>&1
SET application_name = 'load_bigsort';
SET work_mem = '1MB';
SELECT * FROM load_orders ORDER BY memo, amount DESC, created_at LIMIT 50000;
SQL
      sleep 0.5
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done

( while true; do
    pg << 'SQL' > /dev/null 2>&1
SET application_name = 'load_cte_temp';
SET work_mem = '1MB';
WITH big AS (
  SELECT customer_id, SUM(amount) AS total, COUNT(*) AS cnt
  FROM load_orders GROUP BY customer_id
),
ranked AS (
  SELECT *, ROW_NUMBER() OVER (ORDER BY total DESC) AS rn FROM big
)
SELECT r.*, p.category, p.price
FROM ranked r JOIN load_products p ON r.customer_id % 100000 = p.id
WHERE r.rn <= 5000 ORDER BY p.price DESC;
SQL
    sleep 1
  done
) &
echo $! >> /tmp/pg_load_pids

# ── 因素4: WAL 写入冲高 ──
echo "[4/4] WAL冲高: 3个持续写入"

for i in 1 2 3; do
  ( while true; do
      pg << EOF > /dev/null 2>&1
SET application_name = 'load_writer_$i';
INSERT INTO load_logs (session_id, action, payload)
SELECT $i, 'w' || gs, repeat('d', 300)
FROM generate_series(1, 300) gs;
UPDATE load_orders SET amount = amount + 0.001
WHERE id BETWEEN (($i-1)*3000+501) AND ($i*3000+500)
  AND customer_id % 5 = 0;
EOF
      sleep 0.3
    done
  ) &
  echo $! >> /tmp/pg_load_pids
done

echo ""
echo "===== 负载就绪 (~16会话) ====="
echo "因素1: 锁争用链 (1根+6等待)"
echo "因素2: I/O冲高   (4全表扫描)"
echo "因素3: Temp溢出  (2排序+1CTE)"
echo "因素4: WAL冲高   (3写入)"
echo ""
echo "停止: bash /tmp/pg_load_v2.sh stop"
echo "==============================="

wait
