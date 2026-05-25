/*-------------------------------------------------------------------------
 *
 * failover_templates.go
 *	  failover_templates.go provides database-specific failover step
 *	  templates. Each template generates complete, executable SQL for
 *	  the USER to run manually. OpenDB never executes these — they go
 *	  into the report.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/failover_templates.go
 *
 *-------------------------------------------------------------------------
 */
// failover_templates.go provides database-specific failover step templates.
// Each template generates complete, executable SQL for the USER to run manually.
// OpenDB never executes these — they go into the report.
package overlord

// fillOracleFailover populates the report with Oracle Data Guard failover steps.
func fillOracleFailover(r *FailoverReport) {
	r.PreChecks = oraclePreChecks(r.PrimaryHost, r.StandbyHost)
	r.Steps = oracleSteps(r.PrimaryHost, r.StandbyHost)
	r.RollbackPlan = oracleRollback(r.StandbyHost, r.PrimaryHost)
	r.EstimatedDowntime = "5-15 minutes depending on redo apply lag and network latency"
}

func oraclePreChecks(primary, standby string) []string {
	return []string{
		"Confirm standby database (" + standby + ") is mounted and in PHYSICAL STANDBY role",
		"Confirm Data Guard broker is enabled (DGMGRL> SHOW CONFIGURATION)",
		"Confirm transport lag and apply lag are acceptable (V$DATAGUARD_STATS)",
		"Confirm no active long-running transactions on primary (" + primary + ")",
		"Confirm application connection strings use TNS alias or SCAN (not hardcoded IP)",
		"Notify application teams of planned failover window",
	}
}

func oracleSteps(primary, standby string) []FailoverStep {
	return []FailoverStep{
		{
			Order:       1,
			Description: "Check Data Guard sync status on primary (" + primary + ")",
			SQL:         "SELECT NAME, VALUE, DATUM_TIME\nFROM V$DATAGUARD_STATS\nWHERE NAME IN ('transport lag', 'apply lag');",
			Risk:        "low",
			Expected:    "Transport lag and apply lag should be near zero (00:00:00)",
			VerifySQL:   "SELECT DATABASE_ROLE, OPEN_MODE FROM V$DATABASE;",
			Notes:       "Read-only query. If lag is significant, wait for it to clear before proceeding.",
		},
		{
			Order:       2,
			Description: "Verify standby (" + standby + ") archive destinations",
			SQL:         "SELECT DEST_ID, STATUS, TYPE, APPLIED_SEQ#, ERROR\nFROM V$ARCHIVE_DEST_STATUS\nWHERE STATUS != 'INACTIVE';",
			Risk:        "low",
			Expected:    "All active destinations show STATUS=VALID, no errors",
			VerifySQL:   "SELECT THREAD#, MAX(SEQUENCE#) FROM V$ARCHIVED_LOG WHERE APPLIED='YES' GROUP BY THREAD#;",
			Notes:       "Read-only query. Ensure applied sequence matches primary.",
		},
		{
			Order:       3,
			Description: "Stop writes on primary (" + primary + ")",
			SQL:         "ALTER SYSTEM SET LOG_ARCHIVE_DEST_STATE_2 = DEFER;\nALTER DATABASE COMMIT TO SWITCHOVER TO STANDBY WITH SESSION SHUTDOWN;",
			Risk:        "critical",
			Expected:    "Primary transitions to STANDBY role. All sessions disconnected.",
			VerifySQL:   "SELECT DATABASE_ROLE, SWITCHOVER_STATUS FROM V$DATABASE;",
			Notes:       "CRITICAL: This will disconnect all sessions. Ensure application failover is configured.",
		},
		{
			Order:       4,
			Description: "Apply remaining redo on standby (" + standby + ")",
			SQL:         "ALTER DATABASE RECOVER MANAGED STANDBY DATABASE FINISH;",
			Risk:        "high",
			Expected:    "All remaining redo applied. Standby ready for activation.",
			VerifySQL:   "SELECT THREAD#, MAX(SEQUENCE#) FROM V$ARCHIVED_LOG WHERE APPLIED='YES' GROUP BY THREAD#;",
			Notes:       "Wait for this command to complete before proceeding.",
		},
		{
			Order:       5,
			Description: "Promote standby (" + standby + ") to primary",
			SQL:         "ALTER DATABASE COMMIT TO SWITCHOVER TO PRIMARY WITH SESSION SHUTDOWN;\nALTER DATABASE OPEN;",
			Risk:        "critical",
			Expected:    "Standby becomes new primary and opens for read-write.",
			VerifySQL:   "SELECT DATABASE_ROLE, OPEN_MODE FROM V$DATABASE;",
			Notes:       "Database should show DATABASE_ROLE=PRIMARY, OPEN_MODE=READ WRITE.",
		},
		{
			Order:       6,
			Description: "Verify new primary health",
			SQL:         "SELECT COUNT(*) AS ACTIVE_SESSIONS FROM V$SESSION WHERE STATUS = 'ACTIVE' AND TYPE = 'USER';\nSELECT TABLESPACE_NAME, STATUS FROM DBA_TABLESPACES;\nSELECT INSTANCE_NAME, STATUS, DATABASE_STATUS FROM V$INSTANCE;",
			Risk:        "low",
			Expected:    "Instance status OPEN, database status ACTIVE, tablespaces ONLINE",
			VerifySQL:   "SELECT NAME, VALUE FROM V$SYSSTAT WHERE NAME IN ('user commits', 'user rollbacks');",
			Notes:       "Run within 1 minute of promotion to confirm database is accepting transactions.",
		},
	}
}

