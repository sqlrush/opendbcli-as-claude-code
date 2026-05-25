/*-------------------------------------------------------------------------
 *
 * indexhealth.go
 *	  IndexHealthSkill checks for UNUSABLE, fragmented, and unused
 *	  indexes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/indexhealth.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
)

// SQL to find problematic indexes.
const indexHealthSQL = `SELECT owner, index_name, table_name, status, blevel,
       ROUND(leaf_blocks * 8 / 1024, 1) AS size_mb,
       NVL(TO_CHAR(last_analyzed, 'MM-DD HH24:MI'), 'NEVER') AS last_analyzed
FROM dba_indexes
WHERE owner NOT IN ('SYS','SYSTEM','DBSNMP','OUTLN','APPQOSSYS','WMSYS','CTXSYS','XDB','ORDDATA','MDSYS','OLAPSYS','ORDSYS','GSMADMIN_INTERNAL','LBACSYS','DVSYS','AUDSYS')
  AND (status IN ('UNUSABLE', 'N/A') OR blevel > 4)
ORDER BY
  CASE WHEN status = 'UNUSABLE' THEN 0
       WHEN status = 'N/A' THEN 1
       ELSE 2 END,
  blevel DESC
FETCH FIRST 50 ROWS ONLY`

// SQL to find indexes that have never been used (since last stats reset).
const unusedIndexSQL = `SELECT i.owner, i.index_name, i.table_name,
       ROUND(i.leaf_blocks * 8 / 1024, 1) AS size_mb,
       NVL(TO_CHAR(i.last_analyzed, 'MM-DD HH24:MI'), 'NEVER') AS last_analyzed
FROM dba_indexes i
LEFT JOIN v$object_usage u ON i.index_name = u.index_name
WHERE i.owner NOT IN ('SYS','SYSTEM','DBSNMP','OUTLN','APPQOSSYS','WMSYS','CTXSYS','XDB','ORDDATA','MDSYS','OLAPSYS','ORDSYS','GSMADMIN_INTERNAL','LBACSYS','DVSYS','AUDSYS')
  AND i.status = 'VALID'
  AND i.index_type NOT IN ('LOB')
  AND i.uniqueness = 'NONUNIQUE'
  AND (u.used IS NULL OR u.used = 'NO')
  AND i.leaf_blocks > 128
ORDER BY i.leaf_blocks DESC
FETCH FIRST 20 ROWS ONLY`

// IndexHealthSkill checks for UNUSABLE, fragmented, and unused indexes.
type IndexHealthSkill struct {
	driver db.Driver
}

// NewIndexHealthSkill creates an IndexHealthSkill backed by the given driver.
func NewIndexHealthSkill(driver db.Driver) *IndexHealthSkill {
	return &IndexHealthSkill{driver: driver}
}

func (s *IndexHealthSkill) Name() string                       { return "indexhealth" }
func (s *IndexHealthSkill) Description() string                { return "Index health check (unusable/fragmented/unused)" }
func (s *IndexHealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *IndexHealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "indexhealth",
		Description: "Check index health: unusable, fragmented (blevel>4), and unused indexes",
	}
}

func (s *IndexHealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "indexhealth",
		Aliases:  []string{"idxhealth"},
		Usage:    "/indexhealth",
		Examples: []string{"/indexhealth"},
	}
}

func (s *IndexHealthSkill) Validate(_ skill.Params) error { return nil }

func (s *IndexHealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	var b strings.Builder

	// 1. Unusable / fragmented indexes.
	problemResult, err := s.driver.Query(ctx, indexHealthSQL)
	if err != nil {
		return nil, fmt.Errorf("querying index health: %w", err)
	}

	if len(problemResult.Rows) > 0 {
		var rows [][]any
		for _, row := range problemResult.Rows {
			if len(row) < 7 {
				continue
			}
			owner := shared.AsString(row[0])
			name := shared.AsString(row[1])
			table := shared.AsString(row[2])
			status := shared.AsString(row[3])
			blevel := int(shared.ToFloat64(row[4]))
			sizeMB := shared.ToFloat64(row[5])

			issue := status
			action := ""
			if status == "UNUSABLE" {
				issue = "UNUSABLE"
				action = fmt.Sprintf("ALTER INDEX %s.%s REBUILD ONLINE", owner, name)
			} else if blevel > 4 {
				issue = fmt.Sprintf("blevel=%d", blevel)
				action = fmt.Sprintf("ALTER INDEX %s.%s REBUILD ONLINE", owner, name)
			}

			rows = append(rows, []any{
				owner + "." + name,
				table,
				issue,
				fmt.Sprintf("%.1f MB", sizeMB),
				action,
			})
		}

		qr := &db.QueryResult{
			Columns: []string{"Index", "Table", "Issue", "Size", "Action"},
			Rows:    rows,
		}
		b.WriteString(fmt.Sprintf("问题索引 (%d 个)\n\n", len(rows)))
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120}))
	} else {
		b.WriteString("无 UNUSABLE 或碎片化索引\n")
	}

	// 2. Unused indexes (candidate for deletion).
	unusedResult, err := s.driver.Query(ctx, unusedIndexSQL)
	if err != nil {
		// Non-fatal: v$object_usage may not be available.
		b.WriteString("\n(未使用索引检查跳过: " + err.Error() + ")")
	} else if len(unusedResult.Rows) > 0 {
		var rows [][]any
		for _, row := range unusedResult.Rows {
			if len(row) < 5 {
				continue
			}
			owner := shared.AsString(row[0])
			name := shared.AsString(row[1])
			table := shared.AsString(row[2])
			sizeMB := shared.ToFloat64(row[3])

			rows = append(rows, []any{
				owner + "." + name,
				table,
				fmt.Sprintf("%.1f MB", sizeMB),
			})
		}

		qr := &db.QueryResult{
			Columns: []string{"Index", "Table", "Size"},
			Rows:    rows,
		}
		b.WriteString(fmt.Sprintf("\n未使用索引 — 候选删除 (%d 个, >1 MB)\n\n", len(rows)))
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 20, TermWidth: 120}))
	} else {
		b.WriteString("\n无大型未使用索引\n")
	}

	totalIssues := len(problemResult.Rows)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     problemResult,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("索引健康: %d 个问题", totalIssues),
	}, nil
}
