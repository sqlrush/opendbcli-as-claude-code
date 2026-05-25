/*-------------------------------------------------------------------------
 *
 * repl_sqlcompat.go
 *	  REPL SQL compatibility shim — accepts pasted multi-line SQL
 *	  (possibly with comments and trailing semicolons) without the
 *	  user having to escape newlines manually.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_sqlcompat.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"strings"
)

// ── SQL*Plus Compatibility ────────────────────────────────────

// sqlplusAliases maps SQL*Plus command prefixes to OpenDB commands.
// Category 1: direct skill mapping.
var sqlplusAliases = []struct {
	prefix  string
	rewrite func(args string) string
}{
	// SQL*Plus → standard SQL translations (not skill conversions).
	// DESC/DESCRIBE are left as-is — MySQL handles them natively; Oracle will error
	// (but that's expected — Oracle users use /tableinfo instead).
	{"show parameter", func(args string) string {
		// Oracle doesn't support SHOW PARAMETER natively — translate to v$parameter query.
		// MySQL's SHOW VARIABLES LIKE is handled natively by the database.
		if args == "" {
			return "SELECT name, value FROM v$parameter ORDER BY name"
		}
		// Sanitize: only allow alphanumeric, underscore, percent for LIKE pattern.
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '%' {
				return r
			}
			return -1
		}, args)
		return "SELECT name, value FROM v$parameter WHERE name LIKE '%" + safe + "%' ORDER BY name"
	}},
	{"show sga", func(_ string) string {
		return "SELECT name, value FROM v$sga"
	}},
	{"show user", func(_ string) string {
		return "SELECT user FROM dual"
	}},
	{"show con_name", func(_ string) string {
		return "SELECT sys_context('userenv','con_name') FROM dual"
	}},
	{"show pdbs", func(_ string) string {
		return "SELECT con_id, name, open_mode FROM v$pdbs ORDER BY con_id"
	}},
	{"show recyclebin", func(_ string) string {
		return "SELECT object_name, original_name, type, droptime FROM recyclebin ORDER BY droptime DESC"
	}},
	{"show errors", func(args string) string {
		if args == "" {
			return "SELECT line, position, text FROM user_errors ORDER BY sequence"
		}
		// SHOW ERRORS <type> <name> → filter by object type and name.
		parts := strings.Fields(args)
		if len(parts) >= 2 {
			return fmt.Sprintf("SELECT line, position, text FROM user_errors WHERE type = '%s' AND name = '%s' ORDER BY sequence",
				strings.ToUpper(parts[0]), strings.ToUpper(parts[1]))
		}
		return fmt.Sprintf("SELECT line, position, text FROM user_errors WHERE name = '%s' ORDER BY sequence",
			strings.ToUpper(parts[0]))
	}},
	{"show con_id", func(_ string) string {
		return "SELECT sys_context('userenv','con_id') AS con_id FROM dual"
	}},
	{"show spparameter", func(args string) string {
		if args == "" {
			return "SELECT name, value, display_value FROM v$spparameter WHERE isspecified = 'TRUE' ORDER BY name"
		}
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '%' {
				return r
			}
			return -1
		}, args)
		return "SELECT name, value, display_value FROM v$spparameter WHERE name LIKE '%" + safe + "%' ORDER BY name"
	}},
	{"show release", func(_ string) string {
		return "SELECT banner FROM v$version WHERE ROWNUM = 1"
	}},
}

// rewriteSQLPlus translates SQL*Plus commands to standard SQL.
// Only applies to Oracle — MySQL/PG/OG handle SHOW/DESC natively.
// Returns the original input unchanged if no match or non-Oracle database.
func rewriteSQLPlus(input string, dbType string) string {
	if dbType != "" && dbType != "oracle" {
		return input
	}
	lower := strings.ToLower(input)

	// DESC/DESCRIBE → all_tab_columns query (Oracle doesn't support DESC as SQL).
	// Supports tables, views, and v$ views.
	if strings.HasPrefix(lower, "desc ") || strings.HasPrefix(lower, "describe ") {
		var args string
		if strings.HasPrefix(lower, "describe ") {
			args = strings.TrimSpace(input[len("describe "):])
		} else {
			args = strings.TrimSpace(input[len("desc "):])
		}
		args = strings.TrimSuffix(args, ";")
		if args != "" {
			return descToSQL(args)
		}
		return input
	}

	for _, alias := range sqlplusAliases {
		if strings.HasPrefix(lower, alias.prefix) {
			args := strings.TrimSpace(input[len(alias.prefix):])
			return alias.rewrite(args)
		}
		trimmed := strings.TrimSuffix(alias.prefix, " ")
		if lower == trimmed {
			return alias.rewrite("")
		}
	}
	return input
}

// descToSQL converts DESC <object> to a standard SQL query against Oracle dictionary.
// Handles schema.object, v$ views, and plain object names.
func descToSQL(object string) string {
	// Sanitize: strip quotes, semicolons.
	object = strings.Trim(object, "\"';")
	upper := strings.ToUpper(object)

	// v$ views → query all_tab_columns with owner SYS and v_$ prefix.
	if strings.HasPrefix(upper, "V$") {
		viewName := "V_$" + upper[2:]
		return fmt.Sprintf("SELECT column_name, data_type, data_length, nullable FROM all_tab_columns WHERE owner = 'SYS' AND table_name = '%s' ORDER BY column_id", viewName)
	}

	// schema.object → split.
	if idx := strings.Index(object, "."); idx > 0 {
		schema := strings.ToUpper(object[:idx])
		name := strings.ToUpper(object[idx+1:])
		return fmt.Sprintf("SELECT column_name, data_type, data_length, nullable FROM all_tab_columns WHERE owner = '%s' AND table_name = '%s' ORDER BY column_id", schema, name)
	}

	// Plain object → search in current user's objects first.
	return fmt.Sprintf("SELECT column_name, data_type, data_length, nullable FROM all_tab_columns WHERE table_name = '%s' ORDER BY column_id", upper)
}

