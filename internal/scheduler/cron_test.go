/*-------------------------------------------------------------------------
 *
 * cron_test.go
 *	  Test cases for cron.go (scheduler package): TestParseEvery,
 *	  TestParseDaily, TestDailyNext.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/cron_test.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"testing"
	"time"
)

func TestParseEvery(t *testing.T) {
	tests := []struct {
		input    string
		wantDur  time.Duration
		wantErr  bool
	}{
		{"@every 5m", 5 * time.Minute, false},
		{"@every 10s", 10 * time.Second, false},
		{"@every 1h30m", 90 * time.Minute, false},
		{"@every 10m", 10 * time.Minute, false},
		{"@every -1s", 0, true},
		{"@every 0s", 0, true},
		{"@every bad", 0, true},
		{"@unknown", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			sched, err := ParseSchedule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			every, ok := sched.(EverySchedule)
			if !ok {
				t.Fatalf("expected EverySchedule, got %T", sched)
			}
			if every.Interval != tt.wantDur {
				t.Errorf("interval = %v, want %v", every.Interval, tt.wantDur)
			}
			// Next should always return the interval.
			got := sched.Next(time.Now())
			if got != tt.wantDur {
				t.Errorf("Next() = %v, want %v", got, tt.wantDur)
			}
		})
	}
}

func TestParseDaily(t *testing.T) {
	tests := []struct {
		input      string
		wantHour   int
		wantMinute int
		wantErr    bool
	}{
		{"@daily 02:00", 2, 0, false},
		{"@daily 23:30", 23, 30, false},
		{"@daily 00:00", 0, 0, false},
		{"@daily 25:00", 0, 0, true},
		{"@daily 12:60", 0, 0, true},
		{"@daily bad", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			sched, err := ParseSchedule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			daily, ok := sched.(DailySchedule)
			if !ok {
				t.Fatalf("expected DailySchedule, got %T", sched)
			}
			if daily.Hour != tt.wantHour || daily.Minute != tt.wantMinute {
				t.Errorf("got %02d:%02d, want %02d:%02d", daily.Hour, daily.Minute, tt.wantHour, tt.wantMinute)
			}
		})
	}
}

func TestDailyNext(t *testing.T) {
	loc := time.Local

	// If current time is before target, should fire today.
	now := time.Date(2026, 3, 19, 1, 0, 0, 0, loc)
	sched := DailySchedule{Hour: 2, Minute: 0}
	next := sched.Next(now)
	if next != time.Hour {
		t.Errorf("expected 1h, got %v", next)
	}

	// If current time is after target, should fire tomorrow.
	now = time.Date(2026, 3, 19, 3, 0, 0, 0, loc)
	next = sched.Next(now)
	expected := 23 * time.Hour
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}

	// If current time is exactly target, should fire tomorrow.
	now = time.Date(2026, 3, 19, 2, 0, 0, 0, loc)
	next = sched.Next(now)
	if next != 24*time.Hour {
		t.Errorf("expected 24h, got %v", next)
	}
}

func TestScheduleString(t *testing.T) {
	every := EverySchedule{Interval: 5 * time.Minute}
	if s := every.String(); s != "@every 5m0s" {
		t.Errorf("EverySchedule.String() = %q", s)
	}

	daily := DailySchedule{Hour: 2, Minute: 30}
	if s := daily.String(); s != "@daily 02:30" {
		t.Errorf("DailySchedule.String() = %q", s)
	}
}
