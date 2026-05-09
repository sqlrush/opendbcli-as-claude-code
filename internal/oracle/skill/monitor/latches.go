/*-------------------------------------------------------------------------
 *
 * latches.go
 *	  LatchesSkill shows latch contention.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/latches.go
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

const latchesSQL = `SELECT name, gets, misses, sleeps, spin_gets,
       ROUND(misses/NULLIF(gets,0)*100, 2) as miss_ratio
FROM v$latch
WHERE misses > 0
ORDER BY sleeps DESC
FETCH FIRST 30 ROWS ONLY`

// LatchesSkill shows latch contention.
type LatchesSkill struct {
	driver db.Driver
}

// NewLatchesSkill creates a LatchesSkill backed by the given driver.
func NewLatchesSkill(driver db.Driver) *LatchesSkill {
	return &LatchesSkill{driver: driver}
}

func (s *LatchesSkill) Name() string        { return "latches" }
func (s *LatchesSkill) Description() string  { return "Show latch contention" }
func (s *LatchesSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *LatchesSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "latches",
		Description: "Show latch contention",
	}
}

func (s *LatchesSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "latches",
		Usage:   "/latches",
	}
}

func (s *LatchesSkill) Validate(_ skill.Params) error { return nil }

func (s *LatchesSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, latchesSQL)
	if err != nil {
		return nil, fmt.Errorf("querying latches: %w", err)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("Latch 争用 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("Latch 争用 Top 30 — %d 个", len(result.Rows)),
	}, nil
}
