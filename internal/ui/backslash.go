/*-------------------------------------------------------------------------
 *
 * backslash.go
 *	  psql-style backslash command parser — recognises `\d`, `\dt`,
 *	  `\?` and similar one-letter shortcuts so DBAs muscle-memoried
 *	  on psql don't have to relearn.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/backslash.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"strings"
)

// rewriteBackslashCmd translates psql/gsql backslash commands to standard SQL.
// Only applies to PostgreSQL, openGauss, and GaussDB databases.
// Returns the original input unchanged if no match or wrong database type.
func rewriteBackslashCmd(input string, dbType string) string {
	if dbType != "postgres" && dbType != "opengauss" && dbType != "gaussdb" {
		return input
	}

	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, `\`) {
		return input
	}

	lower := strings.ToLower(trimmed)

	// ── \d [object] — describe table or list all relations ──
	switch {
	case lower == `\d` || lower == `\d+`:
		return `SELECT schemaname AS schema, tablename AS name, 'table' AS type FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') UNION ALL SELECT schemaname, viewname, 'view' FROM pg_views WHERE schemaname NOT IN ('pg_catalog','information_schema') UNION ALL SELECT schemaname, sequencename, 'sequence' FROM pg_sequences WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schema, name`

	case strings.HasPrefix(lower, `\d+ `) || strings.HasPrefix(lower, `\d `):
		obj := strings.TrimSpace(trimmed[strings.Index(trimmed, " ")+1:])
		obj = strings.TrimSuffix(obj, ";")
		return descPG(obj)

	// ── \dt — list tables ──
	case lower == `\dt` || lower == `\dt+`:
		return `SELECT schemaname AS schema, tablename AS name, tableowner AS owner FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname, tablename`

	// ── \di — list indexes ──
	case lower == `\di` || lower == `\di+`:
		return `SELECT schemaname AS schema, indexname AS name, tablename AS table FROM pg_indexes WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname, indexname`

	// ── \dv — list views ──
	case lower == `\dv` || lower == `\dv+`:
		return `SELECT schemaname AS schema, viewname AS name, viewowner AS owner FROM pg_views WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname, viewname`

	// ── \du — list roles ──
	case lower == `\du` || lower == `\du+`:
		return `SELECT rolname AS role, CASE WHEN rolsuper THEN 'yes' ELSE 'no' END AS superuser, CASE WHEN rolcreatedb THEN 'yes' ELSE 'no' END AS createdb, CASE WHEN rolcreaterole THEN 'yes' ELSE 'no' END AS createrole FROM pg_roles ORDER BY rolname`

	// ── \dn — list schemas ──
	case lower == `\dn` || lower == `\dn+`:
		return `SELECT nspname AS schema, pg_catalog.pg_get_userbyid(nspowner) AS owner FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname != 'information_schema' ORDER BY nspname`

	// ── \df — list functions ──
	case lower == `\df` || lower == `\df+`:
		return `SELECT n.nspname AS schema, p.proname AS name, pg_catalog.pg_get_function_result(p.oid) AS result_type FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') ORDER BY n.nspname, p.proname`

	// ── \l — list databases ──
	case lower == `\l` || lower == `\l+`:
		return `SELECT datname AS database, pg_catalog.pg_get_userbyid(datdba) AS owner, pg_encoding_to_char(encoding) AS encoding FROM pg_database WHERE NOT datistemplate ORDER BY datname`

	// ── \conninfo — connection info ──
	case lower == `\conninfo`:
		return `SELECT current_database() AS database, current_user AS user, inet_server_addr() AS host, inet_server_port() AS port, version() AS version`

	// ── Unsupported psql commands → friendly hint ──
	case strings.HasPrefix(lower, `\c `) || lower == `\c`:
		return `\hint:切换数据库请使用 /login <连接名>`
	case strings.HasPrefix(lower, `\i `) || lower == `\i`:
		return `\hint:执行脚本请直接粘贴 SQL 内容`
	case lower == `\x` || lower == `\x on` || lower == `\x off` || lower == `\x auto`:
		return `\hint:竖排显示请在 SQL 末尾加 \G，如: SELECT * FROM t\G`
	case lower == `\timing` || lower == `\timing on` || lower == `\timing off`:
		return `\hint:OpenDB 自动显示查询耗时`
	case lower == `\q`:
		return `\hint:退出请使用 /exit`
	}

	return input
}

// descPG converts \d <object> to a column query for PostgreSQL/OpenGauss.
// Handles schema.table and plain table names.
func descPG(object string) string {
	object = strings.Trim(object, `"';`)

	if idx := strings.Index(object, "."); idx > 0 {
		schema := object[:idx]
		name := object[idx+1:]
		return fmt.Sprintf(`SELECT column_name, data_type, character_maximum_length AS max_len, is_nullable, column_default FROM information_schema.columns WHERE table_schema = '%s' AND table_name = '%s' ORDER BY ordinal_position`, schema, name)
	}

	return fmt.Sprintf(`SELECT column_name, data_type, character_maximum_length AS max_len, is_nullable, column_default FROM information_schema.columns WHERE table_name = '%s' ORDER BY ordinal_position`, object)
}
