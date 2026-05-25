/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  Collect gathers all data needed for SQL analysis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqladvisor/collector.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Collect gathers all data needed for SQL analysis.
func Collect(ctx context.Context, driver db.Driver, queryID string) (*sadv.SQLReport, error) {
	report := &sadv.SQLReport{
		SQLID:      queryID,
		TableStats: make(map[string]*sadv.TableStat),
		IndexMap:   make(map[string][]sadv.IndexInfo),
		DataDepth:  sadv.DataDepthBasic,
	}

	if err := collectSQLInfo(ctx, driver, queryID, report); err != nil {
		return nil, fmt.Errorf("collect sql info: %w", err)
	}

	if err := collectPlanTree(ctx, driver, report); err != nil {
		report.UpgradeHint = "无法获取执行计划，建议手动 EXPLAIN"
	}

	tables := ExtractTables(report.PlanTree)
	for _, table := range tables {
		collectTableStats(ctx, driver, table, report)
		collectIndexInfo(ctx, driver, table, report)
	}

	return report, nil
}

func collectSQLInfo(ctx context.Context, driver db.Driver, queryID string, report *sadv.SQLReport) error {
	qr, err := driver.Query(ctx, `SELECT
		LEFT(query, 500),
		calls,
		mean_exec_time,
		shared_blks_hit + shared_blks_read,
		shared_blks_read,
		rows,
		temp_blks_written,
		calls
		FROM pg_stat_statements
		WHERE queryid::text = $1`, queryID)
	if err != nil {
		return fmt.Errorf("query pg_stat_statements: %w", err)
	}
	if len(qr.Rows) == 0 {
		return fmt.Errorf("queryid %s not found in pg_stat_statements", queryID)
	}

	row := qr.Rows[0]
	report.SQLText = toString(row[0])
	report.ExecCount = toInt64(row[1])
	report.AvgElapsedSec = toFloat64(row[2]) / 1000 // ms to sec
	report.AvgBufferGets = toInt64(row[3])
	report.AvgDiskReads = toInt64(row[4])

	calls := toInt64(row[7])
	if calls > 0 {
		report.AvgRowsProc = toInt64(row[5]) / calls
	}

	return nil
}

func collectPlanTree(ctx context.Context, driver db.Driver, report *sadv.SQLReport) error {
	sqlText := report.SQLText
	if sqlText == "" {
		return fmt.Errorf("no SQL text available for EXPLAIN")
	}

	sqlText = strings.TrimRight(sqlText, "; ")
	if strings.HasSuffix(sqlText, "...") {
		return fmt.Errorf("SQL text truncated, cannot EXPLAIN")
	}

	upper := strings.ToUpper(strings.TrimSpace(sqlText))
	if !strings.HasPrefix(upper, "SELECT") &&
		!strings.HasPrefix(upper, "INSERT") &&
		!strings.HasPrefix(upper, "UPDATE") &&
		!strings.HasPrefix(upper, "DELETE") &&
		!strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("cannot EXPLAIN statement type")
	}

	qr, err := driver.Query(ctx, "EXPLAIN (FORMAT JSON) "+sqlText)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	if len(qr.Rows) == 0 || len(qr.Rows[0]) == 0 {
		return fmt.Errorf("empty EXPLAIN output")
	}

	// PG EXPLAIN (FORMAT JSON) returns one row per plan line; concatenate all
	var jsonParts []string
	for _, row := range qr.Rows {
		if len(row) > 0 {
			jsonParts = append(jsonParts, toString(row[0]))
		}
	}
	jsonData := []byte(strings.Join(jsonParts, ""))

	planTree, err := ParsePGExplainJSON(jsonData)
	if err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	report.PlanTree = planTree
	return nil
}

