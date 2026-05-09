/*-------------------------------------------------------------------------
 *
 * json_query_registry.go
 *	  DynamicQueryRegistry stores SQL strings keyed by QueryID.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/json_query_registry.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// ─── Dynamic Query Registry ─────────────────────────────────────────────────
//
// Extends the predefined QueryID system to support JSON rule diagnostic_queries.
// Dynamic queries are registered with IDs in the format "json:{rule_id}:{query_key}".

// DynamicQueryRegistry stores SQL strings keyed by QueryID.
type DynamicQueryRegistry struct {
	queries map[QueryID]string // QueryID → raw SQL
}

// NewDynamicQueryRegistry creates an empty registry.
func NewDynamicQueryRegistry() *DynamicQueryRegistry {
	return &DynamicQueryRegistry{
		queries: make(map[QueryID]string),
	}
}

// Register adds a query to the registry.
func (r *DynamicQueryRegistry) Register(qid QueryID, sql string) {
	r.queries[qid] = sql
}

// RegisterRuleQueries registers all diagnostic_queries from a JSON rule.
func (r *DynamicQueryRegistry) RegisterRuleQueries(ruleID string, queries map[string]JSONDiagQuery) map[string]QueryID {
	result := make(map[string]QueryID, len(queries))
	for key, q := range queries {
		qid := MakeDynamicQueryID(ruleID, key)
		r.queries[qid] = q.SQL
		result[key] = qid
	}
	return result
}

// Lookup returns the SQL for a dynamic QueryID.
func (r *DynamicQueryRegistry) Lookup(qid QueryID) (string, bool) {
	sql, ok := r.queries[qid]
	return sql, ok
}

// MakeDynamicQueryID creates a QueryID from a rule ID and query key.
func MakeDynamicQueryID(ruleID, queryKey string) QueryID {
	return QueryID(fmt.Sprintf("json:%s:%s", ruleID, queryKey))
}

// IsDynamicQueryID returns true if the QueryID is a dynamic JSON query.
func IsDynamicQueryID(qid QueryID) bool {
	return strings.HasPrefix(string(qid), "json:")
}

// ─── Dynamic Query Executor ────────────────────────────────────────────────

// DynamicQueryExecutor bridges predefined QueryDefs and dynamic JSON queries.
type DynamicQueryExecutor struct {
	driver   db.Driver
	registry *DynamicQueryRegistry
	timeout  time.Duration
}

// NewDynamicQueryExecutor creates an executor that can run both predefined and dynamic queries.
func NewDynamicQueryExecutor(driver db.Driver, registry *DynamicQueryRegistry, timeoutSec int) *DynamicQueryExecutor {
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	return &DynamicQueryExecutor{
		driver:   driver,
		registry: registry,
		timeout:  time.Duration(timeoutSec) * time.Second,
	}
}

// Execute runs a query by its ID.
func (e *DynamicQueryExecutor) Execute(qid QueryID, params map[string]string) (interface{}, error) {
	if e.driver == nil {
		return nil, nil
	}

	var sql string

	if IsDynamicQueryID(qid) {
		var ok bool
		sql, ok = e.registry.Lookup(qid)
		if !ok {
			return nil, fmt.Errorf("unknown dynamic query: %s", qid)
		}
	} else {
		qdef := GetQueryDef(qid)
		if qdef == nil {
			return nil, fmt.Errorf("unknown predefined query: %s", qid)
		}
		sql = qdef.RawSQL
	}

	for k, v := range params {
		sql = strings.ReplaceAll(sql, "{"+k+"}", v)
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	result, err := e.driver.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", qid, err)
	}

	return queryResultToMap(result), nil
}

// queryResultToMap converts a db.QueryResult to a map-based structure.
func queryResultToMap(qr *db.QueryResult) interface{} {
	if qr == nil || len(qr.Rows) == 0 {
		return nil
	}

	if len(qr.Rows) == 1 {
		row := make(map[string]interface{}, len(qr.Columns))
		for i, col := range qr.Columns {
			if i < len(qr.Rows[0]) {
				row[strings.ToLower(col)] = qr.Rows[0][i]
			}
		}
		return row
	}

	rows := make([]map[string]interface{}, 0, len(qr.Rows))
	for _, r := range qr.Rows {
		row := make(map[string]interface{}, len(qr.Columns))
		for i, col := range qr.Columns {
			if i < len(r) {
				row[strings.ToLower(col)] = r[i]
			}
		}
		rows = append(rows, row)
	}

	result := map[string]interface{}{
		"rows":  rows,
		"count": float64(len(rows)),
	}
	return result
}

// ─── QueryRegistryProvider interface ───────────────────────────────────────

// QueryRegistryProvider is implemented by providers that have dynamic queries.
type QueryRegistryProvider interface {
	QueryRegistry() *DynamicQueryRegistry
}
