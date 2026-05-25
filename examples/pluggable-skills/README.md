# DBAA Pluggable Skill Production Examples

This directory contains three complete external skill examples that can be copied directly to `~/.opendb/skills/`.

Each example is read-only and targets OpenGauss/GaussDB performance diagnostics:

- `shell_wait_chain_triage`: shell + md, real-time wait/lock/long-transaction triage.
- `python_table_maintenance_advisor`: python + md, table bloat/statistics/sequential-scan maintenance advisor.
- `go_plan_hotspot_analyzer`: go + md, EXPLAIN plan hotspot analyzer.

Install:

```bash
mkdir -p ~/.opendb/skills
cp -R examples/pluggable-skills/shell_wait_chain_triage ~/.opendb/skills/
cp -R examples/pluggable-skills/python_table_maintenance_advisor ~/.opendb/skills/
cp -R examples/pluggable-skills/go_plan_hotspot_analyzer ~/.opendb/skills/
dbaa -c <conn> /skills reload
dbaa -c <conn> /skills list
```

Runtime notes:

- The shell and python examples use `gsql` or `psql` from `PATH`.
- DBAA does not pass database passwords to external scripts by default. Use local trust/peer auth, `.pgpass`, a customer secret manager, or configure a customer-approved environment allowlist if needed.
- The Go example can analyze a supplied `plan_text` without connecting to the database. It can also run `EXPLAIN` for `sql` when `gsql`/`psql` connectivity is available.

