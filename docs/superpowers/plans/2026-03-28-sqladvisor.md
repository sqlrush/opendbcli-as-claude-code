# SQL Advisor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an independent SQL Advisor subsystem that analyzes execution plans for Top SQLs and outputs actionable optimization suggestions with runnable SQL.

**Architecture:** SQL Advisor is a standalone module (`internal/sqladvisor/` for shared types, `internal/oracle/sqladvisor/` for Oracle implementation). It collects data from v$sql/v$sql_plan/DBA_TAB_STATISTICS, parses plan trees, runs 7 analyzers, and generates structured reports. Entry points: `/sqladvisor` skill + Rule Engine integration.

**Tech Stack:** Go, Oracle v$ views, existing skill.Registry pattern, existing db.Driver interface.

---

## File Structure

### New Files (Shared — no build tag)
| File | Responsibility |
|------|---------------|
| `internal/sqladvisor/types.go` | PlanNode, Finding, Suggestion, SQLReport, DataDepth, AnalyzeContext |
| `internal/sqladvisor/analyzer.go` | Analyzer interface |
| `internal/sqladvisor/report.go` | Report rendering to formatted text |

### New Files (Oracle — build tag: oracle || full)
| File | Responsibility |
|------|---------------|
| `internal/oracle/sqladvisor/advisor.go` | Analyze() entry point, orchestrates collect→parse→analyze→report |
| `internal/oracle/sqladvisor/collector.go` | SQL queries against v$sql, v$sql_plan, DBA_TAB_STATISTICS, DBA_INDEXES |
| `internal/oracle/sqladvisor/plan_parser.go` | Flat rows → PlanNode tree |
| `internal/oracle/sqladvisor/analyzers/access_path.go` | Full scan detection, index recommendation |
| `internal/oracle/sqladvisor/analyzers/predicate.go` | Implicit conversion, function-on-column detection |
| `internal/oracle/sqladvisor/analyzers/join.go` | NL/Hash/Sort-Merge analysis |
| `internal/oracle/sqladvisor/analyzers/statistics.go` | Stale stats, missing histogram |
| `internal/oracle/sqladvisor/analyzers/plan_stability.go` | Multi-plan drift, ACS |
| `internal/oracle/sqladvisor/analyzers/resource.go` | Buffer gets efficiency, sort spill |
| `internal/oracle/sqladvisor/analyzers/rewrite.go` | SELECT *, unnecessary ORDER BY, partition pruning |
| `internal/oracle/skill/query/sqladvisor_skill.go` | /sqladvisor skill registration |

### Modified Files
| File | Change |
|------|--------|
| `internal/oracle/register.go` | Register sqladvisor skill |
| `internal/oracle/skill/ai/rule_skill.go` | Integrate SQL Advisor into buildLiveReport, enrich WD025/WD021/WD022/WD023 |
| `internal/oracle/ruleengine/rules_sql_perf.go` | WD025 改为信号触发器，调 SQL Advisor |
| `internal/oracle/ruleengine/rules_sql_tuning.go` | Delete all 25 rules after analyzer absorption |

---

## Task 1: Shared Types (internal/sqladvisor/types.go)

**Files:**
- Create: `internal/sqladvisor/types.go`

- [ ] **Step 1: Create the shared types file**

```go
// internal/sqladvisor/types.go
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
	TableStats    map[string]*TableStat // "OWNER.TABLE" → stat
	IndexMap      map[string][]IndexInfo // "OWNER.TABLE" → indexes
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
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/sqladvisor/...`
Expected: clean build

- [ ] **Step 3: Commit**

```bash
git add internal/sqladvisor/types.go
git commit -m "feat(sqladvisor): add shared types — PlanNode, Finding, SQLReport"
```

---

## Task 2: Analyzer Interface (internal/sqladvisor/analyzer.go)

**Files:**
- Create: `internal/sqladvisor/analyzer.go`

- [ ] **Step 1: Create the analyzer interface**

```go
// internal/sqladvisor/analyzer.go
package sqladvisor

// Analyzer inspects an execution plan and produces findings.
type Analyzer interface {
	Name() string
	Analyze(ctx *AnalyzeContext) []Finding
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/sqladvisor/...`
Expected: clean build

- [ ] **Step 3: Commit**

```bash
git add internal/sqladvisor/analyzer.go
git commit -m "feat(sqladvisor): add Analyzer interface"
```

