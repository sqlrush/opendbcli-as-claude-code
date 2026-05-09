/*-------------------------------------------------------------------------
 *
 * pgerr_kb.go
 *	  PostgreSQL error-code knowledge base — maps numeric error codes
 *	  to short human-readable summaries used in /alert and /llm
 *	  output so users don't have to grep the manual.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/query/pgerr_kb.go
 *
 *-------------------------------------------------------------------------
 */
package query

// pgErrKnowledgeBase maps SQLSTATE codes to knowledge base entries.
var pgErrKnowledgeBase = map[string]*PGErrEntry{
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
			"Check PostgreSQL logs for crash or OOM events",
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
			"Check PostgreSQL version-specific syntax differences",
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
			"Check column name spelling (PostgreSQL lowercases unquoted identifiers)",
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
		Cause:    "PostgreSQL backend cannot allocate memory (work_mem, shared_buffers, or OS limit)",
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
			"Check PostgreSQL logs for shutdown reason",
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
			"Check PostgreSQL logs and dmesg/journalctl for OOM or crash details",
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
		Cause:    "Unexpected internal error in PostgreSQL -- potential bug or data corruption",
		DiagCmds: []string{
			"/health    check overall server health",
			"/os        check system resources",
		},
		Fix: []string{
			"Check PostgreSQL logs for detailed error context and stack trace",
			"Search PostgreSQL bug tracker for known issues",
			"Upgrade to latest minor version (patch release) of your major version",
			"If data corruption suspected, run pg_amcheck or REINDEX",
		},
	},
}
