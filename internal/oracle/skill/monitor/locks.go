/*-------------------------------------------------------------------------
 *
 * locks.go
 *	  LocksSkill shows row and table locks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/locks.go
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

const locksSQL = `SELECT l.sid, l.type,
       DECODE(l.lmode, 0,'None', 1,'Null', 2,'Row-S', 3,'Row-X', 4,'Share', 5,'SSX', 6,'Excl') AS lock_mode,
       l.block, s.username, o.object_name,
       s.blocking_session AS blocker, s.seconds_in_wait AS wait_sec
FROM v$lock l
JOIN v$session s ON l.sid = s.sid
LEFT JOIN dba_objects o ON l.id1 = o.object_id AND l.type = 'TM'
WHERE l.type IN ('TX','TM')
ORDER BY l.block DESC, l.sid`

// LocksSkill shows row and table locks.
type LocksSkill struct {
	driver db.Driver
}

// NewLocksSkill creates a LocksSkill backed by the given driver.
func NewLocksSkill(driver db.Driver) *LocksSkill {
	return &LocksSkill{driver: driver}
}

func (s *LocksSkill) Name() string        { return "locks" }
func (s *LocksSkill) Description() string  { return "Show row and table locks" }
func (s *LocksSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *LocksSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "locks",
		Description: "Show row and table locks",
	}
}

func (s *LocksSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "locks",
		Usage:   "/locks",
	}
}

func (s *LocksSkill) Validate(_ skill.Params) error { return nil }

func (s *LocksSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, locksSQL)
	if err != nil {
		return nil, fmt.Errorf("querying locks: %w", err)
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无锁等待",
			Summary:  "0 locks",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("锁信息 (当前快照) — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("锁等待 (TX/TM) — %d 个", len(result.Rows)),
	}, nil
}
