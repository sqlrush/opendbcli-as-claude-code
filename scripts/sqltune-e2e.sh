#!/bin/bash
# sqltune-e2e.sh — run /sqltune end-to-end integration tests against
# real DB instances.
#
# Each dialect's tests are gated by an env var (HOST gate). Set the
# DSN env vars for the dialects you have access to; others skip cleanly.
#
# Usage:
#   ./scripts/sqltune-e2e.sh                # runs whatever DSNs are set in env
#   SQLTUNE_E2E_MYSQL_HOST=47.251.30.180 SQLTUNE_E2E_MYSQL_PASS=...  \
#     ./scripts/sqltune-e2e.sh
#
# Convention: set HOST to enable a dialect; other env vars use sane
# defaults matching the project's test server (47.251.30.180).
#
# Env vars (see internal/<dialect>/sqltuner/integration_test.go for the
# canonical list per dialect):
#
#   SQLTUNE_E2E_MYSQL_HOST     PORT(3306)  USER(root)     PASS  DB(sys)
#   SQLTUNE_E2E_POSTGRES_HOST  PORT(5432)  USER(postgres) PASS  DB(postgres)
#   SQLTUNE_E2E_OPENGAUSS_HOST PORT(15432) USER(gauss)    PASS  DB(postgres)
#   SQLTUNE_E2E_ORACLE_HOST    PORT(1521)  USER(SYSTEM)   PASS  SERVICE(ORCLPDB1)
#   SQLTUNE_E2E_GAUSSDB_HOST   PORT(8000)  USER(root)     PASS  DB(postgres) (no instance available yet)

set -euo pipefail

cd "$(dirname "$0")/.."

# Build tags must include all 5 supported dialects.
# DM intentionally excluded — see CLAUDE.md "M5b decision" notes.
TAGS="oracle mysql postgres opengauss gaussdb"

# Use macOS-compatible tag list (no full = no DM dependency).
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
  TAGS="full"
fi

echo "=== /sqltune E2E ==="
echo "Set HOST env vars to enable dialects (others skip cleanly):"
echo "  SQLTUNE_E2E_MYSQL_HOST     : ${SQLTUNE_E2E_MYSQL_HOST:-<unset, MySQL tests skip>}"
echo "  SQLTUNE_E2E_POSTGRES_HOST  : ${SQLTUNE_E2E_POSTGRES_HOST:-<unset, PG tests skip>}"
echo "  SQLTUNE_E2E_OPENGAUSS_HOST : ${SQLTUNE_E2E_OPENGAUSS_HOST:-<unset, OG tests skip>}"
echo "  SQLTUNE_E2E_ORACLE_HOST    : ${SQLTUNE_E2E_ORACLE_HOST:-<unset, Oracle tests skip>}"
echo "  SQLTUNE_E2E_GAUSSDB_HOST   : ${SQLTUNE_E2E_GAUSSDB_HOST:-<unset, GaussDB tests skip (no test instance)>}"
echo ""

PKGS=(
  ./internal/sqltune/
  ./internal/opengauss/sqltuner/
  ./internal/mysql/sqltuner/
  ./internal/postgres/sqltuner/
  ./internal/oracle/sqltuner/
  ./internal/gaussdb/sqltuner/
)

# -run Integration: filters to integration_test.go's TestIntegration_*
# -v: show per-scenario pass/skip
# -count=1: disable test result caching (force re-run against DB)
go test -tags "$TAGS" -count=1 -v -run Integration "${PKGS[@]}"

echo ""
echo "=== Done ==="
