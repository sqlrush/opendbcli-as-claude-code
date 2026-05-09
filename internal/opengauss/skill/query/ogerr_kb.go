/*-------------------------------------------------------------------------
 *
 * ogerr_kb.go
 *	  openGauss error-code knowledge base — maps numeric error codes
 *	  to short human-readable summaries used in /alert and /llm
 *	  output so users don't have to grep the manual.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/ogerr_kb.go
 *
 *-------------------------------------------------------------------------
 */
package query

// ogErrKnowledgeBase maps SQLSTATE codes to knowledge base entries.
var ogErrKnowledgeBase = map[string]*OGErrEntry{
	// ── Connection (Class 08) ──

	"08003": {
		Code:     "08003",
		Name:     "connection_does_not_exist",
		Severity: "MEDIUM",
		Cause:    "Client attempted to use a connection that has already been closed or never established",
		DiagCmds: []string{
			"/sessions    check current connections",
			"/health      overall connection health",
		},
		Fix: []string{
			"Check application connection pool settings (min/max idle, lifetime)",
			"Enable connection validation (testOnBorrow or similar)",
			"Investigate network stability between app and database",
		},
	},
	"08006": {
		Code:     "08006",
		Name:     "connection_failure",
		Severity: "HIGH",
		Cause:    "Server closed the connection unexpectedly, network interruption, or pg_hba.conf rejection",
		DiagCmds: []string{
			"/sessions    check active connections",
			"/health      connection and uptime overview",
		},
		Fix: []string{
			"Check pg_hba.conf for client authentication rules",
			"Verify network connectivity and firewall rules",
			"Check OpenGauss logs for crash or OOM events",
			"Increase tcp_keepalives_idle, tcp_keepalives_interval in postgresql.conf",
		},
	},

	// ── Integrity Constraint (Class 23) ──

	"23502": {
		Code:     "23502",
		Name:     "not_null_violation",
		Severity: "LOW",
		Cause:    "INSERT or UPDATE attempted to set a NOT NULL column to NULL",
		DiagCmds: []string{
			"/tableinfo <table>    check column constraints",
		},
		Fix: []string{
			"Provide a value for the NOT NULL column",
			"Set a DEFAULT value on the column if appropriate",
			"ALTER TABLE ... ALTER COLUMN ... DROP NOT NULL if constraint is wrong",
		},
	},
	"23503": {
		Code:     "23503",
		Name:     "foreign_key_violation",
		Severity: "LOW",
		Cause:    "INSERT or UPDATE violates a foreign key constraint -- referenced row does not exist, or DELETE removes a referenced parent row",
		DiagCmds: []string{
			"/tableinfo <table>    check foreign key constraints",
		},
		Fix: []string{
			"Ensure referenced parent row exists before INSERT",
			"Use ON DELETE CASCADE or ON DELETE SET NULL if appropriate",
			"Delete child rows before deleting parent rows",
		},
	},
	"23505": {
		Code:     "23505",
		Name:     "unique_violation",
		Severity: "LOW",
		Cause:    "INSERT or UPDATE violates a UNIQUE or PRIMARY KEY constraint",
		DiagCmds: []string{
			"/tableinfo <table>    check unique constraints and indexes",
			"/topsql               find queries causing violations",
		},
		Fix: []string{
			"Use INSERT ... ON CONFLICT DO UPDATE (upsert) for idempotent writes",
			"Check for duplicate data in application logic",
			"Verify sequence values if using serial/identity columns",
		},
	},

	// ── Transaction State (Class 25) ──

	"25006": {
		Code:     "25006",
		Name:     "read_only_sql_transaction",
		Severity: "LOW",
		Cause:    "Write operation attempted on a read-only transaction or standby server",
		DiagCmds: []string{
			"/health      check if server is primary or standby",
			"/params default_transaction_read_only",
		},
		Fix: []string{
			"Route write queries to the primary server, not the standby",
			"SET default_transaction_read_only = off (if set globally by mistake)",
			"Check connection pool routing for read/write splitting",
		},
	},
	"25P02": {
		Code:     "25P02",
		Name:     "in_failed_sql_transaction",
		Severity: "MEDIUM",
		Cause:    "A previous statement in the transaction failed and the transaction was not rolled back before issuing another command",
		DiagCmds: []string{
			"/sessions    check for idle in transaction sessions",
		},
		Fix: []string{
			"Issue ROLLBACK to abort the failed transaction, then retry",
			"Use SAVEPOINT for partial rollback within transactions",
			"Ensure application handles errors and rolls back properly",
		},
	},

	// ── Authentication (Class 28) ──

	"28P01": {
		Code:     "28P01",
		Name:     "invalid_password",
		Severity: "MEDIUM",
		Cause:    "Authentication failed due to incorrect password",
		DiagCmds: []string{
			"/users    list database users and roles",
		},
		Fix: []string{
			"Verify password in connection string or .pgpass file",
			"ALTER USER <name> PASSWORD 'new_password'",
			"Check pg_hba.conf authentication method (md5, scram-sha-256, trust)",
		},
	},

	// ── Invalid Catalog (Class 3D) ──

	"3D000": {
		Code:     "3D000",
		Name:     "invalid_catalog_name",
		Severity: "MEDIUM",
		Cause:    "Attempted to connect to a database that does not exist",
		DiagCmds: []string{
			"/health    check current database name",
		},
		Fix: []string{
			"Verify database name in connection string",
			"CREATE DATABASE <name> if the database should exist",
			"Check for typos in the database name",
		},
	},

	// ── Transaction Rollback (Class 40) ──

	"40001": {
		Code:     "40001",
		Name:     "serialization_failure",
		Severity: "MEDIUM",
		Cause:    "Transaction could not be serialized due to concurrent modifications (SERIALIZABLE isolation)",
		DiagCmds: []string{
			"/locks       check for conflicting locks",
			"/sessions    check concurrent transactions",
		},
		Fix: []string{
			"Retry the transaction (standard pattern for serializable isolation)",
			"Consider using REPEATABLE READ instead of SERIALIZABLE if strict serializability is not required",
			"Reduce transaction duration to minimize conflict window",
		},
	},
	"40P01": {
		Code:     "40P01",
		Name:     "deadlock_detected",
		Severity: "HIGH",
		Cause:    "Two or more transactions are waiting for each other to release locks",
		DiagCmds: []string{
			"/locks       show current lock waits",
			"/blocktree   show blocking chain",
			"/topsql      find the SQL involved",
		},
		Fix: []string{
			"Ensure consistent lock ordering across all transactions",
			"Reduce transaction scope and duration",
			"Use SELECT ... FOR UPDATE NOWAIT or SKIP LOCKED",
			"Check deadlock_timeout setting (default 1s)",
		},
	},
	"40P02": {
		Code:     "40P02",
		Name:     "cannot_serialize_snapshot",
		Severity: "MEDIUM",
		Cause:    "Logical replication slot cannot serialize a consistent snapshot",
		DiagCmds: []string{
			"/slots       check replication slots",
			"/replication check replication status",
		},
		Fix: []string{
			"Ensure replication slot is not lagging excessively",
			"Drop and recreate the logical replication slot",
			"Check max_replication_slots and wal_level settings",
		},
	},

	// ── Syntax Error (Class 42) ──

	"42601": {
		Code:     "42601",
		Name:     "syntax_error",
		Severity: "LOW",
		Cause:    "SQL statement contains a syntax error",
		DiagCmds: []string{
			"/explain <sql>    validate SQL syntax",
		},
		Fix: []string{
			"Review the SQL statement for typos or missing keywords",
			"Check OpenGauss version-specific syntax differences",
			"Use \\h <command> in psql for syntax help",
		},
	},
	"42703": {
		Code:     "42703",
		Name:     "undefined_column",
		Severity: "LOW",
		Cause:    "Referenced column does not exist in the table",
		DiagCmds: []string{
			"/tableinfo <table>    check column names",
		},
		Fix: []string{
			"Check column name spelling (OpenGauss lowercases unquoted identifiers)",
			"Use double quotes for case-sensitive column names",
			"Verify the column exists with \\d <table> or /tableinfo",
		},
	},
	"42P01": {
		Code:     "42P01",
		Name:     "undefined_table",
		Severity: "LOW",
		Cause:    "Referenced table or view does not exist",
		DiagCmds: []string{
			"/tableinfo <table>    check if table exists",
		},
		Fix: []string{
			"Check table name spelling and search_path",
			"Use schema-qualified name: schema.table",
			"CREATE TABLE if the table should exist",
		},
	},
	"42P07": {
		Code:     "42P07",
		Name:     "duplicate_table",
		Severity: "LOW",
		Cause:    "CREATE TABLE failed because a table with the same name already exists",
		DiagCmds: []string{
			"/tableinfo <table>    check existing table",
		},
		Fix: []string{
			"Use CREATE TABLE IF NOT EXISTS",
			"DROP TABLE <name> first if replacing is intended",
			"Use a different table name or schema",
		},
	},
	"42710": {
		Code:     "42710",
		Name:     "duplicate_object",
		Severity: "LOW",
		Cause:    "Object (index, constraint, type, etc.) with the same name already exists",
		DiagCmds: []string{
			"/tableinfo <table>    check existing objects",
		},
		Fix: []string{
			"Use IF NOT EXISTS clause where supported",
			"Drop the existing object first if replacing is intended",
			"Use a different name",
		},
	},

	// ── Insufficient Resources (Class 53) ──

	"53100": {
		Code:     "53100",
		Name:     "disk_full",
		Severity: "HIGH",
		Cause:    "Disk partition is full -- cannot write WAL, data files, or temp files",
		DiagCmds: []string{
			"/space       check tablespace usage",
			"/segments    find largest tables",
			"/os          check OS disk usage",
		},
		Fix: []string{
			"Free disk space immediately (remove old WAL, pg_log files, temp files)",
			"Add storage or extend the volume",
			"Move tablespace to a larger partition",
			"Enable log_rotation and set log file retention",
		},
	},
	"53200": {
		Code:     "53200",
		Name:     "out_of_memory",
		Severity: "HIGH",
		Cause:    "OpenGauss backend cannot allocate memory (work_mem, shared_buffers, or OS limit)",
		DiagCmds: []string{
			"/sharedbufs    check shared buffer usage",
			"/resource      check resource consumption",
			"/os            check OS memory usage",
		},
		Fix: []string{
			"Reduce work_mem if set too high per-session",
			"Reduce max_connections to lower total memory demand",
			"Increase OS memory or swap space",
			"Check for memory leaks in extensions",
		},
	},
	"53300": {
		Code:     "53300",
		Name:     "too_many_connections",
		Severity: "HIGH",
		Cause:    "Number of client connections exceeds max_connections",
		DiagCmds: []string{
			"/sessions    check current connections",
			"/health      connection utilization overview",
			"/params max_connections",
		},
		Fix: []string{
			"Use a connection pooler (PgBouncer, Pgpool-II)",
			"ALTER SYSTEM SET max_connections = <higher_value>; then restart",
			"Close idle connections in the application",
			"Check for connection leaks in application code",
		},
	},

	// ── Program Limit (Class 54) ──

	"54000": {
		Code:     "54000",
		Name:     "program_limit_exceeded",
		Severity: "MEDIUM",
		Cause:    "Implementation limit exceeded (e.g., too many columns, query too complex, string too long)",
		DiagCmds: []string{
			"/explain <sql>    analyze query complexity",
		},
		Fix: []string{
			"Simplify the query (fewer joins, fewer columns)",
			"Break complex queries into CTEs or temp tables",
			"Check max_locks_per_transaction if lock-related",
		},
	},

	// ── Object State (Class 55) ──

	"55000": {
		Code:     "55000",
		Name:     "object_not_in_prerequisite_state",
		Severity: "MEDIUM",
		Cause:    "Object is not in the required state for the operation (e.g., table being rewritten, extension not loaded)",
		DiagCmds: []string{
			"/extensions    check loaded extensions",
			"/sessions      check for concurrent DDL",
		},
		Fix: []string{
			"Wait for concurrent DDL operations to complete",
			"Ensure required extensions are installed: CREATE EXTENSION <name>",
			"Check pg_stat_progress_* views for ongoing operations",
		},
	},
	"55P03": {
		Code:     "55P03",
		Name:     "lock_not_available",
		Severity: "HIGH",
		Cause:    "Lock request failed due to NOWAIT option or lock_timeout exceeded",
		DiagCmds: []string{
			"/locks       show lock waits",
			"/blocktree   show blocking chain",
			"/params lock_timeout",
		},
		Fix: []string{
			"Retry the operation after the blocking transaction completes",
			"Increase lock_timeout if appropriate",
			"Terminate the blocking session if safe: /kill <pid>",
			"Use LOCK TABLE ... NOWAIT with retry logic",
		},
	},

	// ── Operator Intervention (Class 57) ──

	"57014": {
		Code:     "57014",
		Name:     "query_canceled",
		Severity: "MEDIUM",
		Cause:    "Query was canceled by user request (pg_cancel_backend) or statement_timeout",
		DiagCmds: []string{
			"/params statement_timeout",
			"/topsql    find long-running queries",
		},
		Fix: []string{
			"Increase statement_timeout if query is legitimate",
			"Optimize the slow query (check /explain output)",
			"If canceled intentionally, no action needed",
		},
	},
	"57P01": {
		Code:     "57P01",
		Name:     "admin_shutdown",
		Severity: "HIGH",
		Cause:    "Server is shutting down (pg_ctl stop, SIGTERM, or admin command)",
		DiagCmds: []string{
			"/health    check server status and uptime",
		},
		Fix: []string{
			"Wait for server restart if planned maintenance",
			"Check OpenGauss logs for shutdown reason",
			"Reconnect after server is available",
		},
	},
	"57P02": {
		Code:     "57P02",
		Name:     "crash_shutdown",
		Severity: "HIGH",
		Cause:    "Server crashed (SIGKILL, OOM killer, hardware failure)",
		DiagCmds: []string{
			"/health    check server status after restart",
			"/os        check system resources",
		},
		Fix: []string{
			"Check OpenGauss logs and dmesg/journalctl for OOM or crash details",
			"Increase shared_buffers and OS limits if OOM-killed",
			"Check disk health for hardware issues",
			"Server will auto-recover via WAL replay on restart",
		},
	},
	"57P03": {
		Code:     "57P03",
		Name:     "cannot_connect_now",
		Severity: "HIGH",
		Cause:    "Server is starting up, shutting down, or in crash recovery -- cannot accept connections yet",
		DiagCmds: []string{
			"/health    check server status",
		},
		Fix: []string{
			"Wait for server to complete startup/recovery",
			"Check pg_log for recovery progress",
			"For standby: ensure primary is available and streaming",
		},
	},

	// ── External Error (Class 58) ──

	"58030": {
		Code:     "58030",
		Name:     "io_error",
		Severity: "HIGH",
		Cause:    "I/O error reading or writing data files, WAL, or temporary files",
		DiagCmds: []string{
			"/os        check disk I/O and errors",
			"/space     check tablespace availability",
		},
		Fix: []string{
			"Check disk health: dmesg, smartctl, iostat",
			"Check filesystem for corruption: fsck (requires downtime)",
			"Replace failing disk and restore from backup if needed",
			"Check NFS/SAN connectivity if using network storage",
		},
	},
	"58P01": {
		Code:     "58P01",
		Name:     "undefined_file",
		Severity: "HIGH",
		Cause:    "Referenced file does not exist -- missing tablespace directory, WAL segment, or data file",
		DiagCmds: []string{
			"/space     check tablespace paths",
			"/wal       check WAL status",
		},
		Fix: []string{
			"Restore missing WAL files from archive or backup",
			"Recreate tablespace symlink if directory was moved",
			"If data files are missing, restore from backup",
			"Check pg_tblspc/ symlinks for validity",
		},
	},

	// ── Internal Error (Class XX) ──

	"XX000": {
		Code:     "XX000",
		Name:     "internal_error",
		Severity: "HIGH",
		Cause:    "Unexpected internal error in OpenGauss -- potential bug or data corruption",
		DiagCmds: []string{
			"/health    check overall server health",
			"/os        check system resources",
		},
		Fix: []string{
			"Check OpenGauss logs for detailed error context and stack trace",
			"Search OpenGauss bug tracker for known issues",
			"Upgrade to latest minor version (patch release) of your major version",
			"If data corruption suspected, run pg_amcheck or REINDEX",
		},
	},

	// ── Warning (Class 01) ──

	"01004": {
		Code:     "01004",
		Name:     "string_data_right_truncation",
		Severity: "LOW",
		Cause:    "String value truncated to fit the column width",
		DiagCmds: []string{"/tableinfo <table>  check column widths"},
		Fix:      []string{"ALTER TABLE ... ALTER COLUMN ... TYPE VARCHAR(n) with larger n", "Truncate source data client-side before insert"},
	},
	"01007": {
		Code:     "01007",
		Name:     "privilege_not_granted",
		Severity: "LOW",
		Cause:    "GRANT statement partially failed; some privileges could not be granted",
		DiagCmds: []string{"/sql SELECT * FROM pg_roles WHERE rolname = '<user>'"},
		Fix:      []string{"Verify grantor has the privilege being granted", "Check role dependencies"},
	},

	// ── No Data (Class 02) ──

	"02000": {
		Code:     "02000",
		Name:     "no_data",
		Severity: "LOW",
		Cause:    "Query returned no rows where one or more was expected",
		DiagCmds: []string{"/explain <sql>  verify predicates"},
		Fix:      []string{"Handle empty-result case in application logic", "Review WHERE predicates"},
	},

	// ── Connection (Class 08) — additional ──

	"08000": {
		Code:     "08000",
		Name:     "connection_exception",
		Severity: "MEDIUM",
		Cause:    "Generic connection-level exception",
		DiagCmds: []string{"/sessions", "/os"},
		Fix:      []string{"Check network connectivity", "Review connection pool configuration"},
	},
	"08001": {
		Code:     "08001",
		Name:     "sqlclient_unable_to_establish_sqlconnection",
		Severity: "MEDIUM",
		Cause:    "Client cannot open a connection to the server",
		DiagCmds: []string{"/sessions", "/params max_connections"},
		Fix:      []string{"Verify host/port/service name", "Check max_connections and current session count"},
	},
	"08004": {
		Code:     "08004",
		Name:     "sqlserver_rejected_establishment_of_sqlconnection",
		Severity: "MEDIUM",
		Cause:    "Server actively rejected the connection (pg_hba, role disabled, db missing)",
		DiagCmds: []string{"/users", "/alert"},
		Fix:      []string{"Check pg_hba.conf entries match client IP/user/db", "Verify role is not LOCKED or expired"},
	},
	"08P01": {
		Code:     "08P01",
		Name:     "protocol_violation",
		Severity: "MEDIUM",
		Cause:    "Wire protocol violation between client and server",
		DiagCmds: []string{"/alert"},
		Fix:      []string{"Upgrade client driver", "Disable pgbouncer or change pooling mode if in use"},
	},

	// ── Data Exception (Class 22) ──

	"22001": {
		Code:     "22001",
		Name:     "string_data_right_truncation",
		Severity: "MEDIUM",
		Cause:    "Attempted to INSERT/UPDATE string longer than column max",
		DiagCmds: []string{"/tableinfo <table>"},
		Fix:      []string{"ALTER TABLE ... ALTER COLUMN ... TYPE VARCHAR(n) with larger n", "Validate input length client-side"},
	},
	"22003": {
		Code:     "22003",
		Name:     "numeric_value_out_of_range",
		Severity: "MEDIUM",
		Cause:    "Numeric value exceeds column's declared range",
		DiagCmds: []string{"/tableinfo <table>"},
		Fix:      []string{"Widen numeric type (e.g. INT → BIGINT, NUMERIC(10,2) → NUMERIC(20,2))"},
	},
	"22012": {
		Code:     "22012",
		Name:     "division_by_zero",
		Severity: "MEDIUM",
		Cause:    "SQL expression divides by zero",
		DiagCmds: []string{},
		Fix:      []string{"Use NULLIF(divisor, 0) to return NULL instead of erroring"},
	},
	"22P02": {
		Code:     "22P02",
		Name:     "invalid_text_representation",
		Severity: "MEDIUM",
		Cause:    "Text could not be cast to target type (e.g. 'abc' to INTEGER)",
		DiagCmds: []string{"/explain <sql>"},
		Fix:      []string{"Validate/sanitize input before the cast", "Use a safer cast like regexp_match + conditional"},
	},

	// ── Integrity Constraint (Class 23) — additional ──

	"23001": {
		Code:     "23001",
		Name:     "restrict_violation",
		Severity: "MEDIUM",
		Cause:    "Action blocked by a RESTRICT foreign key",
		DiagCmds: []string{"/tableinfo <table>"},
		Fix:      []string{"Delete referencing rows first", "Redefine FK with ON DELETE CASCADE if semantically safe"},
	},
	"23514": {
		Code:     "23514",
		Name:     "check_violation",
		Severity: "MEDIUM",
		Cause:    "Value violates a CHECK constraint",
		DiagCmds: []string{"/tableinfo <table>"},
		Fix:      []string{"Fix the value to satisfy the constraint", "Loosen or drop CHECK if no longer correct"},
	},

	// ── Transaction State (Class 25) ──

	"25001": {
		Code:     "25001",
		Name:     "active_sql_transaction",
		Severity: "LOW",
		Cause:    "Statement cannot execute inside an active transaction (e.g. VACUUM, CREATE DATABASE)",
		DiagCmds: []string{},
		Fix:      []string{"COMMIT or ROLLBACK first, then run the statement outside any transaction"},
	},
	"25P01": {
		Code:     "25P01",
		Name:     "no_active_sql_transaction",
		Severity: "LOW",
		Cause:    "Statement requires an active transaction (e.g. SAVEPOINT)",
		DiagCmds: []string{},
		Fix:      []string{"BEGIN first, then issue the statement"},
	},

	// ── Auth (Class 28) — additional ──

	"28000": {
		Code:     "28000",
		Name:     "invalid_authorization_specification",
		Severity: "MEDIUM",
		Cause:    "Authentication failed (wrong role, database, or hba rule)",
		DiagCmds: []string{"/users", "/alert"},
		Fix:      []string{"Verify role/db/pg_hba match client", "Check expired or locked account"},
	},

	// ── Transaction Rollback (Class 40) — additional ──

	"40000": {
		Code:     "40000",
		Name:     "transaction_rollback",
		Severity: "MEDIUM",
		Cause:    "Transaction was rolled back (generic)",
		DiagCmds: []string{"/alert", "/longtx"},
		Fix:      []string{"Check upstream cause (deadlock/serialization); retry transaction"},
	},
	"40003": {
		Code:     "40003",
		Name:     "statement_completion_unknown",
		Severity: "MEDIUM",
		Cause:    "Connection lost mid-statement; outcome unknown",
		DiagCmds: []string{"/alert", "/replication"},
		Fix:      []string{"Check idempotency before retry; query actual row state"},
	},

	// ── Syntax / Access (Class 42) — additional ──

	"42501": {
		Code:     "42501",
		Name:     "insufficient_privilege",
		Severity: "MEDIUM",
		Cause:    "Current role lacks required privilege",
		DiagCmds: []string{"/users", "/sql SELECT * FROM information_schema.role_table_grants WHERE grantee='<user>'"},
		Fix:      []string{"GRANT missing privilege to role", "Switch to a role with sufficient privileges"},
	},
	"42704": {
		Code:     "42704",
		Name:     "undefined_object",
		Severity: "MEDIUM",
		Cause:    "Referenced object (schema, type, operator, etc.) does not exist",
		DiagCmds: []string{"/sql SELECT nspname FROM pg_namespace"},
		Fix:      []string{"Check spelling; qualify with schema", "CREATE the missing object"},
	},
	"42883": {
		Code:     "42883",
		Name:     "undefined_function",
		Severity: "MEDIUM",
		Cause:    "Function does not exist or argument types don't match",
		DiagCmds: []string{"/sql \\df <func_name>"},
		Fix:      []string{"Add explicit casts to argument types", "CREATE the function or install the extension"},
	},
	"42P18": {
		Code:     "42P18",
		Name:     "indeterminate_datatype",
		Severity: "LOW",
		Cause:    "Could not determine data type of expression (e.g. NULL without cast)",
		DiagCmds: []string{},
		Fix:      []string{"Add explicit ::type cast", "Provide literal of known type instead of NULL"},
	},

	// ── Insufficient Resources (Class 53) — additional ──

	"53400": {
		Code:     "53400",
		Name:     "configuration_limit_exceeded",
		Severity: "HIGH",
		Cause:    "Server-side configuration limit was hit (too many prepared xacts, etc.)",
		DiagCmds: []string{"/params max_prepared_transactions", "/longtx"},
		Fix:      []string{"Increase the configuration limit and restart if required", "Clean up stale prepared transactions"},
	},

	// ── Program Limit (Class 54) — additional ──

	"54001": {
		Code:     "54001",
		Name:     "statement_too_complex",
		Severity: "MEDIUM",
		Cause:    "SQL statement too complex for planner/executor",
		DiagCmds: []string{"/explain <sql>"},
		Fix:      []string{"Split large IN lists, break query into CTEs", "Refactor complex UNION trees"},
	},
	"54011": {
		Code:     "54011",
		Name:     "too_many_columns",
		Severity: "MEDIUM",
		Cause:    "Table has too many columns (hard limit ~1600)",
		DiagCmds: []string{"/tableinfo <table>"},
		Fix:      []string{"Normalize the schema; move rarely-used columns to a side table"},
	},

	// ── Object Not In Prerequisite State (Class 55) — additional ──

	"55006": {
		Code:     "55006",
		Name:     "object_in_use",
		Severity: "MEDIUM",
		Cause:    "Operation blocked because the object is currently in use (e.g. DROP TABLE with active sessions)",
		DiagCmds: []string{"/blocktree", "/sessions"},
		Fix:      []string{"Identify holders and wait, or terminate with /kill after confirmation"},
	},

	// ── Operator Intervention / System (Class 57–58) — additional ──

	"57000": {
		Code:     "57000",
		Name:     "operator_intervention",
		Severity: "MEDIUM",
		Cause:    "DBA or recovery process intervention",
		DiagCmds: []string{"/alert"},
		Fix:      []string{"Review DBA actions / recovery events in the server log"},
	},

	// ── Snapshot Too Old (Class 72) — OG/PG MVCC ──

	"72000": {
		Code:     "72000",
		Name:     "snapshot_too_old",
		Severity: "HIGH",
		Cause:    "Old snapshot invalidated by VACUUM / autovacuum cleaning rows the long-running transaction still needed",
		DiagCmds: []string{"/longtx", "/vacuum", "/xid"},
		Fix:      []string{"Shorten long-running transactions", "Increase old_snapshot_threshold if deliberately long txns required", "Avoid mixing long reporting reads with aggressive autovacuum"},
	},

	// ── Foreign Data Wrapper (Class HV) ──

	"HV00R": {
		Code:     "HV00R",
		Name:     "fdw_table_not_found",
		Severity: "MEDIUM",
		Cause:    "Foreign table references a base table that doesn't exist on the remote server",
		DiagCmds: []string{"/extensions", "/sql SELECT * FROM pg_foreign_table"},
		Fix:      []string{"Verify remote table exists and user has access", "Recreate foreign table with correct OPTIONS"},
	},

	// ── PL/pgSQL (Class P0) ──

	"P0001": {
		Code:     "P0001",
		Name:     "raise_exception",
		Severity: "MEDIUM",
		Cause:    "Explicit RAISE EXCEPTION from a PL/pgSQL function",
		DiagCmds: []string{"/alert"},
		Fix:      []string{"Investigate business logic that raised the exception", "Add EXCEPTION handlers if appropriate"},
	},
	"P0002": {
		Code:     "P0002",
		Name:     "no_data_found",
		Severity: "LOW",
		Cause:    "SELECT INTO found no rows in PL/pgSQL",
		DiagCmds: []string{},
		Fix:      []string{"Use IF NOT FOUND checks or FOR loop over rows", "Widen WHERE predicate"},
	},
	"P0003": {
		Code:     "P0003",
		Name:     "too_many_rows",
		Severity: "MEDIUM",
		Cause:    "SELECT INTO found more than one row where only one expected",
		DiagCmds: []string{},
		Fix:      []string{"Use LIMIT 1 or aggregate function", "Rewrite to FOR loop if multiple rows expected"},
	},
}