---

## Task 3: Report Renderer (internal/sqladvisor/report.go)

**Files:**
- Create: `internal/sqladvisor/report.go`

- [ ] **Step 1: Create the report renderer**

```go
// internal/sqladvisor/report.go
package sqladvisor

import (
	"fmt"
	"strings"
)

// RenderReport formats an SQLReport into human-readable text.
func RenderReport(r *SQLReport) string {
	var sb strings.Builder

	sb.WriteString("═══ SQL Advisor 诊断报告 ═══\n\n")
	sb.WriteString(fmt.Sprintf("SQL ID: %s\n", r.SQLID))
	if r.SQLText != "" {
		text := r.SQLText
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("SQL 文本: %s\n", text))
	}
	sb.WriteString(fmt.Sprintf("执行次数: %d    平均耗时: %.2fs    逻辑读: %d/次    物理读: %d/次\n\n",
		r.ExecCount, r.AvgElapsedSec, r.AvgBufferGets, r.AvgDiskReads))

	// Render plan tree
	if r.PlanTree != nil {
		sb.WriteString("── 执行计划 ──\n")
		sb.WriteString(renderPlanHeader())
		renderPlanNode(&sb, r.PlanTree, 0)
		sb.WriteString("\n")
	}

	// Render multi-plan comparison
	if len(r.Plans) > 1 {
		sb.WriteString("── 多计划对比 ──\n")
		for i, p := range r.Plans {
			marker := "  "
			if i == len(r.Plans)-1 {
				marker = "→ "
			}
			sb.WriteString(fmt.Sprintf("  %sPlan %d: avg %.3fs, %d 次执行, 逻辑读 %d/次\n",
				marker, p.PlanHashValue, p.AvgElapsedSec, p.Executions, p.AvgBufferGets))
		}
		sb.WriteString("\n")
	}

	// Render findings
	if len(r.Findings) > 0 {
		sb.WriteString(fmt.Sprintf("── 问题诊断（%d 个问题）──\n\n", len(r.Findings)))
		for _, f := range r.Findings {
			icon := "ℹ"
			if f.Severity == "P1" {
				icon = "⚠"
			} else if f.Severity == "P2" {
				icon = "⚠"
			}
			sb.WriteString(fmt.Sprintf("  %s %s: %s\n", icon, f.Severity, f.Summary))
			if f.Detail != "" {
				sb.WriteString(fmt.Sprintf("     %s\n", f.Detail))
			}
			for _, s := range f.Suggestions {
				sb.WriteString(fmt.Sprintf("     ➜ %s\n", s.Action))
				if s.SQL != "" {
					sb.WriteString(fmt.Sprintf("       %s\n", s.SQL))
				}
				if s.Impact != "" {
					sb.WriteString(fmt.Sprintf("       预期: %s\n", s.Impact))
				}
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("── 未发现明显问题 ──\n\n")
	}

	// Data depth hint
	sb.WriteString(fmt.Sprintf("── 数据深度: %s ──\n", r.DataDepth))
	if r.UpgradeHint != "" {
		sb.WriteString(fmt.Sprintf("  %s\n", r.UpgradeHint))
	}

	return sb.String()
}

func renderPlanHeader() string {
	return fmt.Sprintf("  %-4s %-4s %-30s %-20s %10s %10s %s\n",
		"Id", "Pid", "Operation", "Name", "Rows", "Cost", "Filter")
}

func renderPlanNode(sb *strings.Builder, node *PlanNode, depth int) {
	indent := strings.Repeat("  ", depth)
	op := node.Operation
	if node.Options != "" {
		op += " " + node.Options
	}
	filter := node.FilterPred
	if len(filter) > 40 {
		filter = filter[:40] + "..."
	}

	rowsStr := fmt.Sprintf("%d", node.Rows)
	if node.ActualRows != nil {
		rowsStr = fmt.Sprintf("%d→%d", node.Rows, *node.ActualRows)
	}

	sb.WriteString(fmt.Sprintf("  %-4d %-4d %s%-30s %-20s %10s %10d %s\n",
		node.ID, node.ParentID, indent, op, node.ObjectName,
		rowsStr, node.Cost, filter))

	for _, child := range node.Children {
		renderPlanNode(sb, child, depth+1)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/sqladvisor/...`
Expected: clean build

- [ ] **Step 3: Commit**