func oracleRollback(newPrimary, oldPrimary string) string {
	return "If failover fails mid-way:\n" +
		"1. On the old primary (" + oldPrimary + "): `STARTUP MOUNT;` then `ALTER DATABASE RECOVER MANAGED STANDBY DATABASE DISCONNECT;`\n" +
		"2. On the new primary (" + newPrimary + "): `ALTER DATABASE COMMIT TO SWITCHOVER TO STANDBY;`\n" +
		"3. Reinstate the original Data Guard configuration via DGMGRL\n" +
		"4. If reinstatement fails, flashback the standby to a guaranteed restore point"
}

// fillMySQLFailover populates the report with MySQL replication failover steps.
func fillMySQLFailover(r *FailoverReport) {
	r.PreChecks = mysqlPreChecks(r.PrimaryHost, r.StandbyHost)
	r.Steps = mysqlSteps(r.PrimaryHost, r.StandbyHost)
	r.RollbackPlan = mysqlRollback(r.StandbyHost, r.PrimaryHost)
	r.EstimatedDowntime = "1-5 minutes depending on replication lag and binary log size"
}

func mysqlPreChecks(primary, standby string) []string {
	return []string{
		"Confirm replica (" + standby + ") is running (SHOW REPLICA STATUS shows Replica_IO_Running=Yes, Replica_SQL_Running=Yes)",
		"Confirm replication lag (Seconds_Behind_Source) is near zero",
		"Confirm GTID mode is enabled on both primary and replica",
		"Confirm no long-running transactions on primary (" + primary + ")",
		"Confirm application connection strings use virtual IP or proxy (not hardcoded primary IP)",
		"Notify application teams of planned failover window",
	}
}

func mysqlSteps(primary, standby string) []FailoverStep {
	return []FailoverStep{
		{
			Order:       1,
			Description: "Check replication status on replica (" + standby + ")",
			SQL:         "SHOW REPLICA STATUS\\G",
			Risk:        "low",
			Expected:    "Replica_IO_Running=Yes, Replica_SQL_Running=Yes, Seconds_Behind_Source=0",
			VerifySQL:   "SELECT @@server_uuid, @@gtid_executed;",
			Notes:       "Read-only. If Seconds_Behind_Source > 0, wait for it to reach 0.",
		},
		{
			Order:       2,
			Description: "Set primary (" + primary + ") to read-only",
			SQL:         "SET GLOBAL read_only = ON;\nSET GLOBAL super_read_only = ON;",
			Risk:        "high",
			Expected:    "Primary stops accepting writes. Existing transactions will complete.",
			VerifySQL:   "SELECT @@read_only, @@super_read_only;",
			Notes:       "This blocks new writes but allows existing transactions to finish.",
		},
		{
			Order:       3,
			Description: "Wait for replica to catch up",
			SQL:         "-- On replica, wait for GTID to match primary:\nSELECT WAIT_FOR_EXECUTED_GTID_SET('<primary_gtid_executed>', 30);",
			Risk:        "medium",
			Expected:    "Returns 0 (success). Replica is fully caught up.",
			VerifySQL:   "SHOW REPLICA STATUS\\G\n-- Verify: Seconds_Behind_Source = 0, Exec_Source_Log_Pos = Read_Source_Log_Pos",
			Notes:       "Replace <primary_gtid_executed> with the value from SELECT @@gtid_executed on primary.",
		},
		{
			Order:       4,
			Description: "Stop replication and promote replica (" + standby + ")",
			SQL:         "STOP REPLICA;\nRESET REPLICA ALL;\nSET GLOBAL read_only = OFF;\nSET GLOBAL super_read_only = OFF;",
			Risk:        "critical",
			Expected:    "Replica detaches from primary and becomes read-write.",
			VerifySQL:   "SELECT @@read_only, @@super_read_only;\nSHOW REPLICA STATUS;",
			Notes:       "SHOW REPLICA STATUS should return empty set after RESET REPLICA ALL.",
		},
		{
			Order:       5,
			Description: "Configure old primary as replica (optional)",
			SQL:         "-- On old primary (" + primary + "):\nCHANGE REPLICATION SOURCE TO\n  SOURCE_HOST='" + standby + "',\n  SOURCE_AUTO_POSITION=1;\nSTART REPLICA;",
			Risk:        "medium",
			Expected:    "Old primary begins replicating from new primary.",
			VerifySQL:   "SHOW REPLICA STATUS\\G",
			Notes:       "Optional step to set up reverse replication for future failback.",
		},
		{
			Order:       6,
			Description: "Verify new primary health",
			SQL:         "SELECT COUNT(*) AS ACTIVE_CONNECTIONS FROM information_schema.PROCESSLIST WHERE COMMAND != 'Sleep';\nSHOW GLOBAL STATUS LIKE 'Threads_connected';\nSELECT @@server_id, @@server_uuid;",
			Risk:        "low",
			Expected:    "New primary accepting connections and processing transactions",
			VerifySQL:   "SHOW GLOBAL STATUS LIKE 'Com_insert';\nSHOW GLOBAL STATUS LIKE 'Com_update';",
			Notes:       "Verify write operations are flowing to the new primary.",
		},
	}
}

