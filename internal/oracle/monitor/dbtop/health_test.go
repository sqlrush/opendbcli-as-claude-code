/*-------------------------------------------------------------------------
 *
 * health_test.go
 *	  Test cases for health.go (dbtop package):
 *	  TestEvaluateHealth_Healthy, TestEvaluateHealth_Warning_AN,
 *	  TestEvaluateHealth_Critical_AN.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/health_test.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "testing"

func TestEvaluateHealth_Healthy(t *testing.T) {
	s := &Snapshot{
		ActiveCount: 10, DBPercent: 20, WTRPercent: 15,
		HasDelta: true,
		Events:   []WaitEvent{{PCT: 20}},
		Sessions: []SessionRow{{ElapsedSec: 100}},
	}
	EvaluateHealth(s, 0)
	if s.Health != Healthy {
		t.Errorf("Health = %v, want Healthy", s.Health)
	}
	if len(s.Alerts) != 0 {
		t.Errorf("Alerts = %v, want empty", s.Alerts)
	}
}

func TestEvaluateHealth_Warning_AN(t *testing.T) {
	s := &Snapshot{ActiveCount: 50, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (AN=50)", s.Health)
	}
}

func TestEvaluateHealth_Critical_AN(t *testing.T) {
	s := &Snapshot{ActiveCount: 100, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (AN=100)", s.Health)
	}
}

func TestEvaluateHealth_Warning_DBPercent(t *testing.T) {
	s := &Snapshot{DBPercent: 60, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (db%%=60)", s.Health)
	}
}

func TestEvaluateHealth_Critical_WTR(t *testing.T) {
	s := &Snapshot{WTRPercent: 70, HasDelta: true}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (WTR%%=70)", s.Health)
	}
}

func TestEvaluateHealth_Warning_EventPCT(t *testing.T) {
	s := &Snapshot{
		HasDelta: true,
		Events:   []WaitEvent{{Event: "log file sync", PCT: 40}},
	}
	EvaluateHealth(s, 0)
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (event PCT=40)", s.Health)
	}
}

func TestEvaluateHealth_Critical_SessionET(t *testing.T) {
	s := &Snapshot{
		HasDelta: true,
		Sessions: []SessionRow{{ElapsedSec: 700, SID: 142}},
	}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (E/T=700)", s.Health)
	}
}

func TestEvaluateHealth_Warning_TPSDrop(t *testing.T) {
	s := &Snapshot{TPS: 40, HasDelta: true}
	EvaluateHealth(s, 100) // prevTPS=100, current=40, drop=60%
	if s.Health != Warning {
		t.Errorf("Health = %v, want Warning (TPS drop 60%%)", s.Health)
	}
}

func TestEvaluateHealth_Critical_TPSDrop(t *testing.T) {
	s := &Snapshot{TPS: 10, HasDelta: true}
	EvaluateHealth(s, 100) // drop=90%
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (TPS drop 90%%)", s.Health)
	}
}

func TestEvaluateHealth_NoDelta_ANStillChecked(t *testing.T) {
	s := &Snapshot{ActiveCount: 200, HasDelta: false}
	EvaluateHealth(s, 0)
	if s.Health != Critical {
		t.Errorf("Health = %v, want Critical (AN=200 even without delta)", s.Health)
	}
}