```bash
git add internal/sqladvisor/report.go
git commit -m "feat(sqladvisor): add report renderer"
```

---

## Task 4: Oracle Collector (internal/oracle/sqladvisor/collector.go)

**Files:**
- Create: `internal/oracle/sqladvisor/collector.go`

- [ ] **Step 1: Create the data collector**

This file contains all SQL queries and the Collect() function that gathers data from Oracle v$ views. Reference the spec's section 1.7 for exact SQL queries.

Key functions:
- `Collect(ctx, driver, sqlID) (*sqladvisor.SQLReport, error)` — main entry
- `collectSQLInfo(ctx, driver, sqlID)` — v$sql base stats
- `collectPlanTree(ctx, driver, sqlID, planHash)` — v$sql_plan full tree
- `collectPlanStats(ctx, driver, sqlID, planHash)` — v$sql_plan_statistics_all (adaptive)
- `collectTableStats(ctx, driver, tables)` — DBA_TAB_STATISTICS
- `collectIndexInfo(ctx, driver, tables)` — DBA_INDEXES + DBA_IND_COLUMNS
- `collectPlanVariants(ctx, driver, sqlID)` — multi-plan comparison
- `findSQLByText(ctx, driver, text)` — /sqladvisor "SQL文本" mode

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/oracle/sqladvisor/...`

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/collector.go
git commit -m "feat(sqladvisor/oracle): add data collector — v\$sql, v\$sql_plan, stats, indexes"
```

---

## Task 5: Plan Parser (internal/oracle/sqladvisor/plan_parser.go)

**Files:**
- Create: `internal/oracle/sqladvisor/plan_parser.go`

- [ ] **Step 1: Create the plan parser**

Converts flat rows (id, parent_id, operation...) into a `PlanNode` tree using parent_id linkage.

Key function:
- `ParsePlanTree(rows []PlanRow) *sqladvisor.PlanNode` — builds tree from flat rows
- `extractTablesFromPlan(root *sqladvisor.PlanNode) []TableRef` — finds all table references in plan

- [ ] **Step 2: Write tests**

Create `internal/oracle/sqladvisor/plan_parser_test.go` with:
- Test tree building from flat rows (3-level tree: SELECT → NL → TABLE ACCESS FULL + INDEX SCAN)
- Test table extraction from plan tree
- Test edge cases: single-node plan, missing parent_id

- [ ] **Step 3: Run tests**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/oracle/sqladvisor/... -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/oracle/sqladvisor/plan_parser.go internal/oracle/sqladvisor/plan_parser_test.go
git commit -m "feat(sqladvisor/oracle): add plan parser — flat rows to PlanNode tree"
```

---

## Task 6: Access Path Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/access_path.go`

- [ ] **Step 1: Implement access path analyzer**

Absorbs old rules: `full_scan_large_table`, `missing_index`, `function_based_index_needed`, `composite_index_order`, `index_skip_scan_slow`, `cartesian_join`.

Key checks:
1. Walk plan tree leaf nodes for TABLE ACCESS FULL
2. For each full-scan table: lookup TableStats → if num_rows > 10000 and E-Rows/num_rows < 10%, it's selective → should use index
3. Check IndexMap to see if WHERE columns have indexes → if not, suggest CREATE INDEX
4. Detect CARTESIAN JOIN (MERGE JOIN CARTESIAN)
5. Detect INDEX SKIP SCAN with high cost

- [ ] **Step 2: Write tests with mock plan trees**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/access_path.go
git commit -m "feat(sqladvisor/oracle): add access_path analyzer — full scan, index, cartesian"
```

---

## Task 7: Predicate Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/predicate.go`

- [ ] **Step 1: Implement predicate analyzer**

New capability — no old rules to absorb.

Key checks:
1. Parse `filter_predicates` text for TO_NUMBER(), TO_CHAR() wrapping indexed columns → implicit type conversion
2. Detect LIKE '%prefix' patterns (leading wildcard kills index)
3. Detect function calls on indexed columns: UPPER(), TRUNC(), NVL()
4. For each finding: suggest rewriting WHERE clause or creating function-based index

- [ ] **Step 2: Write tests with sample filter_predicates strings**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/predicate.go
git commit -m "feat(sqladvisor/oracle): add predicate analyzer — implicit conversion, function-on-column"
```

---

## Task 8: Join Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/join.go`

