# Schema 类 Skill 三库对比明细

## 1. /tableinfo — 表详情

### 列信息

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `dba_tab_columns` | `information_schema.COLUMNS` | `information_schema.COLUMNS` 或 `pg_attribute` |
| Schema 限定 | `owner = NVL(UPPER(:owner), USER)` | `TABLE_SCHEMA = :schema` | `table_schema = :schema` |
| 大小写 | 默认大写 | 大小写敏感（取决于 lower_case_table_names） | 默认小写 |

#### MySQL SQL
```sql
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE,
       COLUMN_DEFAULT, EXTRA, COLUMN_COMMENT
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = :schema AND TABLE_NAME = :table
ORDER BY ORDINAL_POSITION
```

#### PostgreSQL SQL
```sql
SELECT a.attname AS column_name,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
       NOT a.attnotnull AS nullable,
       pg_get_expr(d.adbin, d.adrelid) AS default_value,
       col_description(a.attrelid, a.attnum) AS comment
FROM pg_attribute a
LEFT JOIN pg_attrdef d ON a.attrelid = d.adrelid AND a.attnum = d.adnum
WHERE a.attrelid = :table_oid AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum
```

### 索引信息

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `dba_indexes` + `dba_ind_columns` | `SHOW INDEX FROM table` 或 `information_schema.STATISTICS` | `pg_indexes` 或 `pg_index` + `pg_class` |
| 聚合列 | `LISTAGG(column_name)` | `GROUP_CONCAT(COLUMN_NAME)` | `string_agg(a.attname, ', ')` |

#### MySQL SQL
```sql
SELECT INDEX_NAME,
       CASE WHEN NON_UNIQUE = 0 THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
       INDEX_TYPE,
       GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) AS columns
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = :schema AND TABLE_NAME = :table
GROUP BY INDEX_NAME, NON_UNIQUE, INDEX_TYPE
```

#### PostgreSQL SQL
```sql
SELECT i.relname AS index_name,
       CASE WHEN ix.indisunique THEN 'UNIQUE' ELSE 'NONUNIQUE' END AS uniqueness,
       CASE WHEN ix.indisvalid THEN 'VALID' ELSE 'INVALID' END AS status,
       pg_get_indexdef(ix.indexrelid) AS definition
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
WHERE ix.indrelid = :table_oid
ORDER BY i.relname
```

### 表统计

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `dba_tables` (num_rows, blocks, avg_row_len, last_analyzed) | `information_schema.TABLES` (TABLE_ROWS, AVG_ROW_LENGTH, DATA_LENGTH) | `pg_stat_user_tables` (n_live_tup, n_dead_tup, last_analyze) |

#### MySQL SQL
```sql
SELECT TABLE_ROWS, AVG_ROW_LENGTH,
       ROUND(DATA_LENGTH / 1048576, 2) AS data_mb,
       ROUND(INDEX_LENGTH / 1048576, 2) AS index_mb,
       UPDATE_TIME AS last_analyzed
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = :schema AND TABLE_NAME = :table
```

#### PostgreSQL SQL
```sql
SELECT n_live_tup AS num_rows,
       n_dead_tup AS dead_rows,
       pg_table_size(:table_full) / 1048576 AS table_mb,
       pg_indexes_size(:table_full) / 1048576 AS index_mb,
       last_analyze, last_autoanalyze
FROM pg_stat_user_tables
WHERE schemaname = :schema AND relname = :table
```

### 约束信息

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `dba_constraints` | `information_schema.TABLE_CONSTRAINTS` + `KEY_COLUMN_USAGE` + `REFERENTIAL_CONSTRAINTS` | `pg_constraint` + `pg_class` |
| 类型 | P(PK), U(Unique), R(FK), C(Check) | PRIMARY KEY, UNIQUE, FOREIGN KEY (无 CHECK 在 MySQL 8.0.16 前) | p(PK), u(Unique), f(FK), c(Check), x(Exclusion) |