func collectTableStats(ctx context.Context, driver db.Driver, table string, report *sadv.SQLReport) {
	qr, err := driver.Query(ctx, `SELECT
		schemaname,
		relname,
		COALESCE(n_live_tup, 0),
		COALESCE(n_dead_tup, 0),
		COALESCE(last_analyze::text, last_autoanalyze::text, ''),
		pg_relation_size(schemaname || '.' || relname)
		FROM pg_stat_user_tables
		WHERE relname = $1`, table)
	if err != nil || len(qr.Rows) == 0 {
		return
	}

	row := qr.Rows[0]
	sizeBytes := toInt64(row[5])
	stat := &sadv.TableStat{
		Owner:     toString(row[0]),
		TableName: toString(row[1]),
		NumRows:   toInt64(row[2]),
		Blocks:    sizeBytes / 8192, // PG 8KB pages
		SizeMB:    float64(sizeBytes) / 1048576,
	}

	lastAnalyzed := toString(row[4])
	if lastAnalyzed != "" {
		stat.LastAnalyzed = lastAnalyzed[:min(10, len(lastAnalyzed))] // YYYY-MM-DD
	}

	deadTuples := toInt64(row[3])
	if stat.NumRows > 0 && float64(deadTuples)/float64(stat.NumRows) > 0.2 {
		stat.StaleStats = true
	}

	report.TableStats[stat.TableName] = stat
}

func collectIndexInfo(ctx context.Context, driver db.Driver, table string, report *sadv.SQLReport) {
	qr, err := driver.Query(ctx, `SELECT
		i.indexname,
		i.indexdef,
		COALESCE(s.idx_scan, 0)
		FROM pg_indexes i
		LEFT JOIN pg_stat_user_indexes s
		  ON s.indexrelname = i.indexname AND s.schemaname = i.schemaname
		WHERE i.tablename = $1
		ORDER BY i.indexname`, table)
	if err != nil || len(qr.Rows) == 0 {
		return
	}

	for _, row := range qr.Rows {
		indexName := toString(row[0])
		indexDef := toString(row[1])
		idxScan := toInt64(row[2])

		columns := extractColumnsFromDef(indexDef)
		uniqueness := "NONUNIQUE"
		if strings.Contains(strings.ToUpper(indexDef), "UNIQUE") {
			uniqueness = "UNIQUE"
		}

		for i, col := range columns {
			idx := sadv.IndexInfo{
				IndexName:      indexName,
				Uniqueness:     uniqueness,
				ColumnName:     col,
				ColumnPosition: i + 1,
				DistinctKeys:   idxScan, // use scan count as proxy
				Status:         "VALID",
			}
			report.IndexMap[table] = append(report.IndexMap[table], idx)
		}
	}
}

// extractColumnsFromDef parses column names from a CREATE INDEX definition.
// e.g. "CREATE INDEX idx ON tbl USING btree (col1, col2)" -> ["col1", "col2"]
func extractColumnsFromDef(def string) []string {
	start := strings.LastIndex(def, "(")
	end := strings.LastIndex(def, ")")
	if start < 0 || end <= start {
		return nil
	}
	colStr := def[start+1 : end]
	parts := strings.Split(colStr, ",")
	var cols []string
	for _, p := range parts {
		col := strings.TrimSpace(p)
		// Remove ordering and other qualifiers
		col = strings.Split(col, " ")[0]
		if col != "" {
			cols = append(cols, col)
		}
	}
	return cols
}

// FindSQLByText searches for SQL queryids matching a text fragment.
func FindSQLByText(ctx context.Context, driver db.Driver, text string) ([]string, error) {
	qr, err := driver.Query(ctx, `SELECT queryid::text
		FROM pg_stat_statements
		WHERE query LIKE $1
		ORDER BY mean_exec_time * calls DESC LIMIT 5`, "%"+text+"%")
	if err != nil {
		return nil, fmt.Errorf("find sql by text: %w", err)
	}
	var queryIDs []string
	for _, row := range qr.Rows {
		if len(row) > 0 {
			queryIDs = append(queryIDs, toString(row[0]))
		}
	}
	return queryIDs, nil
}

// FindTopSQLs returns queryid strings of the top N most costly SQLs.
func FindTopSQLs(ctx context.Context, driver db.Driver, n int) ([]string, error) {
	qr, err := driver.Query(ctx, fmt.Sprintf(`SELECT queryid::text
		FROM pg_stat_statements
		WHERE queryid IS NOT NULL
		ORDER BY mean_exec_time * calls DESC
		LIMIT %d`, n))
	if err != nil {
		return nil, fmt.Errorf("find top sqls: %w", err)
	}
	var queryIDs []string
	for _, row := range qr.Rows {
		if len(row) > 0 {
			queryIDs = append(queryIDs, toString(row[0]))
		}
	}
	return queryIDs, nil
}

