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
 *	  internal/mysql/sqladvisor/collector.go
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
func Collect(ctx context.Context, driver db.Driver, digest string) (*sadv.SQLReport, error) {
	report := &sadv.SQLReport{
		SQLID:      digest,
		TableStats: make(map[string]*sadv.TableStat),
		IndexMap:   make(map[string][]sadv.IndexInfo),
		DataDepth:  sadv.DataDepthBasic,
	}

	if err := collectSQLInfo(ctx, driver, digest, report); err != nil {
		return nil, fmt.Errorf("collect sql info: %w", err)
	}

	if err := collectPlanTree(ctx, driver, digest, report); err != nil {
		// Plan collection failure is not fatal — we still have SQL stats
		report.UpgradeHint = "无法获取执行计划，建议手动 EXPLAIN"
	}

	tables := ExtractTables(report.PlanTree)
	for _, table := range tables {
		collectTableStats(ctx, driver, table, report)
		collectIndexInfo(ctx, driver, table, report)
	}

	return report, nil
}

func collectSQLInfo(ctx context.Context, driver db.Driver, digest string, report *sadv.SQLReport) error {
	qr, err := driver.Query(ctx, `SELECT
		LEFT(DIGEST_TEXT, 500),
		COUNT_STAR,
		ROUND(AVG_TIMER_WAIT / 1e12, 4),
		ROUND(SUM_ROWS_EXAMINED / NULLIF(COUNT_STAR, 0)),
		ROUND(SUM_ROWS_SENT / NULLIF(COUNT_STAR, 0)),
		ROUND(SUM_NO_INDEX_USED / NULLIF(COUNT_STAR, 0), 2),
		ROUND(SUM_NO_GOOD_INDEX_USED / NULLIF(COUNT_STAR, 0), 2),
		ROUND(SUM_LOCK_TIME / NULLIF(COUNT_STAR, 0) / 1e12, 4),
		ROUND(SUM_SORT_ROWS / NULLIF(COUNT_STAR, 0)),
		ROUND(SUM_CREATED_TMP_TABLES / NULLIF(COUNT_STAR, 0), 2),
		ROUND(SUM_CREATED_TMP_DISK_TABLES / NULLIF(COUNT_STAR, 0), 2)
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST = ?`, digest)
	if err != nil {
		return err
	}
	if len(qr.Rows) == 0 {
		return fmt.Errorf("digest %s not found in events_statements_summary_by_digest", digest)
	}

	row := qr.Rows[0]
	report.SQLText = toString(row[0])
	report.ExecCount = toInt64(row[1])
	report.AvgElapsedSec = toFloat64(row[2])
	report.AvgBufferGets = toInt64(row[3])  // rows_examined as proxy for buffer gets
	report.AvgRowsProc = toInt64(row[4])    // rows_sent
	report.AvgDiskReads = toInt64(row[3])   // rows_examined

	return nil
}

func collectPlanTree(ctx context.Context, driver db.Driver, digest string, report *sadv.SQLReport) error {
	// Get the actual SQL text for EXPLAIN
	sqlText := report.SQLText
	if sqlText == "" {
		return fmt.Errorf("no SQL text available for EXPLAIN")
	}

	// Strip trailing semicolons and truncation markers
	sqlText = strings.TrimRight(sqlText, "; ")
	if strings.HasSuffix(sqlText, "...") {
		return fmt.Errorf("SQL text truncated, cannot EXPLAIN")
	}

	// Only EXPLAIN SELECT/INSERT/UPDATE/DELETE/WITH
	upper := strings.ToUpper(strings.TrimSpace(sqlText))
	if !strings.HasPrefix(upper, "SELECT") &&
		!strings.HasPrefix(upper, "INSERT") &&
		!strings.HasPrefix(upper, "UPDATE") &&
		!strings.HasPrefix(upper, "DELETE") &&
		!strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("cannot EXPLAIN statement type")
	}

	qr, err := driver.Query(ctx, "EXPLAIN FORMAT=JSON "+sqlText)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	if len(qr.Rows) == 0 || len(qr.Rows[0]) == 0 {
		return fmt.Errorf("empty EXPLAIN output")
	}

	jsonData := []byte(toString(qr.Rows[0][0]))
	planTree, err := ParseMySQLExplainJSON(jsonData)
	if err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	report.PlanTree = planTree
	return nil
}

