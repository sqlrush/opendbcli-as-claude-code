/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  PlanRow holds one row from v$sql_plan or
 *	  v$sql_plan_statistics_all.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/collector.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// PlanRow holds one row from v$sql_plan or v$sql_plan_statistics_all.
type PlanRow struct {
	ID          int
	ParentID    int
	Operation   string
	Options     string
	ObjectOwner string
	ObjectName  string
	Rows        int64
	ActualRows  *int64
	Starts      *int64
	Bytes       int64
	Cost        int64
	FilterPred  string
	AccessPred  string
	OtherXML    string
}

// TableRef identifies a table by owner and name.
type TableRef struct {
	Owner string
	Name  string
}

// Collect orchestrates all sub-collectors and returns a complete SQLReport.
func Collect(ctx context.Context, driver db.Driver, sqlID string) (*sadv.SQLReport, error) {
	sqlText, execCount, avgElapsed, avgGets, avgReads, avgRows, planHash, err := collectSQLInfo(ctx, driver, sqlID)
	if err != nil {
		return nil, fmt.Errorf("collect sql info: %w", err)
	}

	report := &sadv.SQLReport{
		SQLID:         sqlID,
		SQLText:       sqlText,
		ExecCount:     execCount,
		AvgElapsedSec: avgElapsed,
		AvgBufferGets: avgGets,
		AvgDiskReads:  avgReads,
		AvgRowsProc:   avgRows,
	}

	// Try full stats first, fall back to basic plan tree.
	planRows, err := collectPlanStats(ctx, driver, sqlID, planHash)
	if err == nil && len(planRows) > 0 {
		report.DataDepth = sadv.DataDepthFull
	} else {
		planRows, err = collectPlanTree(ctx, driver, sqlID, planHash)
		if err != nil {
			return nil, fmt.Errorf("collect plan tree: %w", err)
		}
		report.DataDepth = sadv.DataDepthBasic
		report.UpgradeHint = "Run: ALTER SESSION SET statistics_level = ALL; then re-execute the SQL to get actual row counts."
	}

	report.PlanTree = ParsePlanTree(planRows)

	plans, err := collectPlanVariants(ctx, driver, sqlID)
	if err != nil {
		return nil, fmt.Errorf("collect plan variants: %w", err)
	}
	report.Plans = plans

	tables := ExtractTables(report.PlanTree)

	tableStats, err := collectTableStats(ctx, driver, tables)
	if err != nil {
		return nil, fmt.Errorf("collect table stats: %w", err)
	}
	report.TableStats = tableStats

	indexMap, err := collectIndexInfo(ctx, driver, tables)
	if err != nil {
		return nil, fmt.Errorf("collect index info: %w", err)
	}
	report.IndexMap = indexMap

	return report, nil
}

// collectSQLInfo retrieves basic SQL execution statistics from v$sql.
func collectSQLInfo(ctx context.Context, driver db.Driver, sqlID string) (
	sqlText string, execCount int64, avgElapsed float64,
	avgGets, avgReads, avgRows, planHash int64, err error,
) {
	const query = `SELECT sql_text, executions,
       ROUND(elapsed_time/GREATEST(executions,1)/1e6, 3) avg_sec,
       ROUND(buffer_gets/GREATEST(executions,1)) avg_gets,
       ROUND(disk_reads/GREATEST(executions,1)) avg_reads,
       ROUND(rows_processed/GREATEST(executions,1)) avg_rows,
       plan_hash_value
FROM v$sql WHERE sql_id = :1
ORDER BY elapsed_time DESC FETCH FIRST 1 ROWS ONLY`

	result, qerr := driver.Query(ctx, query, sqlID)
	if qerr != nil {
		return "", 0, 0, 0, 0, 0, 0, fmt.Errorf("query v$sql: %w", qerr)
	}
	if len(result.Rows) == 0 {
		return "", 0, 0, 0, 0, 0, 0, fmt.Errorf("sql_id %s not found in v$sql", sqlID)
	}

	row := result.Rows[0]
	sqlText = toStr(row[0])
	if len(sqlText) > 500 {
		sqlText = sqlText[:500]
	}
	execCount = toInt64(row[1])
	avgElapsed = toFloat64(row[2])
	avgGets = toInt64(row[3])
	avgReads = toInt64(row[4])
	avgRows = toInt64(row[5])
	planHash = toInt64(row[6])

	return sqlText, execCount, avgElapsed, avgGets, avgReads, avgRows, planHash, nil
}

