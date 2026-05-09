/*-------------------------------------------------------------------------
 *
 * cron.go
 *	  Schedule determines when a task should next fire.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/cron.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"fmt"
	"strings"
	"time"
)

// Schedule determines when a task should next fire.
type Schedule interface {
	// Next returns the duration until the next firing, relative to now.
	Next(now time.Time) time.Duration
	// String returns a human-readable description.
	String() string
}

// ============================================================================
// @every — fixed-interval schedule
// ============================================================================

// EverySchedule fires at a fixed interval (e.g. @every 5m).
type EverySchedule struct {
	Interval time.Duration
}

func (s EverySchedule) Next(_ time.Time) time.Duration {
	return s.Interval
}

func (s EverySchedule) String() string {
	return fmt.Sprintf("@every %s", s.Interval)
}

// ============================================================================
// @daily — fire once per day at HH:MM
// ============================================================================

// DailySchedule fires once per day at a specific time (e.g. @daily 02:00).
type DailySchedule struct {
	Hour   int
	Minute int
}

func (s DailySchedule) Next(now time.Time) time.Duration {
	target := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, now.Location())
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	return target.Sub(now)
}

func (s DailySchedule) String() string {
	return fmt.Sprintf("@daily %02d:%02d", s.Hour, s.Minute)
}

// ============================================================================
// Parser
// ============================================================================

// ParseSchedule parses a schedule string into a Schedule.
// Supported formats:
//   - "@every <duration>"  e.g. "@every 5m", "@every 10s", "@every 1h30m"
//   - "@daily HH:MM"       e.g. "@daily 02:00", "@daily 23:30"
func ParseSchedule(s string) (Schedule, error) {
	s = strings.TrimSpace(s)

	switch {
	case strings.HasPrefix(s, "@every "):
		return parseEvery(s[7:])
	case strings.HasPrefix(s, "@daily "):
		return parseDaily(s[7:])
	default:
		return nil, fmt.Errorf("unknown schedule format %q (use @every or @daily)", s)
	}
}

func parseEvery(raw string) (Schedule, error) {
	raw = strings.TrimSpace(raw)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid @every duration %q: %w", raw, err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("@every duration must be positive, got %s", d)
	}
	return EverySchedule{Interval: d}, nil
}

func parseDaily(raw string) (Schedule, error) {
	raw = strings.TrimSpace(raw)
	var hour, minute int
	n, err := fmt.Sscanf(raw, "%d:%d", &hour, &minute)
	if err != nil || n != 2 {
		return nil, fmt.Errorf("invalid @daily time %q (expected HH:MM)", raw)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return nil, fmt.Errorf("invalid @daily time %q (hour 0-23, minute 0-59)", raw)
	}
	return DailySchedule{Hour: hour, Minute: minute}, nil
}
