/*-------------------------------------------------------------------------
 *
 * systemschema.go
 *	  System schema discovery for openGauss — enumerates the V$ /
 *	  DBA_ catalogs that the monitor skills query, used at startup
 *	  to detect schema-version differences across builds.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/systemschema.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

// System / internal OG schemas that pollute diagnostic views on empty
// or lightly-loaded instances. User-facing skills should filter these by
// default so the output focuses on business tables. An --include-system
// opt-in is left as future work.
//
// Rationale (from docs/validation/og-live-validation-report.md §关键问题 3):
//   - pg_catalog / information_schema — standard PG system catalogs
//   - snapshot                        — WDR snapshot tables (100% dead by design)
//   - dbe_perf / dbe_pldeveloper /
//     dbe_pldebugger                  — OG monitoring/dev extensions
//   - db4ai                           — OG AI feature schema
//
// Keep this set in one place so /bloat, /vacuum, /hotkey, /autovacuum,
// /indexhealth, /gather, /segments, /toasttable all agree.
const systemSchemaFilter = `schemaname NOT IN ('pg_catalog', 'information_schema', 'snapshot', 'dbe_perf', 'dbe_pldeveloper', 'dbe_pldebugger', 'db4ai', 'gs_logical_cluster', 'sqladvisor')`

// nonEmptyOr returns s if non-empty, otherwise fallback. Used by display
// paths to avoid showing bare "" when a DB column is NULL or the GUC is
// absent (e.g. OG lacks some PG 9.4+ config names).
func nonEmptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