func mysqlRollback(newPrimary, oldPrimary string) string {
	return "If failover fails mid-way:\n" +
		"1. On the old primary (" + oldPrimary + "): `SET GLOBAL read_only = OFF; SET GLOBAL super_read_only = OFF;`\n" +
		"2. On the replica (" + newPrimary + "): `START REPLICA;` to re-attach to original primary\n" +
		"3. Update application connection strings back to original primary if changed\n" +
		"4. Verify replication is healthy: `SHOW REPLICA STATUS\\G`"
}

// fillPostgreSQLFailover populates the report with PostgreSQL streaming replication failover steps.
func fillPostgreSQLFailover(r *FailoverReport) {
	r.PreChecks = pgPreChecks(r.PrimaryHost, r.StandbyHost)
	r.Steps = pgSteps(r.PrimaryHost, r.StandbyHost)
	r.RollbackPlan = pgRollback(r.StandbyHost, r.PrimaryHost)
	r.EstimatedDowntime = "2-10 minutes depending on WAL replay lag and checkpoint interval"
}

func pgPreChecks(primary, standby string) []string {
	return []string{
		"Confirm standby (" + standby + ") is in recovery mode (SELECT pg_is_in_recovery())",
		"Confirm WAL replay lag is acceptable (pg_stat_replication on primary)",
		"Confirm replication slots are active and not lagging",
		"Confirm no active long-running transactions on primary (" + primary + ")",
		"Confirm application uses connection pooler (PgBouncer/Pgpool) or virtual IP",
		"Notify application teams of planned failover window",
	}
}

func pgSteps(primary, standby string) []FailoverStep {
	return []FailoverStep{
		{
			Order:       1,
			Description: "Check replication status on primary (" + primary + ")",
			SQL:         "SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn,\n       (sent_lsn - replay_lsn) AS replay_lag_bytes,\n       sync_state\nFROM pg_stat_replication;",
			Risk:        "low",
			Expected:    "Standby shows state=streaming, replay_lag_bytes near 0",
			VerifySQL:   "SELECT pg_current_wal_lsn(), pg_last_wal_receive_lsn(), pg_is_in_recovery();",
			Notes:       "Read-only query. If replay lag is significant, wait for it to clear.",
		},
		{
			Order:       2,
			Description: "Create checkpoint and stop primary (" + primary + ")",
			SQL:         "CHECKPOINT;\n-- Then stop the primary:\n-- pg_ctl stop -D $PGDATA -m fast",
			Risk:        "critical",
			Expected:    "Primary flushes all WAL and shuts down cleanly.",
			VerifySQL:   "-- On standby, verify WAL received:\nSELECT pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn();",
			Notes:       "CRITICAL: Stopping primary will disconnect all clients. Use 'fast' mode to allow transactions to finish.",
		},
		{
			Order:       3,
			Description: "Wait for standby to replay remaining WAL",
			SQL:         "-- On standby (" + standby + "):\nSELECT pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn();\n-- Wait until receive_lsn = replay_lsn",
			Risk:        "low",
			Expected:    "receive_lsn equals replay_lsn — all WAL replayed",
			VerifySQL:   "SELECT pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() AS caught_up;",
			Notes:       "Poll every few seconds until caught_up = true.",
		},
		{
			Order:       4,
			Description: "Promote standby (" + standby + ") to primary",
			SQL:         "SELECT pg_promote(wait := true, wait_seconds := 60);",
			Risk:        "critical",
			Expected:    "Standby exits recovery and becomes read-write primary.",
			VerifySQL:   "SELECT pg_is_in_recovery();\n-- Should return false (not in recovery = primary)",
			Notes:       "pg_promote() available in PG 12+. For older versions use: pg_ctl promote -D $PGDATA",
		},
		{
			Order:       5,
			Description: "Configure old primary as standby (optional)",
			SQL:         "-- On old primary (" + primary + "), create standby.signal and configure:\n-- In postgresql.conf:\n--   primary_conninfo = 'host=" + standby + " port=5432 user=replicator'\n-- Then: touch $PGDATA/standby.signal\n-- Then: pg_ctl start -D $PGDATA",
			Risk:        "medium",
			Expected:    "Old primary starts as standby replicating from new primary.",
			VerifySQL:   "-- On new primary:\nSELECT client_addr, state FROM pg_stat_replication;",
			Notes:       "Optional. May require pg_rewind if timelines diverged.",
		},
		{
			Order:       6,
			Description: "Verify new primary health",
			SQL:         "SELECT pg_is_in_recovery() AS is_standby;\nSELECT count(*) AS active_backends FROM pg_stat_activity WHERE state = 'active';\nSELECT datname, numbackends FROM pg_stat_database WHERE datname NOT IN ('template0','template1');",
			Risk:        "low",
			Expected:    "is_standby=false, databases accepting connections",
			VerifySQL:   "SELECT xact_commit, xact_rollback FROM pg_stat_database WHERE datname = current_database();",
			Notes:       "Verify transactions are committing on the new primary.",
		},
	}
}

