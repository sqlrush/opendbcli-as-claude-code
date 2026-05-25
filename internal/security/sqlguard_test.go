/*-------------------------------------------------------------------------
 *
 * sqlguard_test.go
 *	  Test cases for sqlguard.go (security package): TestClassifySQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/sqlguard_test.go
 *
 *-------------------------------------------------------------------------
 */
package security

import "testing"

func TestClassifySQL(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want Level
	}{
		// L0 ReadOnly — basic SELECT
		{"select all from users", "SELECT * FROM users", LevelReadOnly},
		{"select count", "select count(*) from orders", LevelReadOnly},
		{"select with leading spaces", "  SELECT 1 FROM dual", LevelReadOnly},
		{"show parameter", "SHOW PARAMETER sga_target", LevelReadOnly},
		{"describe table", "DESCRIBE users", LevelReadOnly},
		{"explain plan", "EXPLAIN PLAN FOR SELECT 1", LevelReadOnly},
		{"select from dual", "SELECT * FROM dual", LevelReadOnly},

		// L1 Operator — session and system management
		{"alter system kill session", "ALTER SYSTEM KILL SESSION '142,38'", LevelOperator},
		{"alter session set", "ALTER SESSION SET nls_date_format='YYYY-MM-DD'", LevelOperator},
		{"analyze table", "ANALYZE TABLE users COMPUTE STATISTICS", LevelOperator},
		{"create index operator", "CREATE INDEX idx_name ON users(name)", LevelOperator},

		// L2 Admin — DML and DDL
		{"alter table add column", "ALTER TABLE users ADD (email VARCHAR2(100))", LevelAdmin},
		{"create table", "CREATE TABLE test (id NUMBER)", LevelAdmin},
		{"grant select", "GRANT SELECT ON users TO app_user", LevelAdmin},
		{"revoke select", "REVOKE SELECT ON users FROM app_user", LevelAdmin},
		{"insert into", "INSERT INTO logs VALUES (1, 'test')", LevelAdmin},
		{"update set", "UPDATE users SET name = 'test' WHERE id = 1", LevelAdmin},

		// L3 Dangerous — destructive operations
		{"drop table", "DROP TABLE users", LevelDangerous},
		{"drop database", "DROP DATABASE", LevelDangerous},
		{"truncate table", "TRUNCATE TABLE orders", LevelDangerous},
		{"drop table lowercase", "drop table USERS", LevelDangerous},
		{"drop table excess whitespace", "  DROP   TABLE   users  ", LevelDangerous},
		{"drop index", "DROP INDEX idx_test", LevelDangerous},
		{"delete from", "DELETE FROM temp_table WHERE expired = 1", LevelDangerous},

		// Edge cases — comments hiding dangerous operations
		{"block comment then drop", "/* comment */ DROP TABLE users", LevelDangerous},
		{"line comment then drop", "-- comment\nDROP TABLE users", LevelDangerous},
		{"multi-line block comment then drop", "/* multi\nline */ DROP TABLE users", LevelDangerous},
		{"double block comment then truncate", "/* comment */ /* another */ TRUNCATE TABLE orders", LevelDangerous},

		// Edge cases — WITH CTE + DML
		{"with cte delete", "WITH cte AS (SELECT 1) DELETE FROM users", LevelDangerous},
		{"with cte update", "WITH cte AS (SELECT 1) UPDATE users SET x=1", LevelAdmin},
		{"with cte select", "WITH cte AS (SELECT 1) SELECT * FROM cte", LevelReadOnly},
		{"with cte complex delete", "WITH cte AS (SELECT id FROM old) DELETE FROM users WHERE id IN (SELECT id FROM cte)", LevelDangerous},

		// Edge cases — PL/SQL blocks
		{"plsql execute immediate drop", "BEGIN EXECUTE IMMEDIATE 'DROP TABLE users'; END;", LevelDangerous},
		{"plsql null block", "BEGIN NULL; END;", LevelDangerous},

		// Edge cases — MERGE
		{"merge into", "MERGE INTO target USING source ON (1=1) WHEN MATCHED THEN UPDATE SET x=1", LevelAdmin},
		{"merge into with join condition", "MERGE INTO target USING source ON (target.id = source.id) WHEN MATCHED THEN UPDATE SET val = source.val", LevelAdmin},

		// Edge cases — case insensitivity
		{"drop table mixed case", "Drop Table Users", LevelDangerous},

		// Edge cases — leading whitespace variations
		{"tab then drop", "  \t  DROP TABLE users", LevelDangerous},
		{"newlines then truncate", "\n\nTRUNCATE TABLE orders", LevelDangerous},
		{"spaces then select", "   SELECT 1 FROM dual", LevelReadOnly},

		// Edge cases — multiple statements (highest level wins)
		{"select then drop", "SELECT 1; DROP TABLE users", LevelDangerous},
		{"two selects", "SELECT 1; SELECT 2", LevelReadOnly},

		// Edge cases — conservative default for unknown/garbage
		{"unknown command", "UNKNOWN_COMMAND foo bar", LevelDangerous},
		{"random text", "some random text", LevelDangerous},
		{"empty string", "", LevelDangerous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifySQL(tt.sql)
			if got != tt.want {
				t.Errorf("ClassifySQL(%q) = %v, want %v", tt.sql, got, tt.want)
			}
		})
	}
}
