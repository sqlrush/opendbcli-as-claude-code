/*-------------------------------------------------------------------------
 *
 * wdr.go
 *	  WDRSkill lists WDR snapshots and guides users into comparison
 *	  reports. Generating a full WDR diff is expensive and better done
 *	  on the server; this skill keeps the OpenDB side focused on
 *	  discovery + instructions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/wdr.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// wdrSnapListSQL lists recent WDR snapshots from the snapshot schema. OG's
// WDR (Workload Diagnosis Report) is the equivalent of Oracle AWR.
const wdrSnapListSQL = `SELECT
  snapshot_id,
  start_ts::text,
  end_ts::text,
  EXTRACT(EPOCH FROM end_ts - start_ts)::int AS window_sec
FROM snapshot.snapshot
ORDER BY snapshot_id DESC
LIMIT 20`

// WDRSkill lists WDR snapshots and guides users into comparison reports.
// Generating a full WDR diff is expensive and better done on the server;
// this skill keeps the OpenDB side focused on discovery + instructions.
type WDRSkill struct{ driver db.Driver }

// NewWDRSkill creates a WDRSkill.
func NewWDRSkill(driver db.Driver) *WDRSkill { return &WDRSkill{driver: driver} }

func (s *WDRSkill) Name() string                       { return "wdr" }
func (s *WDRSkill) Description() string                { return "WDR 快照列表与生成指引" }
func (s *WDRSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *WDRSkill) Validate(_ skill.Params) error      { return nil }
func (s *WDRSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/wdr"} }

func (s *WDRSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "wdr",
		Description: "List recent WDR snapshots (OpenGauss Workload Diagnosis Report, analogous to Oracle AWR)",
	}
}

func (s *WDRSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, wdrSnapListSQL)
	if err != nil {
		// WDR may be disabled (enable_wdr_snapshot=off) or snapshot schema
		// missing — tell the user rather than fail silently.
		msg := fmt.Sprintf("读取 WDR 快照失败: %v\n提示: 需设置 enable_wdr_snapshot=on 并等 wdr_snapshot_interval 生成首个快照。", err)
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: msg,
			Summary:  "WDR unavailable",
		}, nil
	}

	guide := "\n生成对比报告: SELECT generate_wdr_report(<begin_snap>, <end_snap>, 1, 'all', 'cluster');"
	rows := 0
	if result != nil {
		rows = len(result.Rows)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("WDR 快照 — %d 条%s", rows, guide),
		Summary:  fmt.Sprintf("%d WDR snapshots", rows),
	}, nil
}