func collectTableStats(ctx context.Context, driver db.Driver, table string, report *sadv.SQLReport) {
	qr, err := driver.Query(ctx, `SELECT
		TABLE_SCHEMA,
		TABLE_NAME,
		IFNULL(TABLE_ROWS, 0),
		IFNULL(DATA_LENGTH, 0),
		IFNULL(UPDATE_TIME, CREATE_TIME),
		ROUND((DATA_LENGTH + INDEX_LENGTH) / 1048576, 2)
		FROM information_schema.TABLES
		WHERE TABLE_NAME = ? AND TABLE_SCHEMA = DATABASE()`, table)
	if err != nil || len(qr.Rows) == 0 {
		return
	}

	row := qr.Rows[0]
	stat := &sadv.TableStat{
		Owner:     toString(row[0]),
		TableName: toString(row[1]),
		NumRows:   toInt64(row[2]),
		Blocks:    toInt64(row[3]) / 16384, // InnoDB 16KB pages
		SizeMB:    toFloat64(row[5]),
	}
	if row[4] != nil {
		stat.LastAnalyzed = toString(row[4])
	}

	key := stat.TableName
	report.TableStats[key] = stat
}

func collectIndexInfo(ctx context.Context, driver db.Driver, table string, report *sadv.SQLReport) {
	qr, err := driver.Query(ctx, `SELECT
		INDEX_NAME,
		CASE WHEN NON_UNIQUE = 0 THEN 'UNIQUE' ELSE 'NONUNIQUE' END,
		COLUMN_NAME,
		SEQ_IN_INDEX,
		CARDINALITY
		FROM information_schema.STATISTICS
		WHERE TABLE_NAME = ? AND TABLE_SCHEMA = DATABASE()
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`, table)
	if err != nil || len(qr.Rows) == 0 {
		return
	}

	key := table
	for _, row := range qr.Rows {
		idx := sadv.IndexInfo{
			IndexName:      toString(row[0]),
			Uniqueness:     toString(row[1]),
			ColumnName:     toString(row[2]),
			ColumnPosition: int(toInt64(row[3])),
			DistinctKeys:   toInt64(row[4]),
			Status:         "VISIBLE",
		}
		report.IndexMap[key] = append(report.IndexMap[key], idx)
	}
}

// FindSQLByText searches for SQL digests matching a text fragment.
func FindSQLByText(ctx context.Context, driver db.Driver, text string) ([]string, error) {
	qr, err := driver.Query(ctx, `SELECT DIGEST
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST_TEXT LIKE ?
		ORDER BY SUM_TIMER_WAIT DESC LIMIT 5`, "%"+text+"%")
	if err != nil {
		return nil, err
	}
	var digests []string
	for _, row := range qr.Rows {
		if len(row) > 0 {
			digests = append(digests, toString(row[0]))
		}
	}
	return digests, nil
}

// FindTopSQLs returns digest IDs of the top N slowest active SQLs.
func FindTopSQLs(ctx context.Context, driver db.Driver, n int) ([]string, error) {
	qr, err := driver.Query(ctx, fmt.Sprintf(`SELECT DIGEST
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST IS NOT NULL
		AND LAST_SEEN > DATE_SUB(NOW(), INTERVAL 10 MINUTE)
		ORDER BY SUM_TIMER_WAIT DESC LIMIT %d`, n))
	if err != nil {
		return nil, err
	}
	var digests []string
	for _, row := range qr.Rows {
		if len(row) > 0 {
			digests = append(digests, toString(row[0]))
		}
	}
	return digests, nil
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

func toFloat64(v any) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case []byte:
		var f float64
		fmt.Sscanf(string(n), "%f", &f)
		return f
	default:
		var f float64
		fmt.Sscanf(fmt.Sprint(v), "%f", &f)
		return f
	}
}

func toInt64(v any) int64 {
	return int64(toFloat64(v))
}
