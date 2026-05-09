/*-------------------------------------------------------------------------
 *
 * jobs.go
 *	  JobsSkill shows event scheduler jobs.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/admin/jobs.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const jobsSQL = `SELECT
  EVENT_SCHEMA,
  EVENT_NAME,
  STATUS,
  EVENT_TYPE,
  INTERVAL_VALUE,
  INTERVAL_FIELD,
  LAST_EXECUTED
FROM information_schema.EVENTS
ORDER BY LAST_EXECUTED DESC`

// JobsSkill shows event scheduler jobs.
type JobsSkill struct {
	driver db.Driver
}

// NewJobsSkill creates a JobsSkill backed by the given driver.
func NewJobsSkill(driver db.Driver) *JobsSkill {
	return &JobsSkill{driver: driver}
}

func (s *JobsSkill) Name() string                       { return "jobs" }
func (s *JobsSkill) Description() string                { return "定时任务列表" }
func (s *JobsSkill) Category() string                   { return "admin" }
func (s *JobsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *JobsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/jobs", Examples: []string{"/jobs"}}
}

func (s *JobsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "jobs",
		Description: "List MySQL event scheduler jobs with status and schedule",
	}
}

func (s *JobsSkill) Validate(_ skill.Params) error { return nil }

func (s *JobsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, jobsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无定时任务 (event scheduler)",
			Summary:  "no events",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("定时任务 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个定时任务", len(result.Rows)),
	}, nil
}