- [ ] **Step 1: Implement join analyzer**

Absorbs old rules: `nl_high_starts`, `hash_join_spill`, `sort_merge_inefficient`.

Key checks:
1. Find NESTED LOOPS nodes → check child E-Rows: if driver > 10000 rows → suggest HASH JOIN
2. Find HASH JOIN nodes → if E-Rows of build side is very large → potential spill → check PGA
3. Find SORT MERGE JOIN → often suboptimal vs HASH JOIN for equi-joins
4. If ActualRows available (DataDepthFull): compare Starts × A-Rows vs E-Rows for NL inefficiency

- [ ] **Step 2: Write tests**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/join.go
git commit -m "feat(sqladvisor/oracle): add join analyzer — NL/Hash/SortMerge"
```

---

## Task 9: Statistics Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/statistics.go`

- [ ] **Step 1: Implement statistics analyzer**

Absorbs old rules: `stale_table_stats`, `no_histogram`, `dynamic_sampling_low`, `extended_stats_needed`.

Key checks:
1. For each table in plan: check DaysSinceStats > 30 → stale
2. Check StaleStats flag from DBA_TAB_STATISTICS
3. If table has filter_predicates with skewed columns but no histogram → suggest gathering
4. If E-Rows vs actual table num_rows ratio > 10x → stats severely wrong

- [ ] **Step 2: Write tests**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/statistics.go
git commit -m "feat(sqladvisor/oracle): add statistics analyzer — stale stats, histogram"
```

---

## Task 10: Plan Stability Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/plan_stability.go`

- [ ] **Step 1: Implement plan stability analyzer**

Absorbs old rules: `spm_baseline`, `sql_profile_drift`, `adaptive_plan_flapping`, `bind_variable_peeking`.

Key checks:
1. If len(Plans) > 1: compare best vs worst plan → if ratio > 3x → plan regression
2. Suggest SPM baseline for best plan
3. If VersionCount > 5 → possible ACS/bind peeking issue → suggest cursor_sharing check

- [ ] **Step 2: Write tests**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/plan_stability.go
git commit -m "feat(sqladvisor/oracle): add plan_stability analyzer — drift, SPM, ACS"
```

---

## Task 11: Resource Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/resource.go`

- [ ] **Step 1: Implement resource analyzer**

Absorbs old rules: `pq_skew_detected`, `pq_dop_downgrade`, `pq_resource_exhausted`.

Key checks:
1. buffer_gets / rows_processed ratio: if > 1000 → extremely inefficient
2. Detect SORT operations in plan with large E-Rows → potential TEMP spill
3. Check for PX (parallel) operations and DOP settings

- [ ] **Step 2: Write tests**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/resource.go
git commit -m "feat(sqladvisor/oracle): add resource analyzer — efficiency ratio, sort spill, PX"
```

---

## Task 12: Rewrite Analyzer

**Files:**
- Create: `internal/oracle/sqladvisor/analyzers/rewrite.go`

- [ ] **Step 1: Implement rewrite analyzer**

Absorbs old rules: `unnesting_blocked`, `view_merging_blocked`, `partition_pruning_failed`, `unused_index`, `invisible_index_test`.

Key checks:
1. Check SQL text for SELECT * → suggest explicit column list
2. Check for ORDER BY that returns many rows but caller only needs top N
3. Check plan for PARTITION RANGE ALL on partitioned table → missing partition key in WHERE
4. Check for VIEW operations → view merging blocked

- [ ] **Step 2: Write tests**

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/analyzers/rewrite.go
git commit -m "feat(sqladvisor/oracle): add rewrite analyzer — SELECT *, partition pruning, view merge"
```

---

## Task 13: Advisor Entry Point (internal/oracle/sqladvisor/advisor.go)

**Files:**
- Create: `internal/oracle/sqladvisor/advisor.go`

- [ ] **Step 1: Implement the Advisor orchestrator**

