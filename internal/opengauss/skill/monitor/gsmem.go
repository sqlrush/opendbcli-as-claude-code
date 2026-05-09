/*-------------------------------------------------------------------------
 *
 * gsmem.go
 *	  GSMemSkill shows OpenGauss memory in three panels: engine total
 *	  (mirrors Oracle SGA), shared_buffers config + hit ratio (mirrors
 *	  Oracle buffer cache), and by-type breakdown. `/sharedbufs` was
 *	  merged in here.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/gsmem.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// gsTotalMemSQL is OG's engine-wide memory breakdown (shared / process /
// reserved / used). This is the closest OG has to Oracle SGA + PGA as a
// single picture.
const gsTotalMemSQL = `SELECT memorytype, memorymbytes
FROM gs_total_memory_detail
ORDER BY memorymbytes DESC`

// gsSharedBufSettingSQL reads the shared_buffers setting and current cache
// hit ratio — the old /sharedbufs skill data, folded in here after the merge.
const gsSharedBufSettingSQL = `SELECT
  setting,
  unit,
  pg_size_pretty(setting::bigint * 8192) AS shared_buffers_pretty
FROM pg_settings
WHERE name = 'shared_buffers'`

const gsBufferHitSQL = `SELECT
  sum(blks_hit) AS hits,
  sum(blks_read) AS reads,
  CASE WHEN sum(blks_hit) + sum(blks_read) > 0
       THEN ROUND(100.0 * sum(blks_hit) / (sum(blks_hit) + sum(blks_read)), 2)
       ELSE 0 END AS hit_ratio
FROM pg_stat_database`

// GSMemSkill shows OpenGauss memory in three panels: engine total (mirrors
// Oracle SGA), shared_buffers config + hit ratio (mirrors Oracle buffer
// cache), and by-type breakdown. `/sharedbufs` was merged in here.
type GSMemSkill struct{ driver db.Driver }

// NewGSMemSkill creates a GSMemSkill.
func NewGSMemSkill(driver db.Driver) *GSMemSkill { return &GSMemSkill{driver: driver} }

func (s *GSMemSkill) Name() string                       { return "gsmem" }
func (s *GSMemSkill) Description() string                { return "内存详情 (引擎总量 + shared_buffers + 命中率)" }
func (s *GSMemSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *GSMemSkill) Validate(_ skill.Params) error      { return nil }
func (s *GSMemSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/gsmem"} }

func (s *GSMemSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "gsmem",
		Description: "Show OpenGauss memory: engine-wide total, shared_buffers, cache hit ratio, by-type breakdown",
	}
}

func (s *GSMemSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Section 1: shared_buffers setting + cache hit ratio (ex-/sharedbufs).
	if setting, err := s.driver.Query(ctx, gsSharedBufSettingSQL); err == nil && len(setting.Rows) > 0 {
		lines := []string{
			fmt.Sprintf("shared_buffers : %v %v (%v)",
				setting.Rows[0][0], setting.Rows[0][1], setting.Rows[0][2]),
		}
		if hit, err := s.driver.Query(ctx, gsBufferHitSQL); err == nil && len(hit.Rows) > 0 {
			row := hit.Rows[0]
			lines = append(lines,
				fmt.Sprintf("blks_hit       : %v", row[0]),
				fmt.Sprintf("blks_read      : %v", row[1]),
				fmt.Sprintf("hit_ratio      : %v %%", row[2]),
			)
		}
		sections = append(sections, format.PanelSection{Header: "Shared Buffers", Lines: lines})
	}

	// Section 2: engine memory breakdown from gs_total_memory_detail.
	totals, err := s.driver.Query(ctx, gsTotalMemSQL)
	if err != nil {
		// Engine view may be gated — not a fatal error.
		warn := "gs_total_memory_detail 不可用: " + err.Error()
		sections = append(sections, format.PanelSection{Header: "Engine Memory", Lines: []string{"\033[33m⚠\033[0m " + warn}})
	} else {
		lines := make([]string, 0, len(totals.Rows))
		for _, row := range totals.Rows {
			lines = append(lines, fmt.Sprintf("%-30s : %v MB", row[0], row[1]))
		}
		sections = append(sections, format.PanelSection{Header: "Engine Memory (gs_total_memory_detail)", Lines: lines})
	}

	rendered := format.Panel("OpenGauss Memory", sections)
	count := 0
	if totals != nil {
		count = len(totals.Rows)
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("shared_buffers + %d memory categories", count),
	}, nil
}