func pgRollback(newPrimary, oldPrimary string) string {
	return "If failover fails mid-way:\n" +
		"1. If primary (" + oldPrimary + ") was not stopped: it is still the primary, no action needed\n" +
		"2. If primary was stopped but standby not promoted: restart primary with `pg_ctl start -D $PGDATA`\n" +
		"3. If standby (" + newPrimary + ") was promoted but needs rollback: use `pg_rewind` to resync\n" +
		"4. Verify replication with: `SELECT * FROM pg_stat_replication;` on the primary"
}

// fillGenericFailover populates the report with generic failover steps.
func fillGenericFailover(r *FailoverReport, dbType string) {
	r.PreChecks = []string{
		"Confirm standby (" + r.StandbyHost + ") is replicating from primary (" + r.PrimaryHost + ")",
		"Confirm replication lag is acceptable",
		"Confirm application connection strings support failover",
		"Notify application teams of planned failover window",
	}
	r.Steps = []FailoverStep{
		{
			Order:       1,
			Description: "Check replication status",
			SQL:         "-- Check replication status using " + dbType + "-specific commands",
			Risk:        "low",
			Expected:    "Replication is healthy with minimal lag",
			VerifySQL:   "-- Verify replication health using " + dbType + "-specific views",
			Notes:       "Consult " + dbType + " documentation for specific replication monitoring queries.",
		},
		{
			Order:       2,
			Description: "Stop writes on primary (" + r.PrimaryHost + ")",
			SQL:         "-- Set primary to read-only or shut down using " + dbType + "-specific commands",
			Risk:        "critical",
			Expected:    "Primary stops accepting new writes",
			VerifySQL:   "-- Verify primary is read-only",
			Notes:       "CRITICAL: This will affect application availability.",
		},
		{
			Order:       3,
			Description: "Wait for standby to catch up",
			SQL:         "-- Verify replication lag is zero on standby (" + r.StandbyHost + ")",
			Risk:        "low",
			Expected:    "Standby has replayed all changes from primary",
			VerifySQL:   "-- Confirm standby and primary are in sync",
			Notes:       "Do not proceed until replication lag is zero.",
		},
		{
			Order:       4,
			Description: "Promote standby to primary",
			SQL:         "-- Promote " + r.StandbyHost + " using " + dbType + "-specific promotion command",
			Risk:        "critical",
			Expected:    "Standby becomes the new primary and accepts writes",
			VerifySQL:   "-- Verify new primary is accepting writes",
			Notes:       "Verify database role has changed to primary.",
		},
		{
			Order:       5,
			Description: "Update topology and verify",
			SQL:         "-- Update DNS/VIP/proxy to point to new primary\n-- Optionally configure old primary as standby",
			Risk:        "medium",
			Expected:    "Applications reconnect to new primary",
			VerifySQL:   "-- Verify application connections are on the new primary",
			Notes:       "Update connection routing before notifying application teams.",
		},
	}
	r.RollbackPlan = "If failover fails: restart the old primary (" + r.PrimaryHost + ") and reattach the standby. " +
		"Consult " + dbType + " documentation for database-specific rollback procedures."
	r.EstimatedDowntime = "Varies by database type and replication lag. Consult " + dbType + " documentation."
}