// collectPlanTree retrieves the execution plan from v$sql_plan.
func collectPlanTree(ctx context.Context, driver db.Driver, sqlID string, planHash int64) ([]PlanRow, error) {
	const query = `SELECT id, parent_id, operation, options, object_owner, object_name,
       cardinality, bytes, cost, filter_predicates, access_predicates, other_xml
FROM v$sql_plan WHERE sql_id = :1 AND plan_hash_value = :2
ORDER BY id`

	result, err := driver.Query(ctx, query, sqlID, planHash)
	if err != nil {
		return nil, fmt.Errorf("query v$sql_plan: %w", err)
	}

	rows := make([]PlanRow, 0, len(result.Rows))
	for _, r := range result.Rows {
		rows = append(rows, PlanRow{
			ID:          int(toInt64(r[0])),
			ParentID:    int(toInt64(r[1])),
			Operation:   toStr(r[2]),
			Options:     toStr(r[3]),
			ObjectOwner: toStr(r[4]),
			ObjectName:  toStr(r[5]),
			Rows:        toInt64(r[6]),
			Bytes:       toInt64(r[7]),
			Cost:        toInt64(r[8]),
			FilterPred:  toStr(r[9]),
			AccessPred:  toStr(r[10]),
			OtherXML:    toStr(r[11]),
		})
	}

	return rows, nil
}

// collectPlanStats retrieves plan data with runtime statistics from v$sql_plan_statistics_all.
// Returns nil, nil if the query fails or returns no rows (caller should fall back).
func collectPlanStats(ctx context.Context, driver db.Driver, sqlID string, planHash int64) ([]PlanRow, error) {
	const query = `SELECT id, operation, options, object_owner, object_name,
       cardinality, last_output_rows, last_starts,
       last_cr_buffer_gets, last_disk_reads,
       bytes, cost, filter_predicates, access_predicates
FROM v$sql_plan_statistics_all
WHERE sql_id = :1 AND plan_hash_value = :2
ORDER BY id`

	result, err := driver.Query(ctx, query, sqlID, planHash)
	if err != nil {
		return nil, nil //nolint:nilerr // intentional fallback
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}

	rows := make([]PlanRow, 0, len(result.Rows))
	for _, r := range result.Rows {
		pr := PlanRow{
			ID:          int(toInt64(r[0])),
			Operation:   toStr(r[1]),
			Options:     toStr(r[2]),
			ObjectOwner: toStr(r[3]),
			ObjectName:  toStr(r[4]),
			Rows:        toInt64(r[5]),
			Bytes:       toInt64(r[10]),
			Cost:        toInt64(r[11]),
			FilterPred:  toStr(r[12]),
			AccessPred:  toStr(r[13]),
		}
		if r[6] != nil {
			v := toInt64(r[6])
			pr.ActualRows = &v
		}
		if r[7] != nil {
			v := toInt64(r[7])
			pr.Starts = &v
		}
		rows = append(rows, pr)
	}

	return rows, nil
}

// collectPlanVariants retrieves all plan variants for a SQL from v$sql.
func collectPlanVariants(ctx context.Context, driver db.Driver, sqlID string) ([]sadv.PlanInfo, error) {
	const query = `SELECT plan_hash_value, SUM(executions) execs,
       ROUND(SUM(elapsed_time)/GREATEST(SUM(executions),1)/1e6, 3) avg_sec,
       ROUND(SUM(buffer_gets)/GREATEST(SUM(executions),1)) avg_gets
FROM v$sql WHERE sql_id = :1
GROUP BY plan_hash_value ORDER BY avg_sec`

	result, err := driver.Query(ctx, query, sqlID)
	if err != nil {
		return nil, fmt.Errorf("query plan variants: %w", err)
	}

	plans := make([]sadv.PlanInfo, 0, len(result.Rows))
	for _, r := range result.Rows {
		plans = append(plans, sadv.PlanInfo{
			PlanHashValue: toInt64(r[0]),
			Executions:    toInt64(r[1]),
			AvgElapsedSec: toFloat64(r[2]),
			AvgBufferGets: toInt64(r[3]),
		})
	}

	return plans, nil
}

