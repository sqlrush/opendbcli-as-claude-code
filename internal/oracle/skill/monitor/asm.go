/*-------------------------------------------------------------------------
 *
 * asm.go
 *	  ASMSkill shows ASM disk group status and usage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/asm.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const asmDiskgroupSQL = `SELECT name, state, type,
       ROUND(total_mb/1024) AS total_gb,
       ROUND(free_mb/1024) AS free_gb,
       ROUND((total_mb - free_mb) / NULLIF(total_mb, 0) * 100, 1) AS used_pct
FROM v$asm_diskgroup
ORDER BY name`

// ASMSkill shows ASM disk group status and usage.
type ASMSkill struct {
	driver db.Driver
}

// NewASMSkill creates an ASMSkill backed by the given driver.
func NewASMSkill(driver db.Driver) *ASMSkill {
	return &ASMSkill{driver: driver}
}

func (s *ASMSkill) Name() string                      { return "asm" }
func (s *ASMSkill) Description() string               { return "Show ASM disk group status and usage" }
func (s *ASMSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ASMSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "asm",
		Description: "Show ASM disk group status and usage",
	}
}

func (s *ASMSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "asm",
		Usage:   "/asm",
	}
}

func (s *ASMSkill) Validate(_ skill.Params) error { return nil }

func (s *ASMSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, asmDiskgroupSQL)
	if err != nil {
		// Non-ASM databases will error (e.g., ORA-02003)
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "此数据库未使用ASM存储.",
			Summary:  "此数据库未使用ASM存储",
		}, nil
	}

	if len(qr.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "此数据库未使用ASM存储.",
			Summary:  "此数据库未使用ASM存储",
		}, nil
	}

	const warningThreshold = 80.0
	warnings := make([]string, 0)
	for _, row := range qr.Rows {
		name := rowStr(row, 0)
		usedPct := rowFloat(row, 5)
		if usedPct > warningThreshold {
			warnings = append(warnings,
				fmt.Sprintf("%s 使用率 %.1f%% > %.0f%% 预警线", name, usedPct, warningThreshold))
		}
	}

	summary := fmt.Sprintf("%d ASM disk groups", len(qr.Rows))
	for _, w := range warnings {
		summary += "\n" + w
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     qr,
		Rendered: fmt.Sprintf("ASM 磁盘组 — %d 个", len(qr.Rows)),
		Summary:  summary,
	}, nil
}