```go
package sqladvisor

import (
	"context"
	"sort"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/oracle/sqladvisor/analyzers"
)

type Advisor struct {
	driver    db.Driver
	analyzers []sadv.Analyzer
}

func New(driver db.Driver) *Advisor {
	return &Advisor{
		driver: driver,
		analyzers: []sadv.Analyzer{
			analyzers.NewAccessPath(),
			analyzers.NewPredicate(),
			analyzers.NewJoin(),
			analyzers.NewStatistics(),
			analyzers.NewPlanStability(),
			analyzers.NewResource(),
			analyzers.NewRewrite(),
		},
	}
}

func (a *Advisor) Analyze(ctx context.Context, sqlID string) (*sadv.SQLReport, error) {
	// 1. Collect
	report, err := Collect(ctx, a.driver, sqlID)
	if err != nil {
		return nil, err
	}

	// 2. Build analyze context
	actx := &sadv.AnalyzeContext{
		Report:     report,
		TableStats: report.TableStats,
		IndexMap:   report.IndexMap,
		PlanTree:   report.PlanTree,
		DataDepth:  report.DataDepth,
	}

	// 3. Run all analyzers
	for _, az := range a.analyzers {
		findings := az.Analyze(actx)
		report.Findings = append(report.Findings, findings...)
	}

	// 4. Rank by severity
	sort.Slice(report.Findings, func(i, j int) bool {
		return severityRank(report.Findings[i].Severity) > severityRank(report.Findings[j].Severity)
	})

	// 5. Set upgrade hint if needed
	if report.DataDepth == sadv.DataDepthBasic && hasUncertainFindings(report.Findings) {
		report.UpgradeHint = "建议: ALTER SYSTEM SET STATISTICS_LEVEL=ALL 后重新执行该 SQL，\n  再次运行 /sqladvisor " + sqlID + " 可获得实际行数(A-Rows)对比分析"
	}

	return report, nil
}

func (a *Advisor) FindAndAnalyze(ctx context.Context, sqlText string) ([]*sadv.SQLReport, error) {
	sqlIDs, err := FindSQLByText(ctx, a.driver, sqlText)
	if err != nil {
		return nil, err
	}
	var reports []*sadv.SQLReport
	for _, id := range sqlIDs {
		r, err := a.Analyze(ctx, id)
		if err != nil {
			continue
		}
		reports = append(reports, r)
	}
	return reports, nil
}

func severityRank(s string) int {
	switch s {
	case "P1":
		return 3
	case "P2":
		return 2
	case "P3":
		return 1
	default:
		return 0
	}
}

func hasUncertainFindings(findings []Finding) bool {
	// If any finding mentions estimated rows without actual confirmation
	for _, f := range findings {
		if f.Category == "join" || f.Category == "access_path" {
			return true
		}
	}
	return false
}
```

