# OG SQL Tuner — 6 个金标准案例

**版本**: v0.2
**用途**: P1-P5 验证集，每个案例都要让 tuner 跑通 + 给出预期方案
**OG 版本**: 5.0.0

每个案例提供：
- 业务场景描述
- 表 DDL + 数据生成 SQL
- 触发问题的 SQL
- 期望 tuner 抓到的关键发现
- 期望给出的方案
- 对应 7 项能力目标的覆盖

## 7 项能力目标覆盖矩阵

| 案例 | G1 索引 | G2 CBO 解读 | G3 统计 | G4 重写 | G5 PlanTrace | G6 HINT | G7 千行 |
|------|--------|------------|--------|--------|--------------|--------|---------|
| 1. 多表 JOIN 错误 join order | ✓ | ✓ | ✓ |  | ✓ | ✓ |  |
| 2. 三层嵌套子查询 → semi join |  | ✓ |  | ✓ |  |  |  |
| 3. 关联列统计偏差 | ✓ |  | ✓ |  | ✓ |  |  |
| 4. 函数包裹非 sargable 谓词 | ✓ |  |  | ✓ |  |  |  |
| 5. TPC-DS Q64 改造（百行级 CTE）|  | ✓ | ✓ | ✓ |  | ✓ | （部分） |
| **6. 真千行级财务报表 ETL** | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | **✓** |

6 案例**每个目标至少 2 个案例覆盖**，确保 tuner 能力闭环。

**关键差异**: 案例 5 (~120 行) 验证"百行级 CTE"基础能力；案例 6 (~1500 行) 验证 G7 真千行级专属能力（分段 + token 压缩 + 多轮深度模式）。

