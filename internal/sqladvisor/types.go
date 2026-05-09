/*-------------------------------------------------------------------------
 *
 * types.go
 *	  DataDepth indicates how much plan data is available.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqladvisor/types.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

// DataDepth indicates how much plan data is available.
type DataDepth int

const (
	DataDepthBasic DataDepth = iota // v$sql_plan only (E-Rows)
	DataDepthFull                   // v$sql_plan_statistics_all (A-Rows)
)

func (d DataDepth) String() string {
	if d == DataDepthFull {
		return "v$sql_plan_statistics_all (实际行数)"
	}
	return "v$sql_plan (估算行数)"
}

// PlanNode represents one step in an execution plan tree.
type PlanNode struct {
	ID          int
	ParentID    int
	Operation   string // TABLE ACCESS, NESTED LOOPS, HASH JOIN ...
	Options     string // FULL, BY INDEX ROWID ...
	ObjectName  string
	ObjectOwner string
	Rows        int64  // E-Rows (optimizer estimate)
	ActualRows  *int64 // A-Rows (runtime, nil if unavailable)
	Starts      *int64 // execution starts (nil if unavailable)
	Cost        int64
	Bytes       int64
	FilterPred  string
	AccessPred  string
	OtherXML    string // contains hints etc.
	Children    []*PlanNode
}

// IsFullScan returns true if this node is a full table scan.
func (n *PlanNode) IsFullScan() bool {
	return n.Operation == "TABLE ACCESS" && n.Options == "FULL"
}

// IsIndexScan returns true if this node uses an index.
func (n *PlanNode) IsIndexScan() bool {
	return n.Operation == "INDEX" || (n.Operation == "TABLE ACCESS" && n.Options == "BY INDEX ROWID")
}

// PlanInfo holds one plan variant for multi-plan comparison.
type PlanInfo struct {
	PlanHashValue int64
	Executions    int64
	AvgElapsedSec float64
	AvgBufferGets int64
}

// TableStat holds statistics for one table.
type TableStat struct {
	Owner          string
	TableName      string
	NumRows        int64
	Blocks         int64
	LastAnalyzed   string // YYYY-MM-DD
	DaysSinceStats int
	StaleStats     bool
	SizeMB         float64
}

// IndexInfo holds one index column.
type IndexInfo struct {
	IndexName      string
	Uniqueness     string
	ColumnName     string
	ColumnPosition int
	Status         string
	Visibility     string
	DistinctKeys   int64
	ClusterFactor  int64
}

// Finding represents one diagnosed issue.
type Finding struct {
	Severity    string // P1, P2, P3
	Category    string // access_path, predicate, join, statistics, plan_stability, resource, rewrite
	Summary     string
	Detail      string
	Suggestions []Suggestion
}

// Suggestion represents one actionable fix.
type Suggestion struct {
	Action string // human description
	SQL    string // runnable SQL
	Risk   string
	Impact string
}

// SQLReport is the complete diagnosis for one SQL.
type SQLReport struct {
	SQLID         string
	SQLText       string // first 500 chars
	ExecCount     int64
	AvgElapsedSec float64
	AvgBufferGets int64
	AvgDiskReads  int64
	AvgRowsProc   int64
	PlanTree      *PlanNode
	Plans         []PlanInfo
	TableStats    map[string]*TableStat  // "OWNER.TABLE" -> stat
	IndexMap      map[string][]IndexInfo // "OWNER.TABLE" -> indexes
	Findings      []Finding
	DataDepth     DataDepth
	UpgradeHint   string
}

// AnalyzeContext is the input to each Analyzer.
type AnalyzeContext struct {
	Report     *SQLReport
	TableStats map[string]*TableStat
	IndexMap   map[string][]IndexInfo
	PlanTree   *PlanNode
	DataDepth  DataDepth
}
