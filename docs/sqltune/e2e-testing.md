# /sqltune End-to-End Testing (M9)

End-to-end (E2E) integration tests verify the full /sqltune pipeline
(Phase A collection → Round 1 LLM → verify → render) works against real
DB instances. These complement the unit tests (which mock everything)
by catching real-world issues like driver-level type returns, dialect
SQL syntax variations, and permission edge cases.

## Test coverage matrix

| Dialect | Test instance | Test file | Coverage |
|---|---|---|---|
| openGauss | ✅ 47.251.30.180:15432 | `internal/opengauss/sqltuner/integration_test.go` | ExplainPlan + EXPLAIN PERFORMANCE + EquivVerifier DML reject |
| PostgreSQL | ✅ 47.251.30.180:5432 | `internal/postgres/sqltuner/integration_test.go` | Simple SELECT + $N placeholder + DML reject + EnableTrace returns Available:false + big SQL G7 |
| MySQL | ✅ 47.251.30.180:3306 | `internal/mysql/sqltuner/integration_test.go` | Simple SELECT + ? placeholder + DML reject + big SQL G7 |
| Oracle | ✅ 47.251.30.180:1521 | `internal/oracle/sqltuner/integration_test.go` | SELECT FROM dual + :1 placeholder + MERGE reject + 10053 EnableTrace |
| GaussDB | ❌ **no instance** | `internal/gaussdb/sqltuner/integration_test.go` | Decorator chain + PromptBuilder GS_PLAN_TRACE check only |

**GaussDB gap**: The project's test infrastructure does not include a
GaussDB Centralized instance. The decorator architecture
(`gaussdbPlanner` wraps `ogPlanner`; only Kind + Trace differ) means og
integration tests effectively exercise 7/9 of GaussDB's methods.
When a GaussDB instance becomes available, set `SQLTUNE_E2E_GAUSSDB_HOST`
and only the `openGaussDBOrSkip` helper needs wiring.

## Running

```bash
# Set HOST env vars for the dialects you have access to.
export SQLTUNE_E2E_MYSQL_HOST=47.251.30.180
export SQLTUNE_E2E_MYSQL_PASS='YourMySQLPass123!'
export SQLTUNE_E2E_POSTGRES_HOST=47.251.30.180
export SQLTUNE_E2E_POSTGRES_PASS='YourPgPass123!'
export SQLTUNE_E2E_OPENGAUSS_HOST=47.251.30.180
export SQLTUNE_E2E_OPENGAUSS_PASS='GaussPass123new'
export SQLTUNE_E2E_ORACLE_HOST=47.251.30.180
export SQLTUNE_E2E_ORACLE_PASS='OraclePass123'

./scripts/sqltune-e2e.sh
```

Dialects without HOST set skip cleanly (no error). Running with no
env vars set is safe — every test reports SKIP.

## Env var convention

Per dialect, 5 env vars (HOST is the gate; others have defaults):

| Var | Default | Purpose |
|---|---|---|
| `SQLTUNE_E2E_<DIALECT>_HOST` | (required) | Gate; tests skip if unset |
| `SQLTUNE_E2E_<DIALECT>_PORT` | dialect-default | Connection port |
| `SQLTUNE_E2E_<DIALECT>_USER` | dialect-default | DB user |
| `SQLTUNE_E2E_<DIALECT>_PASS` | (empty) | DB password |
| `SQLTUNE_E2E_<DIALECT>_DB` | dialect-default | Database/service |

`<DIALECT>` is one of: `MYSQL`, `POSTGRES`, `OPENGAUSS`, `ORACLE`,
`GAUSSDB`.

Oracle uses `SERVICE` instead of `DB` (defaults to `ORCLPDB1`).

## Mock-LLM mode

Integration tests use `nil` LLMCaller, which makes GenericTuner fall
back to raw Phase A output. This is intentional:
- Tests are deterministic and cost-free
- Round 1 LLM verification belongs in a separate suite with recorded
  responses (out of M9 scope — see TODO below)

The unit-test harness `internal/sqltune/scenarios_test.go` exercises
the LLM-on path with canned JSON replies via `cannedLLM`.

## CI integration (future)

For continuous regression detection, set the env vars in CI secrets
and run `./scripts/sqltune-e2e.sh` on each push. Tests are designed to
be safe (read-only queries, DML always wrapped or rejected) — running
against a shared test instance is OK.

## TODO

- **Recorded-LLM tests**: capture real LLM responses for canonical
  scenarios, replay in CI so Round 1 prompt/parsing logic is covered
  without spending tokens
- **GaussDB instance**: when available, wire `openGaussDBOrSkip` and
  add GS_PLAN_TRACE-specific tests
- **Cross-dialect regression**: run all 5 dialects on the same
  representative SQL and diff outputs for unexpected divergence

## Files

- `scripts/sqltune-e2e.sh` — convenience runner
- `internal/sqltune/scenarios_test.go` — neutral harness + canonical scenarios
- `internal/sqltune/integration_helpers_test.go` — shared helpers
- `internal/<dialect>/sqltuner/integration_test.go` — per-dialect tests
