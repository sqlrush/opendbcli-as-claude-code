/*-------------------------------------------------------------------------
 *
 * mutexes.go
 *	  MutexesSkill shows mutex contention.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/mutexes.go
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

const mutexesSQL = `SELECT mutex_type, location, sleeps, wait_time
FROM v$mutex_sleep
WHERE sleeps > 0
ORDER BY sleeps DESC
FETCH FIRST 30 ROWS ONLY`

// MutexesSkill shows mutex contention.
type MutexesSkill struct {
	driver db.Driver
}

// NewMutexesSkill creates a MutexesSkill backed by the given driver.
func NewMutexesSkill(driver db.Driver) *MutexesSkill {
	return &MutexesSkill{driver: driver}
}

func (s *MutexesSkill) Name() string        { return "mutexes" }
func (s *MutexesSkill) Description() string  { return "Show mutex contention" }
func (s *MutexesSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *MutexesSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "mutexes",
		Description: "Show mutex contention",
	}
}

func (s *MutexesSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "mutexes",
		Usage:   "/mutexes",
	}
}

func (s *MutexesSkill) Validate(_ skill.Params) error { return nil }

func (s *MutexesSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, mutexesSQL)
	if err != nil {
		return nil, fmt.Errorf("querying mutexes: %w", err)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("Mutex 争用 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("Mutex 争用 — %d 个", len(result.Rows)),
	}, nil
}
