/*-------------------------------------------------------------------------
 *
 * scheduler.go
 *	  Scheduler is the background task coordinator. It manages a set of
 *	  scheduled tasks, each backed by an existing skill. Thread-safe:
 *	  all state changes are protected by mu.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/scheduler.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/skill"
)

// Scheduler is the background task coordinator.
// It manages a set of scheduled tasks, each backed by an existing skill.
// Thread-safe: all state changes are protected by mu.
type Scheduler struct {
	mu       sync.Mutex
	tasks    []*Task
	executor *skill.Executor
	history  *History
	eventCh  chan TaskEvent
	stopCh   chan struct{}
	stopped  chan struct{}
	running  bool
}

// New creates a Scheduler with the given executor and data directory.
// dataDir is typically ~/.opendb/scheduler/.
func New(executor *skill.Executor, dataDir string) *Scheduler {
	return &Scheduler{
		executor: executor,
		history:  NewHistory(dataDir),
		eventCh:  make(chan TaskEvent, 16),
	}
}

// LoadTasks parses task configs and registers them.
// Replaces any previously loaded tasks. Safe to call while running (for reload).
func (s *Scheduler) LoadTasks(configs []TaskConfig) error {
	var tasks []*Task
	for _, cfg := range configs {
		if !cfg.IsEnabled() {
			continue
		}
		sched, err := ParseSchedule(cfg.Schedule)
		if err != nil {
			return fmt.Errorf("task %q: %w", cfg.Name, err)
		}
		tasks = append(tasks, &Task{
			Config:   cfg,
			Schedule: sched,
		})
	}

	s.mu.Lock()
	s.tasks = tasks
	s.mu.Unlock()
	return nil
}

// Start begins the scheduler loop in a background goroutine.
// Tasks fire according to their schedules.
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.stopped = make(chan struct{})

	// Calculate initial next-run times.
	now := time.Now()
	for _, t := range s.tasks {
		t.NextRun = now.Add(t.Schedule.Next(now))
	}
	s.mu.Unlock()

	go s.loop()
}

// Stop gracefully shuts down the scheduler and waits for completion.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	close(s.stopCh)
	<-s.stopped
}

// IsRunning returns whether the scheduler loop is active.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// EventCh returns the channel that receives task completion events.
// Returns nil if scheduler is not running.
func (s *Scheduler) EventCh() <-chan TaskEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return nil
	}
	return s.eventCh
}

// RunTask manually triggers a task by name (non-blocking, runs in goroutine).
func (s *Scheduler) RunTask(name string) error {
	s.mu.Lock()
	var target *Task
	for _, t := range s.tasks {
		if t.Config.Name == name {
			target = t
			break
		}
	}
	s.mu.Unlock()

	if target == nil {
		return fmt.Errorf("task %q not found", name)
	}

	go s.executeTask(target)
	return nil
}

// Tasks returns a snapshot of all registered tasks.
func (s *Scheduler) Tasks() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Task, len(s.tasks))
	for i, t := range s.tasks {
		result[i] = *t
	}
	return result
}

// History returns the execution history.
func (s *Scheduler) History() *History {
	return s.history
}

// ── Main loop ──────────────────────────────────────────────────────────────

func (s *Scheduler) loop() {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		close(s.stopped)
	}()

	// Check every second for tasks that need to fire.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case now := <-ticker.C:
			s.checkAndFire(now)
		}
	}
}

func (s *Scheduler) checkAndFire(now time.Time) {
	s.mu.Lock()
	var due []*Task
	for _, t := range s.tasks {
		if !t.NextRun.IsZero() && !now.Before(t.NextRun) {
			due = append(due, t)
			// Reschedule immediately to prevent re-firing.
			t.NextRun = now.Add(t.Schedule.Next(now))
		}
	}
	s.mu.Unlock()

	for _, t := range due {
		go s.executeTask(t)
	}
}

func (s *Scheduler) executeTask(t *Task) {
	start := time.Now()

	// Build params from task config.
	params := make(map[string]any)
	for k, v := range t.Config.Params {
		params[k] = v
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := s.executor.Execute(ctx, t.Config.Skill, skill.ParamsFromMap(params))

	dur := time.Since(start)
	event := TaskEvent{
		TaskName:  t.Config.Name,
		Skill:     t.Config.Skill,
		StartTime: start,
		Duration:  dur,
		Success:   err == nil,
	}

	if err != nil {
		event.Error = err.Error()
	} else if result != nil {
		event.Summary = result.Summary
	}

	// Update last run time.
	s.mu.Lock()
	t.LastRun = start
	s.mu.Unlock()

	// Record in history.
	s.history.Add(HistoryEntry{
		TaskName:  t.Config.Name,
		Skill:     t.Config.Skill,
		StartTime: start,
		Duration:  dur.Round(time.Millisecond).String(),
		Success:   event.Success,
		Summary:   event.Summary,
		Error:     event.Error,
	})

	// Non-blocking send to REPL.
	select {
	case s.eventCh <- event:
	default:
	}
}
