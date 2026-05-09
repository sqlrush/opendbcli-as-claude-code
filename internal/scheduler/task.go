/*-------------------------------------------------------------------------
 *
 * task.go
 *	  TaskConfig is the configuration for a single scheduled task (from
 *	  config.yaml).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/task.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import "time"

// TaskConfig is the configuration for a single scheduled task (from config.yaml).
type TaskConfig struct {
	Name     string            `yaml:"name"`
	Schedule string            `yaml:"schedule"`
	Skill    string            `yaml:"skill"`
	Params   map[string]any    `yaml:"params,omitempty"`
	OnWarn   string            `yaml:"on_warn"`   // "notify" | "auto_fix"
	Enabled  *bool             `yaml:"enabled"`   // nil = true (default enabled)
}

// IsEnabled returns whether the task is enabled (default: true).
func (tc TaskConfig) IsEnabled() bool {
	if tc.Enabled == nil {
		return true
	}
	return *tc.Enabled
}

// Task is a runtime scheduled task with parsed schedule.
type Task struct {
	Config   TaskConfig
	Schedule Schedule
	LastRun  time.Time
	NextRun  time.Time
}

// TaskEvent is emitted to the REPL when a scheduled task completes.
// Immutable: created once and sent through channel.
type TaskEvent struct {
	TaskName  string
	Skill     string
	StartTime time.Time
	Duration  time.Duration
	Success   bool
	Summary   string // short result summary for display
	Warnings  []string
	Error     string
}
