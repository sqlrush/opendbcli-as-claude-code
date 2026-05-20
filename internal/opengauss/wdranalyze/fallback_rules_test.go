/*-------------------------------------------------------------------------
 *
 * fallback_rules_test.go
 *	  Unit tests for the 5 fallback rules. Each rule has a "triggers"
 *	  and "doesn't trigger" case so we know the threshold is bracketed.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/fallback_rules_test.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import "testing"

// ── Rule 1: autovacuum off ─────────────────────────────────────────────

func TestFallback_AutovacuumOff_Triggers(t *testing.T) {
	r := &WDRReport{Settings: map[string]string{"autovacuum": "off"}}
	got := RunFallbackRules(r)
	if !hasFindingID(got, "autovacuum_off") {
		t.Errorf("expected autovacuum_off finding, got %d findings: %v", len(got), findingIDs(got))
	}
}

func TestFallback_AutovacuumOff_DoesNotTrigger_WhenOn(t *testing.T) {
	r := &WDRReport{Settings: map[string]string{"autovacuum": "on"}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "autovacuum_off") {
		t.Error("autovacuum=on should not trigger autovacuum_off rule")
	}
}

func TestFallback_AutovacuumOff_DoesNotTrigger_WhenMissing(t *testing.T) {
	r := &WDRReport{Settings: map[string]string{}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "autovacuum_off") {
		t.Error("missing autovacuum key should not trigger (no data to judge)")
	}
}

// ── Rule 2: deadlocks ──────────────────────────────────────────────────

func TestFallback_Deadlock_Triggers(t *testing.T) {
	r := &WDRReport{Locks: LockStats{DeadlockCount: 3}}
	got := RunFallbackRules(r)
	if !hasFindingID(got, "deadlock_present") {
		t.Error("3 deadlocks should trigger")
	}
}

func TestFallback_Deadlock_DoesNotTrigger_WhenZero(t *testing.T) {
	r := &WDRReport{Locks: LockStats{DeadlockCount: 0}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "deadlock_present") {
		t.Error("deadlock_count=0 should not trigger")
	}
}

// ── Rule 3: replication lag ────────────────────────────────────────────

func TestFallback_ReplicationLag_Triggers(t *testing.T) {
	r := &WDRReport{Replication: ReplicationStats{
		StandbyCount: 2, MaxLagSeconds: 75.0, SyncMode: "async",
	}}
	got := RunFallbackRules(r)
	if !hasFindingID(got, "replication_lag_high") {
		t.Error("75s lag should trigger (threshold 60s)")
	}
}

func TestFallback_ReplicationLag_DoesNotTrigger_BelowThreshold(t *testing.T) {
	r := &WDRReport{Replication: ReplicationStats{
		StandbyCount: 2, MaxLagSeconds: 30.0,
	}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "replication_lag_high") {
		t.Error("30s lag should NOT trigger (threshold 60s)")
	}
}

func TestFallback_ReplicationLag_DoesNotTrigger_NoStandby(t *testing.T) {
	r := &WDRReport{Replication: ReplicationStats{
		StandbyCount: 0, MaxLagSeconds: 999.0,
	}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "replication_lag_high") {
		t.Error("no standby should skip the rule entirely")
	}
}

// ── Rule 4: buffer hit catastrophic ────────────────────────────────────

func TestFallback_BufferHitCritical_Triggers(t *testing.T) {
	r := &WDRReport{IO: IOStats{
		BlocksHit:  70, // → ratio 70 / (70+30) = 70%
		BlocksRead: 30,
	}}
	got := RunFallbackRules(r)
	if !hasFindingID(got, "buffer_hit_critical") {
		t.Error("70% hit ratio should trigger (threshold 80%)")
	}
}

func TestFallback_BufferHitCritical_DoesNotTrigger_AboveThreshold(t *testing.T) {
	r := &WDRReport{IO: IOStats{
		BlocksHit:  85, // → ratio 85%
		BlocksRead: 15,
	}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "buffer_hit_critical") {
		t.Error("85% hit ratio should NOT trigger (threshold 80%)")
	}
}

func TestFallback_BufferHitCritical_DoesNotTrigger_NoIOData(t *testing.T) {
	r := &WDRReport{IO: IOStats{BlocksHit: 0, BlocksRead: 0}}
	got := RunFallbackRules(r)
	if hasFindingID(got, "buffer_hit_critical") {
		t.Error("no IO data → can't judge, must not trigger")
	}
}

// ── Rule 5: single SQL dominates ───────────────────────────────────────

func TestFallback_SingleSQLDominant_Triggers(t *testing.T) {
	r := &WDRReport{
		TimeModel: TimeModelStats{DBTimeSec: 10000},
		TopSQLs: []TopSQLEntry{
			{SQLID: "12345", TotalTimeMS: 6000000, Calls: 100, AvgTimeMS: 60000},
			// 6000000ms = 6000s, 6000/10000 = 60% > 50%
		},
	}
	got := RunFallbackRules(r)
	if !hasFindingID(got, "single_sql_dominant") {
		t.Error("60%% of DB Time on one SQL should trigger")
	}
}

func TestFallback_SingleSQLDominant_DoesNotTrigger_SpreadOut(t *testing.T) {
	r := &WDRReport{
		TimeModel: TimeModelStats{DBTimeSec: 10000},
		TopSQLs: []TopSQLEntry{
			{SQLID: "12345", TotalTimeMS: 3000000}, // 30%
			{SQLID: "67890", TotalTimeMS: 2500000}, // 25%
		},
	}
	got := RunFallbackRules(r)
	if hasFindingID(got, "single_sql_dominant") {
		t.Error("30%% spread should NOT trigger")
	}
}

func TestFallback_SingleSQLDominant_DoesNotTrigger_NoData(t *testing.T) {
	r := &WDRReport{
		TimeModel: TimeModelStats{DBTimeSec: 0},
		TopSQLs:   nil,
	}
	got := RunFallbackRules(r)
	if hasFindingID(got, "single_sql_dominant") {
		t.Error("no data should not trigger")
	}
}

// ── Integration: realistic WDR with multiple triggers ──────────────────

func TestFallback_RealisticWorkload_MultipleTriggers(t *testing.T) {
	// Matches the customer scenario from docs/wdr/plan-wdranalyze.md:
	// autovacuum off + single SQL 47% + buffer hit 91% (not critical)
	r := &WDRReport{
		Settings: map[string]string{"autovacuum": "off"},
		TimeModel: TimeModelStats{DBTimeSec: 29723.5},
		IO: IOStats{
			BlocksHit:  29400000,
			BlocksRead: 2840000, // → 91.2%, NOT critical (threshold 80%)
		},
		TopSQLs: []TopSQLEntry{
			{SQLID: "1923014772", TotalTimeMS: 14250000, Calls: 1827},
			// 14250000ms / 29723.5s / 1000 = 47.9% > 50% NOT.
			// Actually < 50%, so should NOT trigger single_sql_dominant
		},
	}
	got := RunFallbackRules(r)
	if !hasFindingID(got, "autovacuum_off") {
		t.Error("expected autovacuum_off")
	}
	if hasFindingID(got, "buffer_hit_critical") {
		t.Errorf("91.2%% hit should NOT trigger critical (threshold 80%%)")
	}
	if hasFindingID(got, "single_sql_dominant") {
		t.Errorf("47.9%% SQL should NOT trigger (threshold 50%%)")
	}
	// Just one expected here: autovacuum_off
	if len(got) != 1 {
		t.Errorf("expected exactly 1 finding (autovacuum_off), got %d: %v",
			len(got), findingIDs(got))
	}
}

func TestFallback_HealthyWorkload_NoTriggers(t *testing.T) {
	r := &WDRReport{
		Settings:  map[string]string{"autovacuum": "on"},
		TimeModel: TimeModelStats{DBTimeSec: 1000},
		IO:        IOStats{BlocksHit: 95, BlocksRead: 5}, // 95% hit
		Locks:     LockStats{DeadlockCount: 0},
		Replication: ReplicationStats{
			StandbyCount: 2, MaxLagSeconds: 5.0,
		},
		TopSQLs: []TopSQLEntry{
			{SQLID: "abc", TotalTimeMS: 100000}, // 10% of DB Time
			{SQLID: "def", TotalTimeMS: 80000},  // 8%
		},
	}
	got := RunFallbackRules(r)
	if len(got) != 0 {
		t.Errorf("healthy workload should trigger 0 findings, got %d: %v",
			len(got), findingIDs(got))
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func hasFindingID(findings []Finding, id string) bool {
	for _, f := range findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func findingIDs(findings []Finding) []string {
	ids := make([]string, len(findings))
	for i, f := range findings {
		ids[i] = f.ID
	}
	return ids
}