#### MySQL SQL
```sql
SELECT tc.CONSTRAINT_NAME, tc.CONSTRAINT_TYPE,
       GROUP_CONCAT(kcu.COLUMN_NAME ORDER BY kcu.ORDINAL_POSITION) AS columns,
       kcu.REFERENCED_TABLE_NAME, kcu.REFERENCED_COLUMN_NAME
FROM information_schema.TABLE_CONSTRAINTS tc
JOIN information_schema.KEY_COLUMN_USAGE kcu
  ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
  AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA
WHERE tc.TABLE_SCHEMA = :schema AND tc.TABLE_NAME = :table
GROUP BY tc.CONSTRAINT_NAME, tc.CONSTRAINT_TYPE,
         kcu.REFERENCED_TABLE_NAME, kcu.REFERENCED_COLUMN_NAME
```

#### PostgreSQL SQL
```sql
SELECT conname AS constraint_name,
       CASE contype
         WHEN 'p' THEN 'PRIMARY KEY'
         WHEN 'u' THEN 'UNIQUE'
         WHEN 'f' THEN 'FOREIGN KEY'
         WHEN 'c' THEN 'CHECK'
         WHEN 'x' THEN 'EXCLUSION'
       END AS constraint_type,
       pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = :table_oid
ORDER BY contype
```

### MySQL/PG 额外信息

| 信息 | MySQL | PostgreSQL |
|------|-------|-----------|
| 表引擎 | ENGINE (InnoDB/MyISAM/...) | - |
| 字符集 | TABLE_COLLATION | - |
| 分区 | `information_schema.PARTITIONS` | `pg_inherits` (继承/分区) |
| 注释 | TABLE_COMMENT, COLUMN_COMMENT | `obj_description()`, `col_description()` |
| 触发器 | `information_schema.TRIGGERS` | `pg_trigger` |
| Toast | - | `pg_class WHERE reltoastrelid` |
| Bloat | - | `pgstattuple` 扩展 |

---

## 2. /indexadvise — 索引建议

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 计划获取 | `DBMS_XPLAN.DISPLAY/DISPLAY_CURSOR` | `EXPLAIN FORMAT=JSON` | `EXPLAIN (FORMAT JSON)` |
| 全表扫描检测 | 解析 "TABLE ACCESS FULL" | 解析 `"access_type": "ALL"` | 解析 `"Node Type": "Seq Scan"` |
| 谓词信息 | `v$sql_plan.filter_predicates` | EXPLAIN JSON `attached_condition` | EXPLAIN JSON `Filter` |

### MySQL 等价
```sql
EXPLAIN FORMAT=JSON SELECT ...;
```
解析 JSON 输出中的：
- `access_type: "ALL"` → 全表扫描
- `access_type: "index"` → 全索引扫描
- `attached_condition` → WHERE 条件（用于建议索引列）
- `rows_examined_per_scan` → 扫描行数

### PostgreSQL 等价
```sql
EXPLAIN (FORMAT JSON) SELECT ...;
```
解析 JSON 输出中的：
- `Node Type: "Seq Scan"` → 全表扫描
- `Filter` → WHERE 条件
- `Rows Removed by Filter` → 过滤掉的行数

### 索引建议逻辑差异

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 索引类型 | B-tree, Bitmap, Function-based | B-tree, Hash (Memory), FULLTEXT, SPATIAL | B-tree, Hash, GIN, GiST, SP-GiST, BRIN |
| 建议格式 | `CREATE INDEX ... ON table(col)` | `ALTER TABLE ... ADD INDEX idx_name(col)` | `CREATE INDEX ... ON table(col)` |
| 覆盖索引 | 无特殊语法 | `CREATE INDEX ... ON table(col1) INCLUDE (col2)` (8.0不支持) | `CREATE INDEX ... ON table(col) INCLUDE (col2)` (PG 11+) |
| 部分索引 | 不支持 | 不支持 | `CREATE INDEX ... WHERE condition` (PG 独有优势) |

### PG 独有索引建议
- **部分索引**：如果 WHERE 条件高度选择性，建议 `CREATE INDEX ... WHERE status = 'active'`
- **BRIN 索引**：如果是时序数据且列值与物理顺序相关，建议 `CREATE INDEX ... USING BRIN`
- **GIN 索引**：如果是 JSONB 或全文搜索，建议 `CREATE INDEX ... USING GIN`
