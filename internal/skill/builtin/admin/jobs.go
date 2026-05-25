/*-------------------------------------------------------------------------
 *
 * jobs.go
 *	  JobsSkill shows scheduler jobs and optionally failed job details.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/jobs.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const jobsAllSQL = `SELECT owner, job_name, state, enabled,
       TO_CHAR(last_start_date, 'MM-DD HH24:MI:SS') AS last_run,
       TO_CHAR(next_run_date, 'MM-DD HH24:MI:SS') AS next_run,
       run_count, failure_count
FROM dba_scheduler_jobs
WHERE owner NOT IN ('SYS','SYSTEM','ORACLE_OCM','EXFSYS','DBSFWUSER')
ORDER BY last_start_date DESC NULLS LAST
FETCH FIRST 30 ROWS ONLY`

const jobsFailedSQL = `SELECT d.owner, d.job_name, d.status,
       TO_CHAR(d.actual_start_date, 'MM-DD HH24:MI:SS') AS start_time,
       d.run_duration,
       d.error# AS error_code,
       SUBSTR(d.additional_info, 1, 100) AS info
FROM dba_scheduler_job_run_details d
WHERE d.status = 'FAILED'
AND d.actual_start_date > SYSDATE - 7
ORDER BY d.actual_start_date DESC
FETCH FIRST 30 ROWS ONLY`

// JobsSkill shows scheduler jobs and optionally failed job details.
type JobsSkill struct {
	driver db.Driver
}

// NewJobsSkill creates a JobsSkill backed by the given driver.
func NewJobsSkill(driver db.Driver) *JobsSkill {
	return &JobsSkill{driver: driver}
}

func (s *JobsSkill) Name() string        { return "jobs" }
func (s *JobsSkill) Description() string { return "Show scheduler jobs (use 'failed' for failures)" }
func (s *JobsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *JobsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "jobs",
		Description: "Show scheduler jobs (use 'failed' for failures)",
	}
}

func (s *JobsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "jobs",
		Usage:          "/jobs [failed]",
		Examples:       []string{"/jobs", "/jobs failed"},
		ArgCompletions: []string{"failed"},
	}
}

func (s *JobsSkill) Validate(_ skill.Params) error { return nil }

func (s *JobsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	query := jobsAllSQL
	summary := "scheduler jobs"
	if isFailedArg(args) {
		query = jobsFailedSQL
		summary = "failed jobs (7 days)"
	}

	result, err := s.driver.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", summary, err)
	}

	if len(result.Rows) == 0 {
		msg := "无调度作业"
		if isFailedArg(args) {
			msg = "最近 7 天无失败作业"
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: msg,
			Summary:  msg,
		}, nil
	}
	rendered := fmt.Sprintf("调度作业 — %d 个", len(result.Rows))
	if isFailedArg(args) {
		rendered = fmt.Sprintf("失败作业 (7 天) — %d 个", len(result.Rows))
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d 个%s", len(result.Rows), summary),
	}, nil
}

// isFailedArg checks whether the argument indicates a failed-jobs query.
func isFailedArg(args string) bool {
	lower := strings.ToLower(args)
	return strings.HasPrefix(lower, "fail")
}
