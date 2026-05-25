/*-------------------------------------------------------------------------
 *
 * queries.go
 *	  Reusable SQL query strings for the Oracle driver — kept in
 *	  one place so version-specific tweaks land here, not scattered
 *	  across skill files.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/driver/queries.go
 *
 *-------------------------------------------------------------------------
 */
package driver

// SQL constants for Oracle v$ views and data dictionary queries.
const (
	queryServerInfo = `SELECT i.version_full, i.instance_name, i.host_name,
		d.platform_name, i.startup_time
		FROM v$instance i, v$database d`

	querySlowSQL = `SELECT sql_id, elapsed_time/1000 AS elapsed_ms, executions, sql_text
		FROM v$sql
		WHERE elapsed_time/1000 > :threshold
		ORDER BY elapsed_time DESC
		FETCH FIRST :limit ROWS ONLY`

	queryActiveSessions = `SELECT sid, serial#, username, status, sql_id, event, wait_class,
		seconds_in_wait, machine, program
		FROM v$session
		WHERE type = 'USER' AND status = 'ACTIVE'
		ORDER BY seconds_in_wait DESC`

	queryTablespaceUsage = `SELECT tablespace_name,
		ROUND(used_space * block_size / 1048576, 2) AS used_mb,
		ROUND(tablespace_size * block_size / 1048576, 2) AS total_mb,
		ROUND(used_percent, 2) AS used_pct
		FROM dba_tablespace_usage_metrics
		JOIN dba_tablespaces USING (tablespace_name)
		ORDER BY used_percent DESC`

	queryBlockingSessions = `SELECT blocking_session, sid, serial#, username, sql_id, event,
		seconds_in_wait
		FROM v$session
		WHERE blocking_session IS NOT NULL
		ORDER BY seconds_in_wait DESC`

	queryDatabaseVersion = `SELECT banner_full FROM v$version WHERE ROWNUM = 1`

	queryPGAStat = `SELECT name, value FROM v$pgastat WHERE name IN (
		'total PGA allocated', 'total PGA inuse', 'maximum PGA allocated')`

	querySGAStat = `SELECT pool, SUM(bytes)/1048576 AS size_mb
		FROM v$sgastat
		GROUP BY pool
		ORDER BY size_mb DESC`
)