// collectTableStats retrieves table statistics from dba_tab_statistics.
func collectTableStats(ctx context.Context, driver db.Driver, tables []TableRef) (map[string]*sadv.TableStat, error) {
	const query = `SELECT owner, table_name, num_rows, blocks,
       TO_CHAR(last_analyzed,'YYYY-MM-DD') last_analyzed,
       ROUND(SYSDATE - last_analyzed) days_since,
       stale_stats
FROM dba_tab_statistics WHERE owner = :1 AND table_name = :2`

	stats := make(map[string]*sadv.TableStat, len(tables))
	for _, t := range tables {
		result, err := driver.Query(ctx, query, t.Owner, t.Name)
		if err != nil {
			continue // skip tables we can't access
		}
		if len(result.Rows) == 0 {
			continue
		}

		r := result.Rows[0]
		key := t.Owner + "." + t.Name
		staleStr := strings.ToUpper(toStr(r[6]))
		stats[key] = &sadv.TableStat{
			Owner:          toStr(r[0]),
			TableName:      toStr(r[1]),
			NumRows:        toInt64(r[2]),
			Blocks:         toInt64(r[3]),
			LastAnalyzed:   toStr(r[4]),
			DaysSinceStats: int(toInt64(r[5])),
			StaleStats:     staleStr == "YES",
		}
	}

	return stats, nil
}

// collectIndexInfo retrieves index information from dba_indexes joined with dba_ind_columns.
func collectIndexInfo(ctx context.Context, driver db.Driver, tables []TableRef) (map[string][]sadv.IndexInfo, error) {
	const query = `SELECT i.index_name, i.uniqueness, ic.column_name, ic.column_position,
       i.status, i.visibility, i.distinct_keys, i.clustering_factor
FROM dba_indexes i JOIN dba_ind_columns ic
  ON i.index_name = ic.index_name AND i.owner = ic.index_owner
WHERE i.table_owner = :1 AND i.table_name = :2
ORDER BY i.index_name, ic.column_position`

	indexMap := make(map[string][]sadv.IndexInfo, len(tables))
	for _, t := range tables {
		result, err := driver.Query(ctx, query, t.Owner, t.Name)
		if err != nil {
			continue // skip tables we can't access
		}

		key := t.Owner + "." + t.Name
		indexes := make([]sadv.IndexInfo, 0, len(result.Rows))
		for _, r := range result.Rows {
			indexes = append(indexes, sadv.IndexInfo{
				IndexName:      toStr(r[0]),
				Uniqueness:     toStr(r[1]),
				ColumnName:     toStr(r[2]),
				ColumnPosition: int(toInt64(r[3])),
				Status:         toStr(r[4]),
				Visibility:     toStr(r[5]),
				DistinctKeys:   toInt64(r[6]),
				ClusterFactor:  toInt64(r[7]),
			})
		}
		if len(indexes) > 0 {
			indexMap[key] = indexes
		}
	}

	return indexMap, nil
}

// FindSQLByText searches for SQL IDs matching the given text fragment.
func FindSQLByText(ctx context.Context, driver db.Driver, text string) ([]string, error) {
	const query = `SELECT sql_id FROM v$sql
WHERE sql_text LIKE '%' || :1 || '%' AND sql_text NOT LIKE '%v$sql%'
ORDER BY elapsed_time DESC FETCH FIRST 5 ROWS ONLY`

	result, err := driver.Query(ctx, query, text)
	if err != nil {
		return nil, fmt.Errorf("search v$sql by text: %w", err)
	}

	ids := make([]string, 0, len(result.Rows))
	for _, r := range result.Rows {
		ids = append(ids, toStr(r[0]))
	}

	return ids, nil
}

// --- type-conversion helpers ---

func toStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func toInt64(v any) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}