来源说明：
- 案例 1/2 综合自 PostgreSQL 文档 + 电商常见模式
- 案例 3/4 直接复刻 dev.to ["When ANALYZE Isn't Enough"](https://dev.to/michal_cyncynatus_3a792c2/when-analyze-isnt-enough-debugging-bad-row-estimation-in-postgresql-47n6) 真实生产 bug
- 案例 5 TPC-DS Query 64
- 案例 6 TPC-DS Q11/Q14/Q23/Q72/Q78 拼合，模拟真实零售 ETL 报表形态

---

## 案例 1: 5 表 JOIN + 错误 join order + 统计偏差

### 业务场景

电商订单分析: 找过去 30 天**特定商品类目**且**购买金额 > 1000 元**的**活跃用户**信息（含店铺信息）。

5 表：`users` × `orders` × `order_items` × `products` × `categories`。

### 表 DDL

```sql
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_active_at TIMESTAMP,
    status VARCHAR(20) NOT NULL  -- 'active' / 'inactive' / 'banned'
);

CREATE TABLE products (
    id BIGSERIAL PRIMARY KEY,
    category_id BIGINT NOT NULL,
    name VARCHAR(255),
    price NUMERIC(10,2)
);

CREATE TABLE categories (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100),
    parent_id BIGINT
);

CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    status VARCHAR(20) NOT NULL,  -- 'paid' / 'pending' / 'cancelled'
    total_amount NUMERIC(12,2)
);

CREATE TABLE order_items (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    quantity INT,
    price NUMERIC(10,2)
);

-- 故意 NOT 建外键索引, 仅有主键
```

### 数据规模

```sql
-- users: 100 万行 (其中 active=10%, 即 10 万)
INSERT INTO users (email, created_at, last_active_at, status)
SELECT 'user_' || i || '@x.com',
       NOW() - (random() * 365 || ' days')::interval,
       NOW() - (random() * 30 || ' days')::interval,
       CASE WHEN i % 10 = 0 THEN 'active' ELSE 'inactive' END
FROM generate_series(1, 1000000) i;

-- products: 5 万行
-- categories: 200 行  (其中 'electronics' 类目占 5%)
-- orders: 1000 万行 (status='paid' 占 80%)
-- order_items: 5000 万行
```

### 问题 SQL

```sql
SELECT u.id, u.email, u.last_active_at,
       SUM(oi.price * oi.quantity) AS total_spent
FROM users u
JOIN orders o ON o.user_id = u.id
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
JOIN categories c ON c.id = p.category_id
WHERE u.status = 'active'
  AND u.last_active_at > NOW() - INTERVAL '30 days'
  AND o.status = 'paid'
  AND o.created_at > NOW() - INTERVAL '30 days'
  AND c.name = 'electronics'
GROUP BY u.id, u.email, u.last_active_at
HAVING SUM(oi.price * oi.quantity) > 1000;
```

### 期望 tuner 抓到的发现

1. **CBO 选错 join 顺序** (G2/G5)
   - 实际上从 `categories.name='electronics'` 切入最高效（200 行 × 5% = 10 行类目）
   - CBO 因为 `categories` 没统计 `name='electronics'` 的选择性，选了 `users` 起始
   - PlanTrace 解读：CBO 把 categories scan rows 估为 200 而不是 10
2. **统计偏差** (G3): `users.status='active'` 实际 10%，但 `pg_stats` 因数据倾斜估为 50%
3. **缺索引** (G1):
   - `orders(user_id, created_at, status)` 复合索引可以让 user-orders join 变成 index-only scan
   - `order_items(order_id)` 缺索引导致 NL 内层走全表
4. **运行时**: 该 SQL 4 秒，扫了 5000 万 order_items 行的 80%

### 期望方案

**方案 1 (G6 HINT)**: 强制 join 顺序

```sql
SELECT /*+ leading((((c p) oi) o) u)
            indexscan(o idx_orders_user_status_time)
            hashjoin(c p) */
       u.id, u.email, ...
```

**方案 2 (G1 索引)**:

```sql
CREATE INDEX CONCURRENTLY idx_orders_user_status_time
  ON orders(user_id, status, created_at)
  WHERE status = 'paid';  -- 部分索引, 80% 数据命中

CREATE INDEX CONCURRENTLY idx_order_items_order
  ON order_items(order_id) INCLUDE (price, quantity);  -- covering
```

**方案 3 (G3 扩展统计)**:

```sql
-- 修复 categories.name 选择性估算
ALTER TABLE categories ALTER COLUMN name SET STATISTICS 1000;

-- 修复 users 的 status + last_active_at 关联
CREATE STATISTICS users_status_active
  (dependencies, mcv) ON status, last_active_at FROM users;

ANALYZE users;
ANALYZE categories;
```

**预期收益**: 4s → 80ms（**50× 提升**）

---

## 案例 2: 三层嵌套子查询 → semi join 重写

### 业务场景

找出"购买过商品 X 且过去 30 天活跃且邀请过其他用户注册"的核心用户。

### 表 DDL（复用案例 1 的 users / orders / order_items / products）+

```sql
CREATE TABLE referrals (
    id BIGSERIAL PRIMARY KEY,
    referrer_user_id BIGINT NOT NULL,
    referred_user_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);
CREATE INDEX idx_referrals_referrer ON referrals(referrer_user_id);
```

### 问题 SQL

```sql
SELECT u.id, u.email
FROM users u
WHERE u.status = 'active'
  AND u.last_active_at > NOW() - INTERVAL '30 days'
  AND u.id IN (
      SELECT o.user_id
      FROM orders o
      WHERE o.id IN (
          SELECT oi.order_id
          FROM order_items oi
          WHERE oi.product_id IN (
              SELECT p.id FROM products p WHERE p.name = 'iPhone 15'
          )
      )
  )
  AND u.id IN (
      SELECT r.referrer_user_id FROM referrals r
  );
```

### 期望 tuner 抓到的发现

1. **3 层嵌套 IN 阻止 subquery flatten** (G4/G2)
   - PG/OG 优化器对 4 层 IN 嵌套时**会放弃合并**到 join，把每层当独立子查询执行
   - 结果：4 次 NL 嵌套 + 重复扫表
2. **`referrals` 子查询无 WHERE 条件** = 全表扫
3. **`products.name = 'iPhone 15'` 命中 1 行**，应优先作为驱动条件

### 期望重写

**方案 1 (G4 重写为 EXISTS)**:

```sql
SELECT u.id, u.email
FROM users u
WHERE u.status = 'active'
  AND u.last_active_at > NOW() - INTERVAL '30 days'
  AND EXISTS (
      SELECT 1 FROM referrals r WHERE r.referrer_user_id = u.id
  )
  AND EXISTS (
      SELECT 1 FROM orders o
      JOIN order_items oi ON oi.order_id = o.id
      JOIN products p ON p.id = oi.product_id
      WHERE o.user_id = u.id
        AND p.name = 'iPhone 15'
  );
```

**方案 2 (G4 重写为 JOIN + DISTINCT)**:

```sql
SELECT DISTINCT u.id, u.email
FROM users u
JOIN referrals r ON r.referrer_user_id = u.id
JOIN orders o ON o.user_id = u.id
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
WHERE u.status = 'active'
  AND u.last_active_at > NOW() - INTERVAL '30 days'
  AND p.name = 'iPhone 15';
```

**等价性验证 (M4)**: 抽样跑两个版本，应当返回相同 user_id 集合。

**预期收益**: 12s → 200ms（EXISTS 让优化器走 semi-join 算法，单次扫描即可短路）

---

## 案例 3: 关联列统计偏差（G3 + G5）

**来源**: dev.to "When ANALYZE Isn't Enough" — 真实生产 bug 浓缩。

### 业务场景

多租户系统：`items` 表按 `organization_id` 隔离，每个 org 内由特定 `user_id` 拥有 items。

### 表 DDL

```sql
CREATE TABLE items (
    id BIGSERIAL PRIMARY KEY,
    organization_id INT NOT NULL,
    user_id UUID NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    payload JSONB
);

CREATE INDEX idx_items_org ON items(organization_id);
CREATE INDEX idx_items_user ON items(user_id);
```

### 数据特点（关键）

```sql
-- organization_id=1 是大客户，占 80% 的 items
-- 但每个 user_id 只属于一个 organization
-- 即 user_id → organization_id 是函数依赖

INSERT INTO items (organization_id, user_id, payload)
SELECT
  CASE WHEN i % 5 = 0 THEN (i % 100) + 2  -- 20% 散布到 100 个小 org
       ELSE 1 END AS organization_id,      -- 80% 集中在 org 1
  -- 同一 user_id 只属于一个 org（函数依赖）
  ('00000000-0000-0000-0000-' || lpad((i % 10000)::text, 12, '0'))::uuid AS user_id,
  '{}'::jsonb
FROM generate_series(1, 10000000) i;
```

### 问题 SQL

```sql
SELECT * FROM items
WHERE organization_id = 1
  AND user_id = '00000000-0000-0000-0000-000000000123';
```

### 期望 tuner 抓到的发现

1. **行数估算严重偏差** (G3)
   - 估算: `selectivity(org=1) × selectivity(user=X) = 0.8 × 0.0001 = 0.00008` → 估 800 行
   - 实际: user_id 已经唯一定位 1 个 org，actual rows = ~1000 行（user 在 org 1 的所有 items）
   - 估算 21 行 vs 实际 3650 行（dev.to 案例的真实数据）
2. **PlanTrace 解读 (G5)**: CBO 假设两个谓词独立，但实际 user_id → org_id 是函数依赖。trace 显示 CBO 把两个 selectivity 直接乘了
3. **算子选错**: 因低估行数，CBO 选了 NL + index lookup；如果估对会选 bitmap index scan

### 期望方案

**方案 1 (G3 扩展统计 — 修根因)**:

```sql
CREATE STATISTICS items_user_org (dependencies)
  ON user_id, organization_id FROM items;

ANALYZE items;
```

CBO 现在知道 user_id 决定 organization_id，估算自动修正为单列选择性 → 1000 行 → 选 bitmap scan。

**方案 2 (G1 复合索引备用)**:

```sql
CREATE INDEX CONCURRENTLY idx_items_user_org
  ON items(user_id, organization_id);
```

直接走复合索引绕过统计问题。

**方案选择 (PlanTrace 输出)**: 推荐方案 1（治本），方案 2 索引膨胀大但兜底可靠。

---

## 案例 4: 函数包裹非 sargable 谓词（G1 + G4）

**来源**: dev.to 同篇文章 + use-the-index-luke 经典案例。

### 业务场景

软件项目：找最近修改的记录，但 `updated_at` 可能为 NULL（新记录），用 `created_at` 兜底。

### 表 DDL

```sql
CREATE TABLE items (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP  -- nullable
);

-- 已有索引（错的）
CREATE INDEX idx_items_updated ON items(updated_at);
```

### 问题 SQL

```sql
-- 找 2026-02-01 之后被改过的 items
SELECT * FROM items
WHERE COALESCE(updated_at, created_at) > '2026-02-01';
```

### 期望 tuner 抓到的发现

1. **谓词不可 sargable** (G1)
   - `COALESCE(updated_at, created_at)` 是函数表达式，无法直接走索引
   - CBO 看不到这个表达式的选择性 → 估为默认 30%（文章案例：估 254 万，实际 0 行）
2. **现有索引完全没用** (G1): `idx_items_updated` 因函数包裹失效
3. **非 sargable 模式列表 (G4)**:
   - `WHERE func(col) = X` (此案例)
   - `WHERE col + 1 = X`
   - `WHERE substr(col, 1, 3) = 'abc'`
   - `WHERE col::text = '123'`（隐式转换）
   - `WHERE TRIM(col) = X`

### 期望方案

**方案 1 (G4 重写 — 优先)**:

```sql
SELECT * FROM items
WHERE updated_at > '2026-02-01'
   OR (updated_at IS NULL AND created_at > '2026-02-01');
```

CBO 现在能用 `idx_items_updated`（OR 第一支）+ 部分索引（OR 第二支）。

**方案 2 (G1 表达式索引 — 兜底)**:

```sql
CREATE INDEX CONCURRENTLY idx_items_last_modified
  ON items(COALESCE(updated_at, created_at));
```

如果业务高频用 COALESCE 形态，建表达式索引比改 SQL 更省事。

**方案 3 (G1 部分索引 — 极致优化)**:

```sql
-- 只对 updated_at IS NULL 的行建 created_at 索引
CREATE INDEX CONCURRENTLY idx_items_created_when_no_update
  ON items(created_at) WHERE updated_at IS NULL;
```

**预期收益**: 估算从 254 万 → 实际 0 行，避免全表扫；查询从 2.3s → 5ms。

---

## 案例 5: TPC-DS Query 64 改造 + ETL 风格扩展（G7 + 全部）

### 业务场景

零售连锁年度同店销售分析: 比较 2000 年 vs 2001 年同一商品在同一店铺的销售表现，过滤特定商品颜色/价格段。

**真实复杂度**:
- 119 行（原 TPC-DS Query 64）
- 16 表 JOIN
- 2 层 CTE
- 自连接（cross_sales × cross_sales）

### 涉及表（TPC-DS schema 摘录）

```
catalog_sales, catalog_returns, store_sales, store_returns,
date_dim, store, customer, customer_demographics, promotion,
household_demographics, customer_address, income_band, item,
warehouse, web_sales, web_returns
```

每表数据量从百万到亿级。

### 问题 SQL（节选关键段）

```sql
WITH cs_ui AS (
    SELECT cs_item_sk,
           SUM(cs_ext_list_price) AS sale,
           SUM(cr_refunded_cash + cr_reversed_charge + cr_store_credit) AS refund
    FROM catalog_sales, catalog_returns
    WHERE cs_item_sk = cr_item_sk
      AND cs_order_number = cr_order_number
    GROUP BY cs_item_sk
    HAVING SUM(cs_ext_list_price) > 2 * SUM(cr_refunded_cash + cr_reversed_charge + cr_store_credit)
),
cross_sales AS (
    SELECT i_product_name product_name, i_item_sk item_sk,
           s_store_name store_name, s_zip store_zip,
           ad1.ca_street_number b_street_number, ...
           ss_quantity AS s1_qty, sr_return_quantity AS sr1_qty, ...
           d1.d_year AS syear, ...
    FROM store_sales, store_returns, cs_ui, date_dim d1, date_dim d2,
         date_dim d3, store, customer, customer_demographics cd1,
         customer_demographics cd2, promotion, household_demographics hd1,
         household_demographics hd2, customer_address ad1, customer_address ad2,
         income_band ib1, income_band ib2, item
    WHERE ss_store_sk = s_store_sk
      AND ss_sold_date_sk = d1.d_date_sk
      AND ss_customer_sk = c_customer_sk
      AND ss_cdemo_sk = cd1.cd_demo_sk
      AND ... (16 个 join 条件 + 8 个谓词过滤)
)
SELECT cs1.product_name, cs1.store_name, cs2.s2_qty, ...
FROM cross_sales cs1, cross_sales cs2
WHERE cs1.item_sk = cs2.item_sk
  AND cs1.syear = 2000
  AND cs2.syear = 2001
  ...
ORDER BY cs1.product_name, cs1.store_name, cs2.cnt;
```

完整 SQL 见 [TPC-DS Q64](https://github.com/Altinity/tpc-ds/blob/master/queries/query_64.sql)。

### 期望 tuner 抓到的发现

**这是 G7 的核心案例 — tuner 必须能扛 100KB+ SQL**:

1. **分段 (G7 关键能力)**:
   - 自动识别两个 CTE 边界
   - EXPLAIN 整体后，找出 cost > 10% 的 8 个子树
   - 对每个子树独立分析

2. **CBO 解读 (G2/G5)**:
   - 16 表 join 顺序枚举：CBO 默认 `from_collapse_limit=8`，超过会用 GEQO（遗传算法）→ 不一定最优
   - 解读为何 CBO 选了次优 join order
   - 找出 cardinality 估算偏差最大的中间结果

3. **统计修复 (G3)**:
   - 这种多维分析查询对 `customer_demographics × household_demographics` 的关联统计敏感
   - 需 `CREATE STATISTICS` 多列依赖

4. **重写建议 (G4)**:
   - CTE inline vs 物化（OG 5.0 默认 inline，但有时手动 `MATERIALIZED` 能避免重复扫描）
   - 自连接 `cross_sales × cross_sales` 可能预计算一次然后 JOIN

5. **HINT 推荐 (G6)**:
   - `set(from_collapse_limit 16)` 让 CBO 完整枚举 join 顺序
   - `leading()` 锁定关键 join 路径
   - `set(work_mem '256MB')` 让 hash join 不溢出

### 期望方案模板

```sql
SELECT /*+
   set(from_collapse_limit 16)        -- 关闭 GEQO, 完整枚举
   set(work_mem '256MB')               -- hash 不溢出
   leading((cs_ui (item store)))       -- 锁定先 join cs_ui
   indexscan(store_sales idx_ss_date_store_item)
*/
WITH cs_ui AS MATERIALIZED (         -- 改 MATERIALIZED 避免双扫
   ...
), cross_sales AS (
   ...
)
...
```

外加：
- 3-4 个 column store / partition 改造建议
- 5-6 个新索引建议
- 扩展统计 4-5 个

### 预期 tuner 输出体量

报告 markdown ~200 行，覆盖：
- 整体 plan tree 摘要 + 8 个高 cost 子树详细分析
- 每个子树独立的优化方案
- 综合优化清单按预期收益排序

**预期工程难度**: G7 是最难的，需要 P5/P6 阶段重点攻坚。MVP（P1-P3）只需扛**百行级**（即 TPC-DS Q64 原始 119 行）。**真正千行级 SQL 留 P6**（见案例 6）。

---

## 案例 6: 真千行级财务报表 ETL（G7 专项）

**P4 阶段验证 G7 专属案例**。MVP 不跑，P4 千行专项时启用。

### 业务场景

零售连锁年度财务报表 ETL — 把全年销售、客户分群、商品类目排名、地区汇总在一条 SQL 里聚合输出，喂给 BI 工具生成董事会报告。这是真实生产环境的 ETL 形态。

### SQL 复杂度结构

| 维度 | 数量 | 说明 |
|------|------|------|
| 总行数 | ~1500 行 | SQL 文本 |
| CTE 数 | 12 | 含 5 个嵌套引用其他 CTE 的 CTE |
| UNION ALL 段 | 5 | 5 个区域（华东 / 华北 / 华南 / 西部 / 海外） |
| JOIN 表数 | 30+ | 含事实表 / 维度表 / 时间维 |
| 视图嵌套层级 | 4 | `v_dim_customer` → `v_customer_enriched` → `v_customer_segment` → `v_customer_segment_with_lifetime_value` |
| 窗口函数 | 6 | RANK / ROW_NUMBER / LAG / SUM OVER PARTITION 等 |
| 子查询 | 15+ | 含 3 层嵌套 |
| CASE WHEN | 30+ | 业务规则 |
| COALESCE | 20+ | NULL 处理 |

### SQL 来源（重要）

**不手写 1500 行**，而是用 TPC-DS 的 5 个长查询拼合：

```
案例 6 SQL = WITH (
  Q11 客户分群相关 CTE      -- 客户偏好分析
) AS (...),
  WITH (Q14 同店销售 CTE),  -- 跨年同店比较
  WITH (Q23 高价值客户 CTE), -- top 客户筛选
  WITH (Q72 库存周转 CTE),   -- 库存效率
  WITH (Q78 退货影响 CTE)    -- 退货扣减

主查询: 五段 UNION ALL 输出每区域综合报表行
```

每个 TPC-DS query 公开可下载 ([TPC-DS query suite](https://github.com/Altinity/tpc-ds/blob/master/queries/))，拼合后约 1500 行。

### 涉及表

复用 TPC-DS schema（24 表，scale=10 约 10GB 数据），重点表：

```
catalog_sales, store_sales, web_sales              -- 三个销售事实表 (亿级)
catalog_returns, store_returns, web_returns        -- 三个退货事实表 (千万级)
customer, customer_demographics, customer_address  -- 客户维度
date_dim, time_dim                                 -- 时间维度
item, promotion, store, warehouse                  -- 商品 / 促销 / 店铺
income_band, household_demographics                -- 人口统计
inventory                                          -- 库存事实
```

### 视图定义（增加复杂度）

为模拟真实业务封装，建 4 层嵌套视图：

```sql
-- Layer 1: 基础视图
CREATE VIEW v_dim_customer AS
SELECT c.*, ca.ca_state, ca.ca_country, cd.cd_marital_status
FROM customer c
LEFT JOIN customer_address ca ON c.c_current_addr_sk = ca.ca_address_sk
LEFT JOIN customer_demographics cd ON c.c_current_cdemo_sk = cd.cd_demo_sk;

-- Layer 2: 增强视图（引用 Layer 1）
CREATE VIEW v_customer_enriched AS
SELECT vc.*, hd.hd_income_band_sk, hd.hd_dep_count
FROM v_dim_customer vc
LEFT JOIN household_demographics hd ON vc.c_current_hdemo_sk = hd.hd_demo_sk;

-- Layer 3: 分群视图（引用 Layer 2）
CREATE VIEW v_customer_segment AS
SELECT vce.*,
  CASE
    WHEN ib.ib_lower_bound > 100000 THEN 'high_value'
    WHEN ib.ib_lower_bound > 50000  THEN 'medium_value'
    ELSE 'low_value'
  END AS segment
FROM v_customer_enriched vce
LEFT JOIN income_band ib ON vce.hd_income_band_sk = ib.ib_income_band_sk;

-- Layer 4: 终态视图（含窗口函数, 引用 Layer 3）
CREATE VIEW v_customer_segment_with_lifetime_value AS
SELECT vcs.*,
  SUM(ss.ss_net_paid) OVER (PARTITION BY vcs.c_customer_sk) AS lifetime_value,
  ROW_NUMBER() OVER (PARTITION BY vcs.segment ORDER BY ss_net_paid DESC) AS segment_rank
FROM v_customer_segment vcs
LEFT JOIN store_sales ss ON vcs.c_customer_sk = ss.ss_customer_sk;
```

主查询使用 `v_customer_segment_with_lifetime_value`，CBO 必须做 4 层 view inline 才能优化 — 这是 view 展开能力的硬测试。

### 期望 tuner 抓到的关键问题

1. **G7 分段策略生效** — tuner 必须自动检测 SQL > 1000 行，触发千行模式
2. **G3 统计偏差** — 5 个区域 UNION 段每段 selectivity 估算可能严重偏差
3. **CTE 重复计算** — 部分 CTE 被引用多次但 OG 默认 inline，导致重复扫描；部分 CTE 应该 MATERIALIZED
4. **G2 CBO 决策溯源** — 30+ 表 join 顺序枚举超过 from_collapse_limit=8，CBO 走 GEQO 不一定最优
5. **视图穿透失败** — 4 层视图嵌套，CBO 可能没完整 inline 导致重复扫描底层表
6. **窗口函数性能** — `SUM OVER PARTITION BY c_customer_sk` 在 1.2 亿客户上需要排序，是否走索引扫描省 sort
7. **存储模式** — 这种 ETL 报表是列存（OG 5.0 支持）的典型场景，建议改造表为 column store

### 期望方案模板

```sql
-- 综合方案（G6 HINT + G2 CBO + G7 分段优化）
SELECT /*+
  set(from_collapse_limit 30)         -- 关闭 GEQO, 完整 DP 枚举
  set(work_mem '512MB')                -- 大查询 hash 不溢出
  set(enable_hashagg 'on')             -- 强制 hash 聚合
  set(parallel_setup_cost 100)         -- 鼓励并行
  leading((dim_customer fact_sales))   -- 锁定关键 join 路径
*/
WITH cs_segment AS MATERIALIZED (...),  -- 强制 MATERIALIZED 避免重复扫描
     ws_segment AS MATERIALIZED (...),
     ...
```

外加：
- 5-8 个新索引建议（针对热点 fact 表）
- 3-4 个 column store 改造建议（针对 fact 表）
- 6-8 个扩展统计建议（针对关联维度）
- 2-3 个视图改造建议（合并 4 层视图为 1 层 materialized view）

### 关键测试指标（G7 验证）

| 指标 | 目标 | 不达标含义 |
|------|------|----------|
| 总耗时 | ≤ 8 min | 千行档目标 |
| 自动升级触发 | 是 | 验证升级机制 |
| LLM prompt token | < 100K | 验证 token 压缩生效 |
| 找出真问题数 | ≥ 6 类 | 缺索引/统计偏差/CTE/join 顺序/视图穿透/窗口 |
| 至少 1 方案让整体 cost ≤ 30% | 是 | 验证有效性 |
| 报告覆盖每个区域段 | 是 | 验证分段不漏 |

### 数据规模

复用 TPC-DS scale=10：约 10 GB 落盘，刚好测试 OG 实例容量。

### 实施时机

**P4 阶段才启用**。P4 之前用案例 5（TPC-DS Q64, 119 行）验证 G7 的"分段策略机制是否成立"就够了。真千行 SQL 是性能 + token 压缩的极限测试。

---

## 数据初始化脚本

`scripts/sqltune_golden_setup.sh` 一键起 6 个 case 的 schema + data：

```bash
#!/bin/bash
# OG 测试机执行
psql -h 47.251.30.180 -p 15432 -U opendb -d postgres \
  -f case_1_multi_join_schema.sql \
  -f case_2_nested_subquery_schema.sql \
  -f case_3_correlated_stats_schema.sql \
  -f case_4_non_sargable_schema.sql \
  -f case_5_tpcds_q64_schema.sql

# Case 6 复用 case 5 的 TPC-DS schema, 额外加 4 层视图定义
psql ... -f case_6_views_extra.sql

# 各 case 数据生成（避免一次性 50 GB）
for i in 1 2 3 4 5; do
  psql ... -c "\\i case_${i}_data.sql"
done
# Case 6 不生成额外数据 (复用 case 5 数据)
```

### 数据规模总览

| Case | 总行数 | 落盘大小 | 阶段 |
|------|-------|---------|------|
| 1 | 1.6 亿（5 表） | ~10 GB | P1-P5 |
| 2 | 复用 case 1 + referrals 100 万 | +100 MB | P2-P5 |
| 3 | 1000 万 items | ~1 GB | P2-P5 |
| 4 | 1000 万 items（同 case 3 schema） | ~1 GB | P2-P5 |
| 5 | TPC-DS scale=10（最小测试规模） | ~10 GB | P3-P5 |
| **6** | **复用 case 5 + 4 层视图定义** | **+0 GB** | **P4-P5** |

总计 ~22 GB（case 6 复用 case 5 数据，零增量），够覆盖 OG 测试机磁盘容量。

---

## 验证标准

每个 case 跑 tuner 后，输出报告必须满足：

| 标准 | 要求 |
|------|------|
| 抓到根因 | tuner 输出包含上述"期望发现"的 ≥ 80% |
| 给出方案 | 至少 1 个方案能让 EXPLAIN cost 降到原来 ≤ 20% |
| 无幻觉 | 不出现不存在的索引名 / 表列 / OG 不支持的语法 |
| 等价性（重写方案）| 抽样验证通过或明确标 unverified |
| 解释 CBO 决策 (G2/G5) | 不只说"应该改 X"，要说"CBO 因为 Y 选了 Z，改 X 后 CBO 会选 W" |

---

## 参考资料

- [TPC-DS Query 64 source](https://github.com/Altinity/tpc-ds/blob/master/queries/query_64.sql)
- [PostgreSQL Extended Statistics docs](https://www.postgresql.org/docs/current/planner-stats.html)
- [When ANALYZE Isn't Enough — dev.to](https://dev.to/michal_cyncynatus_3a792c2/when-analyze-isnt-enough-debugging-bad-row-estimation-in-postgresql-47n6)
- [openGauss Plan Hint docs](https://docs.opengauss.org/en/docs/5.0.0/docs/PerformanceTuningGuide/scan-operation-hints.html)
- [PostgreSQL Index-Only Scans and Covering Indexes](https://www.postgresql.org/docs/current/indexes-index-only-scans.html)
- [Cybertec — Subqueries and performance in PostgreSQL](https://www.cybertec-postgresql.com/en/subqueries-and-performance-in-postgresql/)
