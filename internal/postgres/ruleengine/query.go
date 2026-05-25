/*-------------------------------------------------------------------------
 *
 * query.go
 *	  QueryDef maps a QueryID to the skill command or raw SQL used to
 *	  execute it.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/query.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// QueryDef maps a QueryID to the skill command or raw SQL used to execute it.
type QueryDef struct {
	ID           QueryID
	Description  string
	SkillCommand string
	RawSQL       string
}

// queryRegistry holds predefined query definitions for PostgreSQL.
// Currently empty — PG rules rely on JSON diagnostic_queries instead of
// hardcoded predefined queries.
var queryRegistry = map[QueryID]*QueryDef{}

// GetQueryDef returns the query definition for the given ID, or nil if not registered.
func GetQueryDef(qid QueryID) *QueryDef {
	return queryRegistry[qid]
}
