/*-------------------------------------------------------------------------
 *
 * myerr_kb.go
 *	  MySQL error-code knowledge base — maps numeric error codes
 *	  to short human-readable summaries used in /alert and /llm
 *	  output so users don't have to grep the manual.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/query/myerr_kb.go
 *
 *-------------------------------------------------------------------------
 */
package query

var mysqlErrKB = map[int]*MErrEntry{
	// ── Connection / Authentication ──

	1040: {
		Code:     1040,
		Name:     "ER_CON_COUNT_ERROR",
		Severity: "HIGH",
		Cause:    "Too many connections — all connection slots are in use",
		DiagCmds: []string{
			"/sessions          List all connections",
			"/resource           Check resource usage",
			"/params max_conn    Show max_connections setting",
		},
		Fixes: []string{
			"SET GLOBAL max_connections = <higher_value>",
			"Kill idle or sleeping connections: /kill <id>",
			"Use connection pooling in the application layer",
			"Check for connection leaks in application code",
		},
	},
	1045: {
		Code:     1045,
		Name:     "ER_ACCESS_DENIED_ERROR",
		Severity: "MEDIUM",
		Cause:    "Access denied for user — wrong username, password, or host",
		DiagCmds: []string{
			"/users              List database users and privileges",
		},
		Fixes: []string{
			"Verify username and password are correct",
			"Check user host restrictions: SELECT user, host FROM mysql.user",
			"GRANT privileges if needed: GRANT ALL ON db.* TO 'user'@'host'",
			"ALTER USER 'user'@'host' IDENTIFIED BY 'new_password'",
		},
	},
	2002: {
		Code:     2002,
		Name:     "CR_CONNECTION_ERROR",
		Severity: "HIGH",
		Cause:    "Can't connect to MySQL server through socket — server not running or socket path wrong",
		DiagCmds: []string{
			"/health             Check server health",
		},
		Fixes: []string{
			"Verify MySQL server is running: systemctl status mysqld",
			"Check socket path in my.cnf: socket = /var/lib/mysql/mysql.sock",
			"Start MySQL: systemctl start mysqld",
		},
	},
	2003: {
		Code:     2003,
		Name:     "CR_CONN_HOST_ERROR",
		Severity: "HIGH",
		Cause:    "Can't connect to MySQL server via TCP — network issue or server not listening",
		DiagCmds: []string{
			"/health             Check server health",
		},
		Fixes: []string{
			"Verify MySQL is listening on the expected port: netstat -tlnp | grep 3306",
			"Check bind-address in my.cnf (use 0.0.0.0 for remote access)",
			"Check firewall rules allow the port",
			"Verify skip-networking is not enabled",
		},
	},
	2006: {
		Code:     2006,
		Name:     "CR_SERVER_GONE_ERROR",
		Severity: "HIGH",
		Cause:    "MySQL server has gone away — connection dropped, packet too large, or server crashed",
		DiagCmds: []string{
			"/health             Check server health",
			"/params max_allow    Show max_allowed_packet",
			"/resource           Check resource usage",
		},
		Fixes: []string{
			"Increase max_allowed_packet: SET GLOBAL max_allowed_packet = 64*1024*1024",
			"Increase wait_timeout for long-running connections",
			"Check MySQL error log for crash or OOM",
			"Implement connection retry logic in application",
		},
	},
	2013: {
		Code:     2013,
		Name:     "CR_SERVER_LOST",
		Severity: "HIGH",
		Cause:    "Lost connection to MySQL server during query — timeout, kill, or network issue",
		DiagCmds: []string{
			"/sessions           Check active sessions",
			"/params net_        Show network timeout parameters",
		},
		Fixes: []string{
			"Increase net_read_timeout and net_write_timeout",
			"Optimize long-running queries to reduce execution time",
			"Check network stability between client and server",
			"Increase max_allowed_packet if sending large data",
		},
	},

	// ── Lock / Concurrency ──

	1205: {
		Code:     1205,
		Name:     "ER_LOCK_WAIT_TIMEOUT",
		Severity: "HIGH",
		Cause:    "Lock wait timeout exceeded — another transaction holds the lock too long",
		DiagCmds: []string{
			"/locks              Show current locks",
			"/blocktree          Show blocking tree",
			"/innodb             Show InnoDB status",
			"/activesessions     Show active queries",
		},
		Fixes: []string{
			"Identify and kill the blocking transaction: /kill <id>",
			"Increase innodb_lock_wait_timeout for long transactions",
			"Optimize transactions to hold locks for shorter duration",
			"Add appropriate indexes to reduce lock scope",
		},
	},
	1213: {
		Code:     1213,
		Name:     "ER_LOCK_DEADLOCK",
		Severity: "HIGH",
		Cause:    "Deadlock found — two or more transactions waiting for each other's locks",
		DiagCmds: []string{
			"/deadlock           Show latest deadlock info",
			"/locks              Show current locks",
			"/innodb             Show InnoDB status",
		},
		Fixes: []string{
			"Review SHOW ENGINE INNODB STATUS for deadlock details",
			"Ensure transactions access tables in consistent order",
			"Keep transactions short and narrow",
			"Add retry logic in application for deadlock errors",
		},
	},

	// ── Data Integrity ──

	1062: {
		Code:     1062,
		Name:     "ER_DUP_ENTRY",
		Severity: "MEDIUM",
		Cause:    "Duplicate entry for key — unique/primary key constraint violation",
		DiagCmds: []string{
			"/explain <sql>      Analyze the failing query",
		},
		Fixes: []string{
			"Use INSERT ... ON DUPLICATE KEY UPDATE for upsert",
			"Use INSERT IGNORE to skip duplicates",
			"Check auto_increment value: SHOW TABLE STATUS",
			"Review application logic for duplicate submissions",
		},
	},
	1048: {
		Code:     1048,
		Name:     "ER_BAD_NULL_ERROR",
		Severity: "LOW",
		Cause:    "Column cannot be null — NOT NULL constraint violation",
		DiagCmds: []string{
			"/explain <sql>      Analyze the failing query",
		},
		Fixes: []string{
			"Provide a value for the NOT NULL column",
			"Set a DEFAULT value: ALTER TABLE t ALTER COLUMN c SET DEFAULT 'val'",
			"Allow NULL if appropriate: ALTER TABLE t MODIFY c type NULL",
		},
	},
	1054: {
		Code:     1054,
		Name:     "ER_BAD_FIELD_ERROR",
		Severity: "LOW",
		Cause:    "Unknown column in query — column name misspelled or does not exist",
		DiagCmds: []string{
			"/tableinfo <table>  Show table structure",
		},
		Fixes: []string{
			"Verify column name spelling",
			"Check table structure: DESCRIBE table_name",
			"Use qualified column names if joining multiple tables",
		},
	},
	1136: {
		Code:     1136,
		Name:     "ER_WRONG_VALUE_COUNT_ON_ROW",
		Severity: "LOW",
		Cause:    "Column count doesn't match value count — INSERT column/value mismatch",
		DiagCmds: []string{
			"/tableinfo <table>  Show table structure",
		},
		Fixes: []string{
			"Ensure INSERT statement has matching column and value counts",
			"Explicitly list columns: INSERT INTO t (col1, col2) VALUES (v1, v2)",
		},
	},
	1366: {
		Code:     1366,
		Name:     "ER_TRUNCATED_WRONG_VALUE_FOR_FIELD",
		Severity: "LOW",
		Cause:    "Incorrect value for column — data type or character set mismatch",
		DiagCmds: []string{
			"/tableinfo <table>  Show table structure",
		},
		Fixes: []string{
			"Verify the data type matches the column definition",
			"Check character set: SHOW CREATE TABLE table_name",
			"Set sql_mode to handle truncation: SET sql_mode = ''",
		},
	},
	1406: {
		Code:     1406,
		Name:     "ER_DATA_TOO_LONG",
		Severity: "LOW",
		Cause:    "Data too long for column — string exceeds column width",
		DiagCmds: []string{
			"/tableinfo <table>  Show table structure",
		},
		Fixes: []string{
			"Truncate data before insertion",
			"Increase column width: ALTER TABLE t MODIFY c VARCHAR(500)",
			"Check if strict mode is causing the error: SET sql_mode = ''",
		},
	},

	// ── Schema / Table ──

	1146: {
		Code:     1146,
		Name:     "ER_NO_SUCH_TABLE",
		Severity: "MEDIUM",
		Cause:    "Table doesn't exist — wrong table name or database context",
		DiagCmds: []string{
			"/tableinfo <table>  Show table info",
		},
		Fixes: []string{
			"Verify table name and database: SHOW TABLES",
			"Check if table was dropped or renamed",
			"Use fully qualified name: database.table",
		},
	},
	1049: {
		Code:     1049,
		Name:     "ER_BAD_DB_ERROR",
		Severity: "MEDIUM",
		Cause:    "Unknown database — database name misspelled or does not exist",
		DiagCmds: []string{},
		Fixes: []string{
			"Check available databases: SHOW DATABASES",
			"Create the database: CREATE DATABASE dbname",
			"Verify connection string database name",
		},
	},
	1064: {
		Code:     1064,
		Name:     "ER_PARSE_ERROR",
		Severity: "LOW",
		Cause:    "SQL syntax error — malformed SQL statement",
		DiagCmds: []string{
			"/explain <sql>      Analyze the query",
		},
		Fixes: []string{
			"Check SQL syntax near the error position indicated",
			"Verify reserved word usage (quote with backticks if needed)",
			"Check MySQL version compatibility for syntax features",
		},
	},
	1114: {
		Code:     1114,
		Name:     "ER_RECORD_FILE_FULL",
		Severity: "HIGH",
		Cause:    "Table is full — disk space exhausted or tablespace limit reached",
		DiagCmds: []string{
			"/space              Show tablespace usage",
			"/segments           Show large segments",
			"/os                 Show OS disk usage",
		},
		Fixes: []string{
			"Free disk space or add storage",
			"Enable innodb_file_per_table and OPTIMIZE TABLE",
			"Purge old data or archive to different storage",
			"Check innodb_data_file_path autoextend settings",
		},
	},

	// ── Foreign Key ──

	1451: {
		Code:     1451,
		Name:     "ER_ROW_IS_REFERENCED_2",
		Severity: "MEDIUM",
		Cause:    "Cannot delete or update a parent row — foreign key constraint prevents deletion",
		DiagCmds: []string{
			"/tableinfo <table>  Show table constraints",
		},
		Fixes: []string{
			"Delete or update child rows first",
			"Use ON DELETE CASCADE in foreign key definition",
			"SET FOREIGN_KEY_CHECKS = 0 (temporary, use with caution)",
		},
	},
	1452: {
		Code:     1452,
		Name:     "ER_NO_REFERENCED_ROW_2",
		Severity: "MEDIUM",
		Cause:    "Cannot add or update a child row — foreign key references missing parent",
		DiagCmds: []string{
			"/tableinfo <table>  Show table constraints",
		},
		Fixes: []string{
			"Insert the parent row first",
			"Verify the referenced value exists in the parent table",
			"SET FOREIGN_KEY_CHECKS = 0 (temporary, for data loading)",
		},
	},

	// ── Permissions ──

	1142: {
		Code:     1142,
		Name:     "ER_TABLEACCESS_DENIED_ERROR",
		Severity: "MEDIUM",
		Cause:    "Permission denied for SELECT/INSERT/UPDATE/DELETE on table",
		DiagCmds: []string{
			"/users              List database users and privileges",
		},
		Fixes: []string{
			"GRANT required privileges: GRANT SELECT ON db.table TO 'user'@'host'",
			"Check user grants: SHOW GRANTS FOR 'user'@'host'",
			"FLUSH PRIVILEGES after manual grant table changes",
		},
	},
	1227: {
		Code:     1227,
		Name:     "ER_SPECIFIC_ACCESS_DENIED_ERROR",
		Severity: "MEDIUM",
		Cause:    "Access denied — requires SUPER or specific privilege",
		DiagCmds: []string{
			"/users              List database users and privileges",
		},
		Fixes: []string{
			"GRANT SUPER ON *.* TO 'user'@'host' (or specific privilege needed)",
			"Use a user with sufficient privileges",
			"In MySQL 8.0+, use dynamic privileges instead of SUPER",
		},
	},
	1290: {
		Code:     1290,
		Name:     "ER_OPTION_PREVENTS_STATEMENT",
		Severity: "MEDIUM",
		Cause:    "Server running with --read-only — write operations blocked",
		DiagCmds: []string{
			"/params read_only    Show read_only setting",
			"/replication         Show replication status",
		},
		Fixes: []string{
			"SET GLOBAL read_only = OFF (if this is intended to be writable)",
			"Connect to the primary/master for write operations",
			"Use SET GLOBAL super_read_only = OFF for SUPER users",
		},
	},

	// ── Query Execution ──

	1317: {
		Code:     1317,
		Name:     "ER_QUERY_INTERRUPTED",
		Severity: "LOW",
		Cause:    "Query execution was interrupted — killed by admin or timeout",
		DiagCmds: []string{
			"/activesessions     Show active queries",
		},
		Fixes: []string{
			"Retry the query",
			"Increase max_execution_time if using query timeout",
			"Check if another session killed the query",
		},
	},

	// ── Replication ──

	1236: {
		Code:     1236,
		Name:     "ER_MASTER_FATAL_ERROR_READING_BINLOG",
		Severity: "HIGH",
		Cause:    "Binary log position error — replica cannot find the requested binlog position",
		DiagCmds: []string{
			"/replication         Show replication status",
			"/binlog              Show binary log info",
		},
		Fixes: []string{
			"SHOW MASTER STATUS on primary to get current position",
			"CHANGE MASTER TO with correct binlog file and position",
			"If using GTID: SET GLOBAL gtid_purged to skip missing transactions",
			"Rebuild replica from fresh backup if positions are unrecoverable",
		},
	},
	1032: {
		Code:     1032,
		Name:     "ER_KEY_NOT_FOUND",
		Severity: "HIGH",
		Cause:    "Can't find record in table — replication row not found on replica",
		DiagCmds: []string{
			"/replication         Show replication status",
		},
		Fixes: []string{
			"SET GLOBAL slave_skip_errors = 1032 (temporary)",
			"pt-table-sync to synchronize data between primary and replica",
			"STOP SLAVE; SET GLOBAL SQL_SLAVE_SKIP_COUNTER = 1; START SLAVE",
			"Rebuild replica from fresh backup for persistent issues",
		},
	},
	1594: {
		Code:     1594,
		Name:     "ER_SLAVE_RELAY_LOG_READ_FAILURE",
		Severity: "HIGH",
		Cause:    "Relay log read failure — corrupted or missing relay log file",
		DiagCmds: []string{
			"/replication         Show replication status",
		},
		Fixes: []string{
			"STOP SLAVE; RESET SLAVE; START SLAVE",
			"Check disk space and filesystem for errors",
			"Verify relay_log settings in my.cnf",
			"Rebuild replica if relay logs are unrecoverable",
		},
	},
	1872: {
		Code:     1872,
		Name:     "ER_SLAVE_RLI_INIT_REPOSITORY",
		Severity: "HIGH",
		Cause:    "Slave failed to initialize relay log info — GTID or relay log repository issue",
		DiagCmds: []string{
			"/replication         Show replication status",
		},
		Fixes: []string{
			"RESET SLAVE ALL and reconfigure replication",
			"Check relay_log_info_repository setting (TABLE recommended)",
			"Verify GTID configuration consistency between primary and replica",
			"Rebuild replica from backup with CHANGE MASTER TO ... MASTER_AUTO_POSITION = 1",
		},
	},

	// ── Partitioning ──

	1526: {
		Code:     1526,
		Name:     "ER_NO_PARTITION_FOR_GIVEN_VALUE",
		Severity: "MEDIUM",
		Cause:    "Table has no partition for the given value — partition range exceeded",
		DiagCmds: []string{
			"/tableinfo <table>  Show table partition info",
		},
		Fixes: []string{
			"ALTER TABLE t ADD PARTITION (PARTITION p_new VALUES LESS THAN (new_value))",
			"Add a MAXVALUE partition to catch all overflow",
			"Set up automatic partition management with events or scheduler",
		},
	},
}
