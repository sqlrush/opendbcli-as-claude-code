# Generate a 1000+ line synthetic ETL-style SQL using OG bench tables
# Structure mimics TPC-DS reporting: 10 CTEs + 5-region UNION + nested subqueries

regions = [
    ("east",   "uid % 10 = 0"),
    ("ne",     "uid % 10 = 1"),
    ("se",     "uid % 10 = 2"),
    ("north",  "uid % 5 = 1"),
    ("south",  "uid % 5 = 2"),
    ("west",   "uid % 5 = 3"),
    ("oversea","uid % 10 = 9"),
    ("apac",   "uid % 10 = 5"),
    ("emea",   "uid % 10 = 6"),
    ("latam",  "uid % 10 = 7"),
    ("africa", "uid % 10 = 8"),
]

ctes = []

# CTE 1: bench_mix_a 的活跃 status 分布
ctes.append("""mix_a_active AS (
    SELECT id, status, payload, ROW_NUMBER() OVER (PARTITION BY status ORDER BY id) AS rn
    FROM bench_mix_a
    WHERE status > 0
)""")

# CTE 2-3: 引用 CTE 1 的嵌套 CTE
ctes.append("""mix_a_top10 AS (
    SELECT * FROM mix_a_active WHERE rn <= 10
)""")
ctes.append("""mix_a_summary AS (
    SELECT status, count(*) AS cnt, avg(id) AS avg_id, sum(length(payload)) AS payload_bytes
    FROM mix_a_active
    GROUP BY status
    HAVING count(*) > 100
)""")

# CTE 4: bench_mix_b 范围聚合
ctes.append("""mix_b_buckets AS (
    SELECT
        FLOOR(uid / 100000.0) * 100000 AS uid_bucket,
        count(*) AS cnt,
        count(DISTINCT name) AS distinct_names
    FROM bench_mix_b
    GROUP BY 1
)""")

# CTE 5: bench_og_hot 高频 uid
ctes.append("""hot_top_uids AS (
    SELECT uid, count(*) AS access_cnt
    FROM bench_og_hot
    GROUP BY uid
    HAVING count(*) > 1
    ORDER BY 2 DESC
    LIMIT 1000
)""")

# CTE 6: 跨表 join
ctes.append("""cross_a_b AS (
    SELECT a.id, a.status, b.uid, b.name
    FROM bench_mix_a a
    JOIN bench_mix_b b ON b.id = a.id
    WHERE a.status BETWEEN 1 AND 5
)""")

# CTE 7-8: 嵌套引用 CTE 6
ctes.append("""cross_a_b_filtered AS (
    SELECT * FROM cross_a_b WHERE uid > 100000
)""")
ctes.append("""cross_a_b_agg AS (
    SELECT status, uid % 1000 AS uid_mod, count(*) AS cnt
    FROM cross_a_b_filtered
    GROUP BY status, uid % 1000
)""")

# CTE 9: 多表三联 join
ctes.append("""triple_join AS (
    SELECT a.id, a.status, b.uid AS b_uid, h.uid AS h_uid
    FROM bench_mix_a a
    JOIN bench_mix_b b ON b.id = a.id
    JOIN bench_og_hot h ON h.id = a.id
    WHERE a.status > 0
)""")

# CTE 10: 窗口函数
ctes.append("""windowed AS (
    SELECT
        id, status, payload,
        ROW_NUMBER() OVER (PARTITION BY status ORDER BY id) AS row_in_status,
        LAG(status) OVER (ORDER BY id) AS prev_status,
        SUM(status) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS running_sum
    FROM bench_mix_a
)""")

# Build 5-region UNION main body
region_parts = []
for region, predicate in regions:
    part = f"""SELECT
    '{region}' AS region,
    s.status,
    s.cnt AS active_cnt,
    s.payload_bytes,
    b.uid_bucket,
    b.cnt AS bucket_cnt,
    b.distinct_names,
    COALESCE(h.access_cnt, 0) AS hot_access,
    cab.uid_mod,
    cab.cnt AS join_cnt,
    tj.h_uid AS triple_h_uid,
    w.running_sum,
    w.prev_status,
    CASE
        WHEN s.cnt > 1000 THEN 'high'
        WHEN s.cnt > 100  THEN 'medium'
        ELSE 'low'
    END AS density,
    /* 30+ CASE 表达式 */
    CASE WHEN s.payload_bytes > 1000000 THEN 'big' ELSE 'small' END AS payload_size,
    CASE WHEN b.distinct_names > 10000 THEN 'diverse' ELSE 'concentrated' END AS diversity,
    CASE WHEN h.access_cnt > 5 THEN 'hot' ELSE 'cold' END AS heat,
    /* COALESCE 链 */
    COALESCE(h.access_cnt, b.cnt, s.cnt, 0) AS combined_metric,
    /* 子查询 */
    (SELECT count(*) FROM bench_og_hot WHERE uid = h.uid AND id < 1000) AS sub_count_1,
    (SELECT max(id) FROM bench_mix_a WHERE status = s.status) AS sub_max_id,
    (SELECT avg(uid)::bigint FROM bench_mix_b WHERE id < 10000 AND uid % 5 = s.status % 5) AS sub_avg_uid,
    /* 窗口 */
    NTILE(10) OVER (PARTITION BY '{region}' ORDER BY s.cnt DESC) AS region_decile
FROM mix_a_summary s
LEFT JOIN mix_b_buckets b ON b.uid_bucket / 100000 = s.status
LEFT JOIN hot_top_uids h ON h.uid % 100 = s.status
LEFT JOIN cross_a_b_agg cab ON cab.status = s.status
LEFT JOIN triple_join tj ON tj.status = s.status
LEFT JOIN windowed w ON w.status = s.status AND w.row_in_status <= 10
WHERE s.cnt > 50
  AND ({predicate})
"""
    region_parts.append(part)

# Final SQL
sql_parts = ["WITH"]
for i, cte in enumerate(ctes):
    if i < len(ctes) - 1:
        sql_parts.append(cte + ",")
    else:
        sql_parts.append(cte)

sql_parts.append("\nSELECT * FROM (")
sql_parts.append("\nUNION ALL\n".join(region_parts))
sql_parts.append(") final\n")
sql_parts.append("WHERE active_cnt > 100")
sql_parts.append("ORDER BY region, density, status")
sql_parts.append("LIMIT 1000;")

print("\n".join(sql_parts))