Note: adjust import paths to match actual module path (check go.mod).

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/oracle/sqladvisor/...`

- [ ] **Step 3: Commit**

```bash
git add internal/oracle/sqladvisor/advisor.go
git commit -m "feat(sqladvisor/oracle): add Advisor orchestrator — collect, parse, analyze, report"
```

---

## Task 14: /sqladvisor Skill

**Files:**
- Create: `internal/oracle/skill/query/sqladvisor_skill.go`
- Modify: `internal/oracle/register.go`

- [ ] **Step 1: Create the skill**

Follow the ExplainSkill pattern (same directory). Three modes:
1. `/sqladvisor 8rd2y271f37v8` → Analyze by sql_id
2. `/sqladvisor "SELECT * FROM ..."` → FindAndAnalyze by text
3. `/sqladvisor` → Analyze top problematic SQLs from current v$session

The skill should:
- Parse args to determine mode
- Call advisor.Analyze() or advisor.FindAndAnalyze()
- Render report using sqladvisor.RenderReport()
- Return skill.Result{Type: skill.ResultText, Rendered: rendered}

- [ ] **Step 2: Register in register.go**

Add to RegisterSkills function after the existing query skills:
```go
registry.RegisterForDB("oracle", query.NewSQLAdvisorSkill(driver))
```

- [ ] **Step 3: Integration test — run /sqladvisor manually**

Build and test:
```bash
cd /Users/yingjiewang/opendb
go build -tags full -o ./opendb ./cmd/opendb/
./opendb -c oracle
# then type: /sqladvisor
```

- [ ] **Step 4: Commit**

```bash
git add internal/oracle/skill/query/sqladvisor_skill.go internal/oracle/register.go
git commit -m "feat: add /sqladvisor skill — sql_id, text match, auto top SQL modes"
```

---

## Task 15: Rule Engine Integration

**Files:**
- Modify: `internal/oracle/skill/ai/rule_skill.go`
- Modify: `internal/oracle/ruleengine/rules_sql_perf.go`

- [ ] **Step 1: Add SQL Advisor to buildLiveReport enrichment**

In `rule_skill.go`, after the existing TopSQL enrichment loop (around line 461-502), add:

```go
// Enrich TopSQLs with SQL Advisor findings for full-scan or slow SQLs
advisor := oraclesqladvisor.New(s.driver)
for i, sql := range report.TopSQLs {
	if sql.HasFullScan || (sql.AvgElapsedSec > 1.0 && sql.MaxConcurrent >= 2) {
		advReport, err := advisor.Analyze(ctx, sql.SQLID)
		if err == nil && len(advReport.Findings) > 0 {
			report.TopSQLs[i].AdvisorFindings = advReport.Findings
			report.TopSQLs[i].AdvisorReport = advReport
		}
	}
}
```

This requires adding `AdvisorFindings` and `AdvisorReport` fields to `sentinel.SQLProfile` struct.

- [ ] **Step 2: Modify WD025 to use SQL Advisor findings**

In `rules_sql_perf.go`, change WD025's Tree.Check to use `ctx.TopSQLs[i].AdvisorReport` when available, generating Findings and Actions from the advisor report instead of the current generic diagnosis.

- [ ] **Step 3: Verify build and rule output**

```bash
cd /Users/yingjiewang/opendb
go build -tags full -o ./opendb ./cmd/opendb/
```

- [ ] **Step 4: Commit**

```bash
git add internal/oracle/skill/ai/rule_skill.go internal/oracle/ruleengine/rules_sql_perf.go internal/oracle/sentinel/types.go
git commit -m "feat: integrate SQL Advisor into rule engine — WD025 uses advisor findings"
```

---

## Task 16: Delete Old SQL Tuning Rules

**Files:**
- Modify: `internal/oracle/ruleengine/rules_sql_tuning.go`
- Modify: `internal/oracle/ruleengine/community.go` (remove registration of old rules)

- [ ] **Step 1: Verify all 25 rules are covered by analyzers**

Cross-check the migration table from the spec:
- access_path.go covers: full_scan_large_table, missing_index, function_based_index_needed, composite_index_order, index_skip_scan_slow, cartesian_join (6)
- join.go covers: nl_high_starts, hash_join_spill, sort_merge_inefficient (3)
- statistics.go covers: stale_table_stats, no_histogram, dynamic_sampling_low, extended_stats_needed (4)
- plan_stability.go covers: spm_baseline, sql_profile_drift, adaptive_plan_flapping, bind_variable_peeking (4)
- resource.go covers: pq_skew_detected, pq_dop_downgrade, pq_resource_exhausted (3)
- rewrite.go covers: unnesting_blocked, view_merging_blocked, partition_pruning_failed, unused_index, invisible_index_test (5)
Total: 25 ✓

- [ ] **Step 2: Delete rules_sql_tuning.go**

Remove the file entirely. Also remove all references in `community.go`'s `Rules()` method that register these 25 rules.

- [ ] **Step 3: Verify build**

Run: `cd /Users/yingjiewang/opendb && go build -tags full -o ./opendb ./cmd/opendb/`
Expected: clean build (no references to deleted rules)

- [ ] **Step 4: Commit**

```bash
git add -A internal/oracle/ruleengine/
git commit -m "refactor: delete 25 SQL tuning rules — absorbed into SQL Advisor analyzers"
```

---

## Task 17: End-to-End Test on Test Server

- [ ] **Step 1: Build and deploy**

```bash
cd /Users/yingjiewang/opendb
go build -tags full -o ./opendb ./cmd/opendb/
# On test server:
cd /root/opendb && git pull origin main && go build -tags full -o ./opendb ./cmd/opendb/
```

- [ ] **Step 2: Test /sqladvisor with known SQL**

```bash
./opendb -c oracle
/sqladvisor   # auto mode — should analyze current top SQLs
```

- [ ] **Step 3: Test /sqladvisor with specific sql_id**

```bash
/sqladvisor 8rd2y271f37v8   # or any known sql_id
```

- [ ] **Step 4: Test /rule live integration**

```bash
# Create a full-scan load
./opendb -c oracle '/rule live'
# Verify SQL Advisor findings appear in rule output
```

- [ ] **Step 5: Re-test T016-T019, T081 scenarios**

Inject test scenarios on test server and verify score improvement from ~15 to 60+.
