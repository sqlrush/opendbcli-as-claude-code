/*-------------------------------------------------------------------------
 *
 * gather.go
 *	  GatherSkill checks for stale/missing statistics and optionally
 *	  gathers them.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/gather.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
)

// SQL to find tables with stale or missing statistics.
const staleStatsSQL = `SELECT s.owner, s.table_name,
       NVL(TO_CHAR(s.last_analyzed, 'MM-DD HH24:MI'), 'NEVER') AS last_analyzed,
       s.stale_stats,
       NVL(m.inserts + m.updates + m.deletes, 0) AS modifications,
       s.num_rows
FROM dba_tab_statistics s
LEFT JOIN dba_tab_modifications m
  ON s.owner = m.table_owner AND s.table_name = m.table_name AND m.partition_name IS NULL
WHERE s.object_type = 'TABLE'
  AND s.owner NOT IN ('SYS','SYSTEM','DBSNMP','OUTLN','APPQOSSYS','WMSYS','CTXSYS','XDB','ORDDATA','MDSYS','OLAPSYS','ORDSYS','GSMADMIN_INTERNAL','LBACSYS','DVSYS','AUDSYS')
  AND (s.stale_stats = 'YES' OR s.last_analyzed IS NULL
       OR (m.inserts + m.updates + m.deletes) > NVL(s.num_rows, 0) * 0.1)
ORDER BY NVL(m.inserts + m.updates + m.deletes, 0) DESC
FETCH FIRST 50 ROWS ONLY`

// SQL to gather statistics for a single table.
// Uses DBMS_STATS.GATHER_TABLE_STATS with no_invalidate => TRUE to avoid cursor invalidation storm.
const gatherOneTableSQL = `BEGIN
  DBMS_STATS.GATHER_TABLE_STATS(
    ownname     => :1,
    tabname     => :2,
    no_invalidate => TRUE,
    cascade     => TRUE
  );
END;`

// GatherSkill checks for stale/missing statistics and optionally gathers them.
type GatherSkill struct {
	driver db.Driver
}

// NewGatherSkill creates a GatherSkill backed by the given driver.
func NewGatherSkill(driver db.Driver) *GatherSkill {
	return &GatherSkill{driver: driver}
}

func (s *GatherSkill) Name() string                       { return "gather" }
func (s *GatherSkill) Description() string                { return "Check and gather stale table statistics" }
func (s *GatherSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelAdmin }

func (s *GatherSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "gather",
		Description: "Check and gather stale table statistics",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "check (default) | run | run <owner.table>",
				},
			},
		},
	}
}

func (s *GatherSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "gather",
		Usage:          "/gather [check|run|run <owner.table>]",
		ArgCompletions: []string{"check", "run"},
		Examples: []string{
			"/gather",
			"/gather check",
			"/gather run",
			"/gather run HR.EMPLOYEES",
		},
	}
}

func (s *GatherSkill) Validate(params skill.Params) error {
	args := strings.Fields(params.StringOr("args", "check"))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "check", "run", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: check, run, run <owner.table>", args[0])
	}
}

func (s *GatherSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.Fields(params.StringOr("args", "check"))
	action := "check"
	if len(args) > 0 {
		action = args[0]
	}

	switch action {
	case "check", "":
		return s.check(ctx)
	case "run":
		if len(args) >= 2 {
			return s.gatherOne(ctx, args[1])
		}
		return s.gatherAll(ctx)
	default:
		return &skill.Result{Type: skill.ResultError, Rendered: fmt.Sprintf("未知操作: %s", action)}, nil
	}
}

func (s *GatherSkill) check(ctx context.Context) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, staleStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying stale stats: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "所有表统计信息均为最新",
			Summary:  "no stale stats",
		}, nil
	}

	var rows [][]any
	for _, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		owner := shared.AsString(row[0])
		table := shared.AsString(row[1])
		lastAnalyzed := shared.AsString(row[2])
		stale := shared.AsString(row[3])
		mods := int(shared.ToFloat64(row[4]))
		numRows := int(shared.ToFloat64(row[5]))

		reason := ""
		if stale == "YES" {
			reason = "STALE"
		} else if lastAnalyzed == "NEVER" {
			reason = "NEVER"
		} else if numRows > 0 && mods > numRows/10 {
			reason = fmt.Sprintf(">10%% (%s mods)", shared.FormatNumber(mods))
		}

		rows = append(rows, []any{
			owner + "." + table,
			lastAnalyzed,
			reason,
			shared.FormatNumber(mods),
			shared.FormatNumber(numRows),
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"Table", "Last Analyzed", "Reason", "Mods", "Rows"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120})

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: fmt.Sprintf("统计信息过期的表 (%d 个)\n\n%s\n\n使用 /gather run 自动收集", len(rows), table),
		Summary:  fmt.Sprintf("%d tables with stale stats", len(rows)),
	}, nil
}

func (s *GatherSkill) gatherAll(ctx context.Context) (*skill.Result, error) {
	// First find stale tables.
	result, err := s.driver.Query(ctx, staleStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying stale stats: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "所有表统计信息均为最新，无需收集",
			Summary:  "no stale stats",
		}, nil
	}

	// Gather stats for each table, smallest first (by num_rows).
	type tableEntry struct {
		owner string
		table string
		rows  int
	}
	var tables []tableEntry
	for _, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		tables = append(tables, tableEntry{
			owner: shared.AsString(row[0]),
			table: shared.AsString(row[1]),
			rows:  int(shared.ToFloat64(row[5])),
		})
	}

	var succeeded, failed int
	var details []string
	for _, t := range tables {
		gatherCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := s.driver.Exec(gatherCtx, gatherOneTableSQL, t.owner, t.table)
		cancel()

		if err != nil {
			failed++
			details = append(details, fmt.Sprintf("  FAIL %s.%s: %v", t.owner, t.table, err))
		} else {
			succeeded++
			details = append(details, fmt.Sprintf("  OK   %s.%s (%s rows)", t.owner, t.table, shared.FormatNumber(t.rows)))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "统计信息收集完成: %d 成功, %d 失败\n\n", succeeded, failed)
	for _, d := range details {
		b.WriteString(d + "\n")
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("gathered %d tables, %d failed", succeeded, failed),
	}, nil
}

func (s *GatherSkill) gatherOne(ctx context.Context, fullName string) (*skill.Result, error) {
	parts := strings.SplitN(fullName, ".", 2)
	if len(parts) != 2 {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "格式: /gather run OWNER.TABLE_NAME",
		}, nil
	}
	owner := strings.ToUpper(parts[0])
	table := strings.ToUpper(parts[1])

	gatherCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := s.driver.Exec(gatherCtx, gatherOneTableSQL, owner, table)
	if err != nil {
		return nil, fmt.Errorf("gathering stats for %s.%s: %w", owner, table, err)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("已收集 %s.%s 的统计信息 (no_invalidate=TRUE, cascade=TRUE)", owner, table),
		Summary:  fmt.Sprintf("gathered %s.%s", owner, table),
	}, nil
}
