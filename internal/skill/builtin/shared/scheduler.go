/*-------------------------------------------------------------------------
 *
 * scheduler.go
 *	  SchedulerSkill manages the background scheduler
 *	  (start/stop/status/run/history). It wraps a scheduler.Scheduler
 *	  instance and exposes it as a Skill for the REPL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/scheduler.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/scheduler"
	"github.com/sqlrush/opendb/internal/skill"
)

// SchedulerSkill manages the background scheduler (start/stop/status/run/history).
// It wraps a scheduler.Scheduler instance and exposes it as a Skill for the REPL.
type SchedulerSkill struct {
	sched   *scheduler.Scheduler
	configs []scheduler.TaskConfig
}

// NewSchedulerSkill creates a SchedulerSkill with the given scheduler and task configs.
func NewSchedulerSkill(sched *scheduler.Scheduler, configs []scheduler.TaskConfig) *SchedulerSkill {
	return &SchedulerSkill{
		sched:   sched,
		configs: configs,
	}
}

func (s *SchedulerSkill) Name() string                       { return "scheduler" }
func (s *SchedulerSkill) Description() string                { return "Manage background scheduled tasks (start/stop/status/run/history)" }
func (s *SchedulerSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SchedulerSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "scheduler",
		Description: "Manage background scheduled tasks",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: start, stop, status, run, history",
					"enum":        []string{"start", "stop", "status", "run", "history"},
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Task name (for 'run' action)",
				},
			},
		},
	}
}

func (s *SchedulerSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "scheduler",
		Aliases:        []string{"sched", "cron"},
		Usage:          "/scheduler [start|stop|status|run <task>|history]",
		ArgCompletions: []string{"start", "stop", "status", "run", "history"},
		Examples: []string{
			"/scheduler",
			"/scheduler start",
			"/scheduler stop",
			"/scheduler status",
			"/scheduler run space-watch",
			"/scheduler history",
		},
	}
}

func (s *SchedulerSkill) Validate(params skill.Params) error {
	action := params.StringOr("action", params.StringOr("args", "status"))
	parts := strings.Fields(action)
	if len(parts) == 0 {
		return nil
	}
	switch parts[0] {
	case "start", "stop", "status", "run", "history", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: start, stop, status, run <task>, history", parts[0])
	}
}

func (s *SchedulerSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	raw := params.StringOr("action", params.StringOr("args", "status"))
	if raw == "" {
		raw = "status"
	}
	parts := strings.Fields(raw)
	action := parts[0]

	switch action {
	case "start":
		return s.start()
	case "stop":
		return s.stop()
	case "status":
		return s.status()
	case "run":
		if len(parts) < 2 {
			return &skill.Result{Type: skill.ResultError, Rendered: "用法: /scheduler run <task-name>"}, nil
		}
		return s.run(parts[1])
	case "history":
		return s.history()
	default:
		return &skill.Result{Type: skill.ResultError, Rendered: fmt.Sprintf("未知操作: %s", action)}, nil
	}
}

// ── Scheduler interface for REPL integration ──

// Scheduler returns the underlying scheduler instance.
func (s *SchedulerSkill) Scheduler() *scheduler.Scheduler {
	return s.sched
}

// AutoStart loads tasks and starts the scheduler silently.
func (s *SchedulerSkill) AutoStart() error {
	if s.sched.IsRunning() {
		return nil
	}
	if err := s.sched.LoadTasks(s.configs); err != nil {
		return err
	}
	s.sched.Start()
	return nil
}

// IsRunning returns whether the scheduler is active.
func (s *SchedulerSkill) IsRunning() bool {
	return s.sched.IsRunning()
}

// EventCh returns the scheduler event channel.
func (s *SchedulerSkill) EventCh() <-chan scheduler.TaskEvent {
	return s.sched.EventCh()
}

// StopScheduler stops the scheduler.
func (s *SchedulerSkill) StopScheduler() {
	if s.sched.IsRunning() {
		s.sched.Stop()
	}
}

// ── Actions ──

func (s *SchedulerSkill) start() (*skill.Result, error) {
	if s.sched.IsRunning() {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Scheduler 已在运行中",
			Summary:  "already running",
		}, nil
	}

	if err := s.sched.LoadTasks(s.configs); err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: fmt.Sprintf("加载任务失败: %v", err),
		}, nil
	}

	s.sched.Start()

	tasks := s.sched.Tasks()
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Scheduler 巡检引擎已启动 (%d 个任务)", len(tasks)),
		Summary:  fmt.Sprintf("started with %d tasks", len(tasks)),
	}, nil
}

func (s *SchedulerSkill) stop() (*skill.Result, error) {
	if !s.sched.IsRunning() {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Scheduler 未在运行",
			Summary:  "not running",
		}, nil
	}

	s.sched.Stop()
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: "Scheduler 已停止",
		Summary:  "stopped",
	}, nil
}

// taskDescriptions maps task names to Chinese descriptions.
var taskDescriptions = map[string]string{
	"space-watch":    "表空间使用率巡检",
	"alert-scan":     "告警日志 ORA 错误扫描",
	"stats-check":    "统计信息过期检查",
	"backup-check":   "RMAN 备份状态检查",
	"password-check": "用户密码过期检查",
	"perf-snapshot":  "性能快照采集与对比",
	"index-check":    "索引健康检查",
}

func (s *SchedulerSkill) status() (*skill.Result, error) {
	if !s.sched.IsRunning() {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Scheduler 状态: 未运行\n使用 /scheduler start 启动",
			Summary:  "not running",
		}, nil
	}

	tasks := s.sched.Tasks()

	// Build a map of latest history entries per task.
	histEntries := s.sched.History().Entries()
	lastResult := make(map[string]scheduler.HistoryEntry)
	for _, e := range histEntries {
		lastResult[e.TaskName] = e
	}

	var rows [][]any
	now := time.Now()
	for _, t := range tasks {
		desc := taskDescriptions[t.Config.Name]
		if desc == "" {
			desc = "/" + t.Config.Skill
		}

		nextRun := "-"
		if !t.NextRun.IsZero() {
			remaining := t.NextRun.Sub(now).Round(time.Second)
			if remaining < 0 {
				remaining = 0
			}
			nextRun = fmt.Sprintf("%s后", remaining)
		}

		// Last result from history.
		result := "-"
		if entry, ok := lastResult[t.Config.Name]; ok {
			if entry.Success {
				summary := entry.Summary
				if len(summary) > 30 {
					summary = summary[:27] + "..."
				}
				if summary == "" {
					summary = "OK"
				}
				result = fmt.Sprintf("OK %s (%s)", entry.StartTime.Format("15:04"), entry.Duration)
			} else {
				errMsg := entry.Error
				if len(errMsg) > 30 {
					errMsg = errMsg[:27] + "..."
				}
				result = fmt.Sprintf("FAIL %s", errMsg)
			}
		}

		rows = append(rows, []any{
			t.Config.Name,
			desc,
			t.Schedule.String(),
			nextRun,
			result,
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"Task", "Description", "Schedule", "Next", "Last Result"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120})

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Scheduler 巡检引擎运行中 (%d 个任务)\n\n%s", len(tasks), table),
		Summary:  fmt.Sprintf("running, %d tasks", len(tasks)),
	}, nil
}

func (s *SchedulerSkill) run(taskName string) (*skill.Result, error) {
	if !s.sched.IsRunning() {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "Scheduler 未在运行，先执行 /scheduler start",
		}, nil
	}

	if err := s.sched.RunTask(taskName); err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: fmt.Sprintf("执行失败: %v", err),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("已触发任务 %q (异步执行中)", taskName),
		Summary:  fmt.Sprintf("triggered %s", taskName),
	}, nil
}

func (s *SchedulerSkill) history() (*skill.Result, error) {
	entries := s.sched.History().Recent(20)
	if len(entries) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "暂无执行记录",
			Summary:  "no history",
		}, nil
	}

	var rows [][]any
	for _, e := range entries {
		status := "OK"
		if !e.Success {
			status = "FAIL"
		}
		summary := e.Summary
		if e.Error != "" {
			summary = e.Error
		}
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		rows = append(rows, []any{
			e.StartTime.Format("01-02 15:04:05"),
			e.TaskName,
			e.Duration,
			status,
			summary,
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"Time", "Task", "Duration", "Status", "Summary"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 20, TermWidth: 120})

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Scheduler 执行历史 (最近 %d 条)\n\n%s", len(entries), table),
		Summary:  fmt.Sprintf("%d entries", len(entries)),
	}, nil
}
