/*-------------------------------------------------------------------------
 *
 * classifier_test.go
 *	  Test cases for classifier.go (dispatch package):
 *	  TestClassifyInput.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dispatch/classifier_test.go
 *
 *-------------------------------------------------------------------------
 */
package dispatch

import "testing"

func TestClassifyInput(t *testing.T) {
	tests := []struct {
		input string
		want  InputType
	}{
		// ── Empty / Slash ──
		{"", InputEmpty},
		{"   ", InputEmpty},
		{"/help", InputSlashCommand},
		{"/slowsql 5000", InputSlashCommand},

		// ── Phase 1: Unambiguous SQL first-words ──
		{"SELECT * FROM users", InputSQL},
		{"select count(*) from orders", InputSQL},
		{"  SELECT 1", InputSQL},
		{"INSERT INTO t VALUES (1)", InputSQL},
		{"DELETE FROM t", InputSQL},
		{"ALTER TABLE t ADD col INT", InputSQL},
		{"CREATE TABLE t (id INT)", InputSQL},
		{"DROP TABLE t", InputSQL},
		{"TRUNCATE TABLE t", InputSQL},
		{"MERGE INTO t USING s ON (1=1) WHEN MATCHED THEN UPDATE SET x=1", InputSQL},
		{"DECLARE v INT; BEGIN NULL; END;", InputSQL},
		{"GRANT SELECT ON t TO u", InputSQL},
		{"REVOKE SELECT ON t FROM u", InputSQL},
		{"COMMIT", InputSQL},
		{"ROLLBACK", InputSQL},
		{"SAVEPOINT sp1", InputSQL},
		{"FLASHBACK TABLE t TO BEFORE DROP", InputSQL},
		{"PURGE RECYCLEBIN", InputSQL},
		{"VACUUM ANALYZE t", InputSQL},
		{"REINDEX TABLE t", InputSQL},
		{"REPLACE INTO t VALUES (1)", InputSQL},

		// ── Phase 2: Ambiguous words — SQL confirmed by second word ──
		{"SHOW TABLES", InputSQL},
		{"SHOW PROCESSLIST", InputSQL},
		{"SHOW PARAMETER sga", InputSQL},
		{"SHOW CREATE TABLE t", InputSQL},
		{"SHOW DATABASES", InputSQL},
		{"SHOW VARIABLES", InputSQL},
		{"SET ROLE dba", InputSQL},
		{"SET GLOBAL max_connections=100", InputSQL},
		{"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", InputSQL},
		{"CHECK TABLE t", InputSQL},
		{"LOCK TABLE t IN EXCLUSIVE MODE", InputSQL},
		{"UNLOCK TABLES", InputSQL},
		{"ANALYZE TABLE t", InputSQL},
		{"OPTIMIZE TABLE t", InputSQL},
		{"LOAD DATA INFILE '/tmp/data' INTO TABLE t", InputSQL},
		{"KILL QUERY 1234", InputSQL},
		{"RENAME TABLE old_t TO new_t", InputSQL},
		{"FLUSH PRIVILEGES", InputSQL},
		{"FLUSH TABLES", InputSQL},
		{"START TRANSACTION", InputSQL},
		{"STOP SLAVE", InputSQL},
		{"CHANGE MASTER TO master_host='h'", InputSQL},
		{"RESET MASTER", InputSQL},
		{"INSTALL PLUGIN x SONAME 'x.so'", InputSQL},
		{"CACHE INDEX t IN keycache", InputSQL},
		{"REPAIR TABLE t", InputSQL},
		{"EXPLAIN SELECT * FROM t", InputSQL},
		{"EXPLAIN ANALYZE SELECT 1", InputSQL},
		{"COMMENT ON TABLE t IS 'desc'", InputSQL},
		{"AUDIT SELECT ON t", InputSQL},
		{"REFRESH MATERIALIZED VIEW mv", InputSQL},
		{"IMPORT FOREIGN SCHEMA s FROM SERVER srv INTO public", InputSQL},
		{"SECURITY LABEL ON TABLE t IS 'x'", InputSQL},
		{"FETCH FIRST 10 ROWS ONLY", InputSQL},

		// ── Phase 2: Ambiguous words — special pattern matching ──
		{"SET @var=1", InputSQL},
		{"SET @@global.wait_timeout=300", InputSQL},
		{"CALL pkg.proc()", InputSQL},
		{"CALL my_procedure(1, 2)", InputSQL},
		{"EXEC dbms_stats.gather_table_stats('HR','EMP')", InputSQL},
		{"EXECUTE IMMEDIATE 'SELECT 1'", InputSQL},
		{"DO $$ BEGIN RAISE NOTICE 'hi'; END $$", InputSQL},
		{"DESCRIBE users", InputSQL},
		{"DESC employees", InputSQL},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", InputSQL},
		{"BEGIN", InputSQL},
		{"BEGIN TRANSACTION", InputSQL},
		{"BEGIN;", InputSQL},
		{"END", InputSQL},
		{"END TRANSACTION", InputSQL},
		{"ABORT", InputSQL},
		{"USE mydb", InputSQL},
		{"KILL 1234", InputSQL},
		{"LISTEN my_channel", InputSQL},
		{"NOTIFY my_channel", InputSQL},
		{"COPY t FROM '/path'", InputSQL},
		{"COPY t TO STDOUT", InputSQL},
		{"CLOSE my_cursor", InputSQL},
		{"PREPARE stmt FROM 'SELECT ?'", InputSQL},
		{"PREPARE plan AS SELECT 1", InputSQL},
		{"TABLE t", InputSQL},
		{"VALUES (1, 'a'), (2, 'b')", InputSQL},
		{"UPDATE t SET x=1", InputSQL},
		{"UPDATE employees SET salary=50000 WHERE id=1", InputSQL},

		// ── Phase 2: Ambiguous words — NL confirmed by article/pronoun ──
		{"show me the slow queries", InputNaturalLanguage},
		{"show the database health", InputNaturalLanguage},
		{"show how to optimize", InputNaturalLanguage},
		{"set up monitoring", InputNaturalLanguage},
		{"set the threshold to 100", InputNaturalLanguage},
		{"check the database health", InputNaturalLanguage},
		{"check if there are locks", InputNaturalLanguage},
		{"explain why CPU is high", InputNaturalLanguage},
		{"explain the query plan", InputNaturalLanguage},
		{"explain how this works", InputNaturalLanguage},
		{"describe the architecture", InputNaturalLanguage},
		{"describe how it works", InputNaturalLanguage},
		{"analyze the performance", InputNaturalLanguage},
		{"optimize the query", InputNaturalLanguage},
		{"lock is causing problems", InputNaturalLanguage},
		{"kill the hanging process", InputNaturalLanguage},
		{"load is too high", InputNaturalLanguage},
		{"start the backup", InputNaturalLanguage},
		{"stop the replication", InputNaturalLanguage},
		{"change the parameter", InputNaturalLanguage},
		{"reset the counter", InputNaturalLanguage},
		{"close the connection", InputNaturalLanguage},
		{"use the index", InputNaturalLanguage},
		{"begin the migration", InputNaturalLanguage},
		{"table is locked", InputNaturalLanguage},
		{"values are wrong", InputNaturalLanguage},
		{"fetch the data", InputNaturalLanguage},
		{"comment about this", InputNaturalLanguage},

		// ── Phase 3: Pattern scan ──
		{"FROM t WHERE id=1", InputSQL}, // fragment with WHERE clause — Phase 3 catches it; DB will error
		// Natural language that happens to contain SQL-ish words.
		{"what are the slow queries", InputNaturalLanguage},
		{"帮我看看慢查询", InputNaturalLanguage},
		{"check database health", InputNaturalLanguage},
		{"why is the database slow", InputNaturalLanguage},
		{"how to add an index", InputNaturalLanguage},
		{"help me fix this error", InputNaturalLanguage},
		{"数据库性能下降了", InputNaturalLanguage},
		{"CPU usage is 100%", InputNaturalLanguage},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ClassifyInput(tt.input)
			if got != tt.want {
				t.Errorf("ClassifyInput(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
