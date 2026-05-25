# python_table_maintenance_advisor

Python + Markdown pluggable skill for table maintenance and SQL performance risk triage.

## Install

```bash
cp -R python_table_maintenance_advisor ~/.opendb/skills/
dbaa -c gauss_local /skills reload
dbaa -c gauss_local /skills show python_table_maintenance_advisor
```

## Run

```bash
dbaa -c gauss_local /skills run python_table_maintenance_advisor '{"min_table_mb":32,"dead_pct_warn":20,"limit":15}'
```

Natural language examples:

- 当前哪些表统计信息过期，会影响 SQL 优化吗
- 帮我分析表膨胀和顺序扫描风险

## Production Requirements

- Python 3.8+.
- `gsql` or `psql` in `PATH`.
- Passwordless local auth, `.pgpass`, or customer-approved secret injection.

