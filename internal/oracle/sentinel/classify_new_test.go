/*-------------------------------------------------------------------------
 *
 * classify_new_test.go
 *	  Test cases for classify_new.go (sentinel package):
 *	  TestClassifyPlanDrift, TestClassifyPlanDrift_HighConcurrency,
 *	  TestClassifyPlanDrift_SinglePlanHighElapsed.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/classify_new_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "testing"

// ──────────────────────────────────────────────────────────────────
// Phase 3: PlanDrift
// ──────────────────────────────────────────────────────────────────

func TestClassifyPlanDrift(t *testing.T) {
	// Multi plan hash → drift with confidence 0.8
	r := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "abc123", OccurrenceRate: 0.5, MaxConcurrent: 2, MaxElapsedSec: 45.0},
		},
		PlanHistory: map[string][]int64{
			"abc123": {111111, 222222},
		},
	}
	c := classifyPlanDrift(r)
	if c.Cause != CausePlanDrift {
		t.Errorf("cause = %q, want plan_drift", c.Cause)
	}
	if c.Confidence != 0.8 {
		t.Errorf("confidence = %f, want 0.8 for multi plan hash", c.Confidence)
	}
	if len(c.Evidence) == 0 {
		t.Error("expected evidence")
	}
}

func TestClassifyPlanDrift_HighConcurrency(t *testing.T) {
	// High concurrency (>=5) should NOT be plan drift — it's BadSQL territory
	r := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "abc123", OccurrenceRate: 0.8, MaxConcurrent: 6, MaxElapsedSec: 45.0},
		},
		PlanHistory: map[string][]int64{
			"abc123": {111111, 222222},
		},
	}
	c := classifyPlanDrift(r)
	if c.Cause == CausePlanDrift {
		t.Error("high concurrency (>=5) should not trigger plan_drift")
	}
}

func TestClassifyPlanDrift_SinglePlanHighElapsed(t *testing.T) {
	// Single plan but high elapsed + low concurrency → confidence 0.6
	r := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "xyz789", OccurrenceRate: 0.4, MaxConcurrent: 1, MaxElapsedSec: 60.0},
		},
	}
	c := classifyPlanDrift(r)
	if c.Cause != CausePlanDrift {
		t.Errorf("cause = %q, want plan_drift", c.Cause)
	}
	if c.Confidence != 0.6 {
		t.Errorf("confidence = %f, want 0.6 for single plan pattern match", c.Confidence)
	}
}

func TestClassifyPlanDrift_LowOccurrence(t *testing.T) {
	// OccurrenceRate < 0.3 should not trigger
	r := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "low1", OccurrenceRate: 0.2, MaxConcurrent: 1, MaxElapsedSec: 100.0},
		},
	}
	c := classifyPlanDrift(r)
	if c.Cause == CausePlanDrift {
		t.Error("low occurrence rate (<0.3) should not trigger plan_drift")
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 4: MemoryPressure
// ──────────────────────────────────────────────────────────────────

func TestClassifyMemoryPressure(t *testing.T) {
	// Buffer busy waits + PGA high
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "buffer busy waits", WaitClass: "Concurrency", Percentage: 20},
			{Event: "free buffer waits", WaitClass: "Configuration", Percentage: 5},
		},
		Metrics: map[string]MetricSummary{
			string(MetricPGAUsedPct): {Max: 92},
		},
	}
	c := classifyMemoryPressure(r)
	if c.Cause != CauseMemoryPressure {
		t.Errorf("cause = %q, want memory_pressure", c.Cause)
	}
	if c.Confidence < 0.6 {
		t.Errorf("confidence = %f, want >= 0.6 with buffer waits + PGA high", c.Confidence)
	}
}

func TestClassifyMemoryPressure_LowWaits(t *testing.T) {
	// Memory wait events below threshold → no match
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "buffer busy waits", WaitClass: "Concurrency", Percentage: 3},
		},
	}
	c := classifyMemoryPressure(r)
	if c.Cause == CauseMemoryPressure {
		t.Error("memory waits below 10% should not trigger memory_pressure")
	}
}

func TestClassifyMemoryPressure_SharedPoolLow(t *testing.T) {
	// Shared pool free very low adds confidence
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "latch: cache buffers chains", WaitClass: "Concurrency", Percentage: 15},
		},
		Metrics: map[string]MetricSummary{
			string(MetricSharedPoolFreePct): {Min: 3},
		},
	}
	c := classifyMemoryPressure(r)
	if c.Cause != CauseMemoryPressure {
		t.Errorf("cause = %q, want memory_pressure", c.Cause)
	}
	if c.Confidence < 0.6 {
		t.Errorf("confidence = %f, want >= 0.6 with shared pool low", c.Confidence)
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 4: ConnectionExhaust
// ──────────────────────────────────────────────────────────────────

func TestClassifyConnectionExhaust(t *testing.T) {
	// Sessions > 85% of limit
	r := BurstReport{
		ResourceLimits: []ResourceLimitEntry{
			{ResourceName: "sessions", CurrentUtilization: 450, LimitValue: 500},
		},
	}
	c := classifyConnectionExhaust(r)
	if c.Cause != CauseConnectionExhaust {
		t.Errorf("cause = %q, want connection_exhaust", c.Cause)
	}
	if c.Confidence < 0.6 {
		t.Errorf("confidence = %f, want >= 0.6 for 90%% usage", c.Confidence)
	}
}

func TestClassifyConnectionExhaust_Critical(t *testing.T) {
	// Sessions > 95% of limit → higher confidence
	r := BurstReport{
		ResourceLimits: []ResourceLimitEntry{
			{ResourceName: "sessions", CurrentUtilization: 490, LimitValue: 500},
		},
	}
	c := classifyConnectionExhaust(r)
	if c.Cause != CauseConnectionExhaust {
		t.Errorf("cause = %q, want connection_exhaust", c.Cause)
	}
	if c.Confidence < 0.85 {
		t.Errorf("confidence = %f, want >= 0.85 for 98%% usage", c.Confidence)
	}
}

func TestClassifyConnectionExhaust_NotTriggered(t *testing.T) {
	// Sessions at 50% → no trigger
	r := BurstReport{
		ResourceLimits: []ResourceLimitEntry{
			{ResourceName: "sessions", CurrentUtilization: 250, LimitValue: 500},
		},
	}
	c := classifyConnectionExhaust(r)
	if c.Cause == CauseConnectionExhaust {
		t.Error("50% sessions should not trigger connection_exhaust")
	}
}

func TestClassifyConnectionExhaust_FallbackMetric(t *testing.T) {
	// No ResourceLimits but resource_limit_pct metric > 85
	r := BurstReport{
		Metrics: map[string]MetricSummary{
			string(MetricResourceLimitPct): {Max: 92},
		},
	}
	c := classifyConnectionExhaust(r)
	if c.Cause != CauseConnectionExhaust {
		t.Errorf("cause = %q, want connection_exhaust from metric fallback", c.Cause)
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 5: ArchiveDelay
// ──────────────────────────────────────────────────────────────────

func TestClassifyArchiveDelay(t *testing.T) {
	// "log file switch (archiving needed)" > 5%
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "log file switch (archiving needed)", WaitClass: "Configuration", Percentage: 15},
		},
	}
	c := classifyArchiveDelay(r)
	if c.Cause != CauseArchiveDelay {
		t.Errorf("cause = %q, want archive_delay", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7", c.Confidence)
	}
}

func TestClassifyArchiveDelay_WithFRA(t *testing.T) {
	// Archive wait + FRA high → boosted confidence
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "log file switch (archiving needed)", WaitClass: "Configuration", Percentage: 25},
		},
		SpaceDetails: []SpaceDetail{
			{Name: "FRA", UsedPct: 95},
		},
	}
	c := classifyArchiveDelay(r)
	if c.Cause != CauseArchiveDelay {
		t.Errorf("cause = %q, want archive_delay", c.Cause)
	}
	if c.Confidence < 0.9 {
		t.Errorf("confidence = %f, want >= 0.9 with FRA >90%% and wait >20%%", c.Confidence)
	}
}

func TestClassifyArchiveDelay_LowWait(t *testing.T) {
	// Archive wait below threshold → no trigger
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "log file switch (archiving needed)", WaitClass: "Configuration", Percentage: 3},
		},
	}
	c := classifyArchiveDelay(r)
	if c.Cause == CauseArchiveDelay {
		t.Error("3% archive wait should not trigger archive_delay")
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 5: DGSyncDelay
// ──────────────────────────────────────────────────────────────────

func TestClassifyDGSyncDelay(t *testing.T) {
	// log file sync + standby lag > 120s
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "log file sync", WaitClass: "Commit", Percentage: 25},
		},
		Metrics: map[string]MetricSummary{
			string(MetricStandbyApplyLag): {Max: 300},
			string(MetricDataGuardStatus): {Max: 1},
		},
	}
	c := classifyDGSyncDelay(r)
	if c.Cause != CauseDGSyncDelay {
		t.Errorf("cause = %q, want dg_sync_delay", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7 with both DG indicators", c.Confidence)
	}
}

func TestClassifyDGSyncDelay_NoDGEvidence(t *testing.T) {
	// log file sync but no DG evidence → should NOT be DG (it's Redo)
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "log file sync", WaitClass: "Commit", Percentage: 25},
		},
	}
	c := classifyDGSyncDelay(r)
	if c.Cause == CauseDGSyncDelay {
		t.Error("without DG evidence, log file sync should not trigger dg_sync_delay")
	}
}

func TestClassifyDGSyncDelay_LowSync(t *testing.T) {
	// log file sync < 15% → no trigger
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "log file sync", WaitClass: "Commit", Percentage: 10},
		},
		Metrics: map[string]MetricSummary{
			string(MetricStandbyApplyLag): {Max: 300},
		},
	}
	c := classifyDGSyncDelay(r)
	if c.Cause == CauseDGSyncDelay {
		t.Error("log file sync <15% should not trigger dg_sync_delay")
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 5: UndoPressure
// ──────────────────────────────────────────────────────────────────

func TestClassifyUndoPressure(t *testing.T) {
	// US contention + undo high
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "enq: US - contention", WaitClass: "Application", Percentage: 8},
		},
		Metrics: map[string]MetricSummary{
			string(MetricUndoUsedPct): {Max: 92},
		},
	}
	c := classifyUndoPressure(r)
	if c.Cause != CauseUndoPressure {
		t.Errorf("cause = %q, want undo_pressure", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7 with wait + metric", c.Confidence)
	}
}

func TestClassifyUndoPressure_WaitOnly(t *testing.T) {
	// US contention only (undo metric stub=0) → still triggers
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "enq: US - contention", WaitClass: "Application", Percentage: 5},
		},
	}
	c := classifyUndoPressure(r)
	if c.Cause != CauseUndoPressure {
		t.Errorf("cause = %q, want undo_pressure", c.Cause)
	}
	if c.Confidence != 0.5 {
		t.Errorf("confidence = %f, want 0.5 for wait-only", c.Confidence)
	}
}

func TestClassifyUndoPressure_NeitherMatch(t *testing.T) {
	// No US contention, undo not high → no trigger
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "enq: US - contention", WaitClass: "Application", Percentage: 1},
		},
		Metrics: map[string]MetricSummary{
			string(MetricUndoUsedPct): {Max: 50},
		},
	}
	c := classifyUndoPressure(r)
	if c.Cause == CauseUndoPressure {
		t.Error("low US wait and low undo should not trigger undo_pressure")
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 5: TempPressure
// ──────────────────────────────────────────────────────────────────

func TestClassifyTempPressure(t *testing.T) {
	// direct path temp waits + temp high
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "direct path read temp", WaitClass: "User I/O", Percentage: 8},
			{Event: "direct path write temp", WaitClass: "User I/O", Percentage: 5},
		},
		Metrics: map[string]MetricSummary{
			string(MetricTempUsedPct): {Max: 90},
		},
	}
	c := classifyTempPressure(r)
	if c.Cause != CauseTempPressure {
		t.Errorf("cause = %q, want temp_pressure", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7 with wait + metric", c.Confidence)
	}
}

func TestClassifyTempPressure_SpaceDetailsOnly(t *testing.T) {
	// TEMP from SpaceDetails (no metric) + wait events
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "direct path read temp", WaitClass: "User I/O", Percentage: 12},
		},
		SpaceDetails: []SpaceDetail{
			{Name: "TEMP", UsedPct: 95},
		},
	}
	c := classifyTempPressure(r)
	if c.Cause != CauseTempPressure {
		t.Errorf("cause = %q, want temp_pressure", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7 with wait + SpaceDetails", c.Confidence)
	}
}

func TestClassifyTempPressure_LowWaitAndUsage(t *testing.T) {
	// Low temp waits and normal usage → no trigger
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{Event: "direct path read temp", WaitClass: "User I/O", Percentage: 3},
		},
		Metrics: map[string]MetricSummary{
			string(MetricTempUsedPct): {Max: 40},
		},
	}
	c := classifyTempPressure(r)
	if c.Cause == CauseTempPressure {
		t.Error("low temp waits and normal usage should not trigger temp_pressure")
	}
}

// ──────────────────────────────────────────────────────────────────
// Phase 2: DatabaseHang
// ──────────────────────────────────────────────────────────────────

func TestClassifyDatabaseHang(t *testing.T) {
	// Elimination: nothing specific matches, scattered waits, high active, low TPS
	r := BurstReport{
		PeakActive:     200,
		BaselineActive: 15,
		Metrics: map[string]MetricSummary{
			string(MetricActive):     {Max: 200, Avg: 150},
			string(MetricCommitRate): {Avg: 2, Max: 5}, // very low TPS
		},
		WaitProfile: []WaitBucket{
			{Event: "enq: TX - row lock contention", WaitClass: "Application", Percentage: 10},
			{Event: "db file sequential read", WaitClass: "User I/O", Percentage: 10},
			{Event: "cursor: pin S wait on X", WaitClass: "Concurrency", Percentage: 10},
			{Event: "log file sync", WaitClass: "Commit", Percentage: 10},
			{Event: "SQL*Net message from client", WaitClass: "Idle", Percentage: 15},
			{Event: "log file switch (archiving needed)", WaitClass: "Configuration", Percentage: 5},
		},
	}
	c := classifyDatabaseHang(r)
	if c.Cause != CauseDatabaseHang {
		t.Errorf("cause = %q, want database_hang", c.Cause)
	}
	if c.Confidence < 0.5 {
		t.Errorf("confidence = %f, want >= 0.5", c.Confidence)
	}
}

func TestClassifyDatabaseHang_NotHang_IODominant(t *testing.T) {
	// IO dominant (>60%) → eliminated, not hang
	r := BurstReport{
		PeakActive:     100,
		BaselineActive: 10,
		Metrics: map[string]MetricSummary{
			string(MetricActive):     {Max: 100, Avg: 80},
			string(MetricCommitRate): {Avg: 5},
		},
		WaitProfile: []WaitBucket{
			{Event: "db file sequential read", WaitClass: "User I/O", Percentage: 65},
		},
	}
	c := classifyDatabaseHang(r)
	if c.Cause == CauseDatabaseHang {
		t.Error("IO dominant (>60%) should eliminate hang classification")
	}
}

func TestClassifyDatabaseHang_NotHang_HighTPS(t *testing.T) {
	// TPS still healthy → not a hang
	r := BurstReport{
		PeakActive:     100,
		BaselineActive: 10,
		Metrics: map[string]MetricSummary{
			string(MetricActive):     {Max: 100, Avg: 80},
			string(MetricCommitRate): {Avg: 200}, // healthy TPS
		},
		WaitProfile: []WaitBucket{},
	}
	c := classifyDatabaseHang(r)
	if c.Cause == CauseDatabaseHang {
		t.Error("healthy TPS should not trigger hang classification")
	}
}

func TestClassifyDatabaseHang_InstanceNotOpen(t *testing.T) {
	// Instance not OPEN adds 0.3 score
	r := BurstReport{
		PeakActive:     200,
		BaselineActive: 15,
		Metrics: map[string]MetricSummary{
			string(MetricActive):         {Max: 200, Avg: 150},
			string(MetricCommitRate):     {Avg: 0},
			string(MetricInstanceStatus): {Max: 0}, // not OPEN
		},
		WaitProfile: []WaitBucket{
			{Event: "misc wait", WaitClass: "Other", Percentage: 20},
		},
	}
	c := classifyDatabaseHang(r)
	if c.Cause != CauseDatabaseHang {
		t.Errorf("cause = %q, want database_hang (instance not OPEN)", c.Cause)
	}
	if c.Confidence < 0.5 {
		t.Errorf("confidence = %f, want >= 0.5", c.Confidence)
	}
}

func TestClassifyDatabaseHang_NotHang_LowActive(t *testing.T) {
	// Active sessions not high enough → no pre-screen pass
	r := BurstReport{
		Metrics: map[string]MetricSummary{
			string(MetricActive):     {Max: 5, Avg: 3},
			string(MetricCommitRate): {Avg: 1},
		},
	}
	c := classifyDatabaseHang(r)
	if c.Cause == CauseDatabaseHang {
		t.Error("low active sessions should not trigger hang")
	}
}

// ──────────────────────────────────────────────────────────────────
// Integration: Priority chain tests
// ──────────────────────────────────────────────────────────────────

func TestClassifyPriorityChain_LockOverBadSQL(t *testing.T) {
	// Both lock and bad SQL present, lock wins (Rule 1 > Rule 2)
	r := BurstReport{
		BlockingChains: []BlockingChain{
			{RootSID: 100, VictimCount: 5, RootSQLID: "sql_lock", RootEvent: "TX"},
		},
		TopSQLs: []SQLProfile{
			{SQLID: "sql_hot", OccurrenceRate: 0.9, MaxConcurrent: 10, MaxElapsedSec: 20},
		},
	}
	c := Classify(r)
	if c.Cause != CauseLockContention {
		t.Errorf("cause = %q, want lock_contention (higher priority than bad_sql)", c.Cause)
	}
}

func TestClassifyPriorityChain_PlanDriftOverRedo(t *testing.T) {
	// Both plan drift and redo present, plan drift wins (Rule 3 > Rule 4)
	r := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "drift1", OccurrenceRate: 0.5, MaxConcurrent: 2, MaxElapsedSec: 50},
		},
		PlanHistory: map[string][]int64{
			"drift1": {111, 222},
		},
		WaitProfile: []WaitBucket{
			{WaitClass: "Commit", Percentage: 30},
		},
	}
	c := Classify(r)
	if c.Cause != CausePlanDrift {
		t.Errorf("cause = %q, want plan_drift (Rule 3 over Redo Rule 4)", c.Cause)
	}
}

func TestClassifyPriorityChain_HangIsLast(t *testing.T) {
	// Hang only matches when nothing specific does — scattered waits, low TPS
	r := BurstReport{
		PeakActive:     200,
		BaselineActive: 10,
		Metrics: map[string]MetricSummary{
			string(MetricActive):     {Max: 200, Avg: 180},
			string(MetricCommitRate): {Avg: 1},
		},
		WaitProfile: []WaitBucket{
			{Event: "wait1", WaitClass: "Other", Percentage: 8},
			{Event: "wait2", WaitClass: "Application", Percentage: 5},
			{Event: "wait3", WaitClass: "User I/O", Percentage: 7},
			{Event: "log file switch (archiving needed)", WaitClass: "Configuration", Percentage: 4},
		},
	}
	c := Classify(r)
	if c.Cause != CauseDatabaseHang {
		t.Errorf("cause = %q, want database_hang (nothing else matched)", c.Cause)
	}
}

func TestClassifyPriorityChain_FallbackTrigger(t *testing.T) {
	// Nothing matches including hang → fall back to trigger metric
	r := BurstReport{
		TriggerEvent: TriggerEvent{
			Metric:    string(MetricIO),
			Baseline:  5,
			Current:   20,
			Threshold: 15,
		},
		Metrics: map[string]MetricSummary{
			string(MetricActive): {Max: 5, Avg: 3},
		},
	}
	c := Classify(r)
	if c.Cause != CauseIOSubsystem {
		t.Errorf("cause = %q, want io_subsystem (from trigger fallback)", c.Cause)
	}
	if c.Confidence != 0.4 {
		t.Errorf("confidence = %f, want 0.4 for trigger fallback", c.Confidence)
	}
}

// ──────────────────────────────────────────────────────────────────
// Helper: waitEventPct
// ──────────────────────────────────────────────────────────────────

func TestWaitEventPct(t *testing.T) {
	profile := []WaitBucket{
		{Event: "buffer busy waits", WaitClass: "Concurrency", Percentage: 12.5},
		{Event: "db file sequential read", WaitClass: "User I/O", Percentage: 30},
	}
	got := waitEventPct(profile, "buffer busy waits")
	if got != 12.5 {
		t.Errorf("waitEventPct = %f, want 12.5", got)
	}
	got = waitEventPct(profile, "nonexistent")
	if got != 0 {
		t.Errorf("waitEventPct for missing event = %f, want 0", got)
	}
}
