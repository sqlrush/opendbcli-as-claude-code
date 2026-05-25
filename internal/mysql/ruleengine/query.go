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
 *	  internal/mysql/ruleengine/query.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// QueryDef maps a QueryID to the skill command or raw SQL used to execute it.
type QueryDef struct {
	ID           QueryID
	Description  string
	SkillCommand string // preferred: /processlist, /sql "..."
	RawSQL       string // fallback raw SQL
}

// queryRegistry holds all known query definitions keyed by QueryID.
// MySQL has different predefined queries than Oracle — these will be populated
// as MySQL-specific diagnostic queries are developed.
var queryRegistry = map[QueryID]*QueryDef{}

// GetQueryDef returns the query definition for the given ID, or nil if not
// registered.
func GetQueryDef(qid QueryID) *QueryDef {
	return queryRegistry[qid]
}
