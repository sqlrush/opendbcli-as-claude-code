/*-------------------------------------------------------------------------
 *
 * detector_test.go
 *	  Test cases for detector.go (sentinel package):
 *	  TestMetricHistory_PushAndValues, TestMetricHistory_RingBuffer,
 *	  TestLinearSlope_Flat.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/detector_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"testing"
)

// ── MetricHistory ──────────────────────────────────────────────────

func TestMetricHistory_PushAndValues(t *testing.T) {
	h := NewMetricHistory(5)
	if h.Len() != 0 {
		t.Errorf("empty history len = %d, want 0", h.Len())
	}

	for i := 1; i <= 5; i++ {
		h.Push(float64(i))
	}
	if h.Len() != 5 {
		t.Errorf("after 5 pushes, len = %d, want 5", h.Len())
	}
	vals := h.Values()
	for i, v := range vals {
		if v != float64(i+1) {
			t.Errorf("vals[%d] = %f, want %f", i, v, float64(i+1))
		}
	}
}

func TestMetricHistory_RingBuffer(t *testing.T) {
	h := NewMetricHistory(3)
	for i := 1; i <= 5; i++ {
		h.Push(float64(i))
	}
	if h.Len() != 3 {
		t.Errorf("after overflow, len = %d, want 3", h.Len())
	}
	vals := h.Values()
	// Should have 3, 4, 5 (oldest to newest)
	want := []float64{3, 4, 5}
	for i, v := range vals {
		if v != want[i] {
			t.Errorf("vals[%d] = %f, want %f", i, v, want[i])
		}
	}
}

// ── linearSlope ────────────────────────────────────────────────────

func TestLinearSlope_Flat(t *testing.T) {
	slope := linearSlope([]float64{5, 5, 5, 5, 5})
	if slope != 0 {
		t.Errorf("flat slope = %f, want 0", slope)
	}
}

func TestLinearSlope_Rising(t *testing.T) {
	// Values: 0, 1, 2, 3, 4 → slope = 1.0
	slope := linearSlope([]float64{0, 1, 2, 3, 4})
	if slope < 0.99 || slope > 1.01 {
		t.Errorf("rising slope = %f, want ~1.0", slope)
	}
}

func TestLinearSlope_TooFew(t *testing.T) {
	if slope := linearSlope([]float64{5}); slope != 0 {
		t.Errorf("single value slope = %f, want 0", slope)
	}
	if slope := linearSlope(nil); slope != 0 {
		t.Errorf("nil slope = %f, want 0", slope)
	}
}

// ── T3 Trend Detection ────────────────────────────────────────────

func TestCheckT3_NoTriggerFlat(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 10, Std: 2, Ready: true}
	cfg := DefaultConfig()

	// Push 15 flat values
	for i := 0; i < 15; i++ {
		d.PushMetricValue(MetricLogicalReadRate, 10)
	}

	trigger := d.CheckT3(MetricLogicalReadRate, bl, cfg)
	if trigger != nil {
		t.Error("flat values should not trigger T3")
	}
}

func TestCheckT3_TriggerOnTrend(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 10, Std: 1, Ready: true}
	cfg := DefaultConfig()

	// Push steadily rising values: 10, 11, 12, ... 24
	for i := 0; i < 15; i++ {
		d.PushMetricValue(MetricLogicalReadRate, float64(10+i))
	}

	// Should trigger after enough sustained trend windows
	var trigger *TriggerEvent
	for i := 0; i < 5; i++ {
		d.PushMetricValue(MetricLogicalReadRate, float64(25+i))
		trigger = d.CheckT3(MetricLogicalReadRate, bl, cfg)
		if trigger != nil {
			break
		}
	}
	if trigger == nil {
		t.Error("sustained rising trend should trigger T3")
	}
}

// ── T4 Acceleration Detection ─────────────────────────────────────

func TestCheckT4_NoTriggerSteady(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 10, Std: 2, Ready: true}
	hw := HardwareProfile{}

	// Push 5 steady values
	for i := 0; i < 5; i++ {
		d.PushMetricValue(MetricPQSessions, 10)
	}

	trigger := d.CheckT4(MetricPQSessions, 10, bl, hw)
	if trigger != nil {
		t.Error("steady values should not trigger T4")
	}
}

func TestCheckT4_TriggerOnAcceleration(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 5, Std: 1, Ready: true}
	hw := HardwareProfile{} // softAbsMin for PQ = max(0*0.5, 4) = 4

	// Push accelerating values: 5, 7, 12 → accel = 12 - 2*7 + 5 = 3
	d.PushMetricValue(MetricPQSessions, 5)
	d.PushMetricValue(MetricPQSessions, 7)
	d.PushMetricValue(MetricPQSessions, 12)

	trigger := d.CheckT4(MetricPQSessions, 12, bl, hw)
	if trigger == nil {
		t.Fatal("acceleration above threshold should trigger T4")
	}
	if trigger.Current != 12 {
		t.Errorf("current = %f, want 12", trigger.Current)
	}
}

func TestCheckT4_BelowSoftAbsMin(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 1, Std: 0.5, Ready: true}
	hw := HardwareProfile{CPUCores: 16} // softAbsMin for PQ = 16*0.5 = 8

	d.PushMetricValue(MetricPQSessions, 1)
	d.PushMetricValue(MetricPQSessions, 2)
	d.PushMetricValue(MetricPQSessions, 5)

	trigger := d.CheckT4(MetricPQSessions, 5, bl, hw)
	if trigger != nil {
		t.Error("below softAbsMin should not trigger T4")
	}
}

// ── T5 Compound Detection ─────────────────────────────────────────

func TestCheckT5_InstanceStatus(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricInstanceStatus)

	// OPEN (1) → no trigger
	trigger := d.CheckT5(MetricInstanceStatus, 1, def, nil)
	if trigger != nil {
		t.Error("OPEN status should not trigger T5")
	}

	// Non-OPEN (0) → trigger
	trigger = d.CheckT5(MetricInstanceStatus, 0, def, nil)
	if trigger == nil {
		t.Fatal("non-OPEN status should trigger T5")
	}
}

func TestCheckT5_CompoundRequiresAllAlerts(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricRedoLogSpaceWait)
	// CompoundWith: [MetricLogSwitchRate]

	// LogSwitchRate NOT in alert → no trigger
	trigger := d.CheckT5(MetricRedoLogSpaceWait, 5, def, nil)
	if trigger != nil {
		t.Error("should not trigger when companion not in alert")
	}

	// Set LogSwitchRate to alert
	d.SetAlertState(MetricLogSwitchRate, true)
	trigger = d.CheckT5(MetricRedoLogSpaceWait, 5, def, nil)
	if trigger == nil {
		t.Fatal("should trigger when all companions are in alert")
	}
}

// ── T6 Capacity Detection ─────────────────────────────────────────

func TestCheckT6_NormalCapacity(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricTablespaceUsedPct)

	trigger := d.CheckT6(MetricTablespaceUsedPct, 80, def)
	if trigger != nil {
		t.Error("80% should not trigger (red=95%)")
	}
}

func TestCheckT6_RedThreshold(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricTablespaceUsedPct)

	trigger := d.CheckT6(MetricTablespaceUsedPct, 96, def)
	if trigger == nil {
		t.Fatal("96% should trigger (red=95%)")
	}
	if trigger.Current != 96 {
		t.Errorf("current = %f, want 96", trigger.Current)
	}
}

func TestCheckT6_InvertedCapacity(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricSharedPoolFreePct)

	// 10% free → above red threshold (5%) → no trigger
	trigger := d.CheckT6(MetricSharedPoolFreePct, 10, def)
	if trigger != nil {
		t.Error("10% free should not trigger (red=5%)")
	}

	// 3% free → below red threshold → trigger
	trigger = d.CheckT6(MetricSharedPoolFreePct, 3, def)
	if trigger == nil {
		t.Fatal("3% free should trigger (red=5%)")
	}
}

// ── T7 Shift Detection ────────────────────────────────────────────

func TestCheckT7_NoShift(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 1000, Std: 50, Ready: true}

	// Push 25 values around 1000
	for i := 0; i < 25; i++ {
		d.PushMetricValue(MetricNetworkRoundTrip, 1000)
	}

	trigger := d.CheckT7(MetricNetworkRoundTrip, bl)
	if trigger != nil {
		t.Error("stable values should not trigger T7")
	}
}

func TestCheckT7_DetectsShift(t *testing.T) {
	d := NewDetectorState()
	bl := &MetricBaseline{Avg: 1000, Std: 50, Ready: true}

	// First half: around 100, second half: around 500
	for i := 0; i < 15; i++ {
		d.PushMetricValue(MetricNetworkRoundTrip, 100)
	}
	for i := 0; i < 15; i++ {
		d.PushMetricValue(MetricNetworkRoundTrip, 500)
	}

	trigger := d.CheckT7(MetricNetworkRoundTrip, bl)
	if trigger == nil {
		t.Fatal("level shift (100→500) should trigger T7")
	}
}

// ── T8 Regression Detection ───────────────────────────────────────

func TestCheckT8_NormalHitRate(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricBufferCacheHit)

	trigger := d.CheckT8(MetricBufferCacheHit, 98, def)
	if trigger != nil {
		t.Error("98% hit rate should not trigger (floor=90%)")
	}
}

func TestCheckT8_LowHitRate_Sustained(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricBufferCacheHit)

	// Need 5 sustained ticks below 90%
	for i := 0; i < 4; i++ {
		trigger := d.CheckT8(MetricBufferCacheHit, 85, def)
		if trigger != nil {
			t.Errorf("tick %d: should not trigger yet (need 5 sustained)", i+1)
		}
	}

	trigger := d.CheckT8(MetricBufferCacheHit, 85, def)
	if trigger == nil {
		t.Fatal("tick 5: should trigger after 5 sustained below floor")
	}
}

func TestCheckT8_LatchMissRate(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricLatchFreeRate)

	// Latch miss rate > 5% → trigger (inverted: higher is worse)
	for i := 0; i < 5; i++ {
		d.CheckT8(MetricLatchFreeRate, 8, def)
	}
	trigger := d.CheckT8(MetricLatchFreeRate, 8, def)
	// sustainedT8 was reset after 5, so we need another round
	// Actually the 5th call triggers and resets. Let me redo:
	d2 := NewDetectorState()
	for i := 0; i < 4; i++ {
		d2.CheckT8(MetricLatchFreeRate, 8, def)
	}
	trigger = d2.CheckT8(MetricLatchFreeRate, 8, def)
	if trigger == nil {
		t.Fatal("latch miss rate 8% should trigger after sustained (floor=5%)")
	}
}

func TestCheckT8_ResetOnNormal(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricBufferCacheHit)

	// 3 ticks below floor, then normal, then 3 more → never reaches 5
	for i := 0; i < 3; i++ {
		d.CheckT8(MetricBufferCacheHit, 85, def)
	}
	d.CheckT8(MetricBufferCacheHit, 95, def) // reset
	for i := 0; i < 3; i++ {
		d.CheckT8(MetricBufferCacheHit, 85, def)
	}
	// Only 3 consecutive after reset → should not trigger
	if d.sustainedT8[MetricBufferCacheHit] != 3 {
		t.Errorf("sustained = %d, want 3", d.sustainedT8[MetricBufferCacheHit])
	}
}

// ── T9 Absence Detection ──────────────────────────────────────────

func TestCheckT9_NoTriggerLowBaseline(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricCommitRate)
	bl := &MetricBaseline{Avg: 5, Std: 1, Ready: true} // below MinBaseline(20)

	sample := SentinelSample{Values: map[MetricName]float64{MetricActive: 10}}
	trigger := d.CheckT9(MetricCommitRate, 1, def, bl, sample)
	if trigger != nil {
		t.Error("should not trigger when baseline < MinBaseline")
	}
}

func TestCheckT9_Triggers(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricCommitRate)
	bl := &MetricBaseline{Avg: 100, Std: 10, Ready: true}

	// Active sessions history: needs to be populated for active sync check
	for i := 0; i < 10; i++ {
		d.PushMetricValue(MetricActive, 50) // stable active
	}

	sample := SentinelSample{Values: map[MetricName]float64{MetricActive: 50}}

	// Commit rate drops from 100 to 10 (90% drop, > 80% threshold)
	for i := 0; i < 4; i++ {
		trigger := d.CheckT9(MetricCommitRate, 10, def, bl, sample)
		if trigger != nil {
			t.Errorf("tick %d: should not trigger yet (need 5 sustained)", i+1)
		}
	}

	trigger := d.CheckT9(MetricCommitRate, 10, def, bl, sample)
	if trigger == nil {
		t.Fatal("should trigger after 5 sustained ticks of >80% drop")
	}
}

func TestCheckT9_SuppressedByActiveDrop(t *testing.T) {
	d := NewDetectorState()
	def := GetMetricDef(MetricCommitRate)
	bl := &MetricBaseline{Avg: 100, Std: 10, Ready: true}

	// Active sessions baseline ~50, but now dropped to 10 (business low)
	for i := 0; i < 10; i++ {
		d.PushMetricValue(MetricActive, 50)
	}

	sample := SentinelSample{Values: map[MetricName]float64{MetricActive: 10}} // active also low

	for i := 0; i < 10; i++ {
		trigger := d.CheckT9(MetricCommitRate, 10, def, bl, sample)
		if trigger != nil {
			t.Error("should be suppressed when active also drops (business low)")
		}
	}
}

// ── DetectExtended ─────────────────────────────────────────────────

func TestDetectExtended_NoTriggerEmpty(t *testing.T) {
	d := NewDetectorState()
	sample := SentinelSample{Values: make(map[MetricName]float64)}
	baseline := Baseline{
		Metrics: make(map[MetricName]*MetricBaseline),
		Ready:   true,
	}
	cfg := DefaultConfig()

	trigger := d.DetectExtended(sample, baseline, cfg)
	if trigger != nil {
		t.Error("empty sample should not trigger")
	}
}

func TestDetectExtended_T6Trigger(t *testing.T) {
	d := NewDetectorState()
	sample := SentinelSample{
		Values: map[MetricName]float64{
			MetricTablespaceUsedPct: 97, // above red threshold 95
		},
	}
	baseline := Baseline{
		Metrics: make(map[MetricName]*MetricBaseline),
		Ready:   true,
	}
	cfg := DefaultConfig()

	trigger := d.DetectExtended(sample, baseline, cfg)
	if trigger == nil {
		t.Fatal("tablespace at 97% should trigger T6")
	}
	if trigger.Metric != string(MetricTablespaceUsedPct) {
		t.Errorf("metric = %q, want tablespace_used_pct", trigger.Metric)
	}
}

// ── Metric Registry ────────────────────────────────────────────────

func TestAllMetricDefs_Count(t *testing.T) {
	defs := AllMetricDefs()
	if len(defs) != 48 {
		t.Errorf("metric count = %d, want 48", len(defs))
	}
}

func TestMetricsByTier(t *testing.T) {
	fast := MetricsByTier(ProbeFast)
	medium := MetricsByTier(ProbeMedium)
	slow := MetricsByTier(ProbeSlow)

	total := len(fast) + len(medium) + len(slow)
	if total != 48 {
		t.Errorf("fast(%d) + medium(%d) + slow(%d) = %d, want 48",
			len(fast), len(medium), len(slow), total)
	}
}

func TestGetMetricDef_Exists(t *testing.T) {
	def := GetMetricDef(MetricActive)
	if def == nil {
		t.Fatal("MetricActive should be in registry")
	}
	if def.Label != "活跃会话Active Sessions" {
		t.Errorf("label = %q, want 活跃会话Active Sessions", def.Label)
	}
	if def.Unit != "个" {
		t.Errorf("unit = %q, want 个", def.Unit)
	}
}

func TestGetMetricDef_NotExists(t *testing.T) {
	def := GetMetricDef(MetricName("nonexistent"))
	if def != nil {
		t.Error("nonexistent metric should return nil")
	}
}

func TestMetricDef_HasStrategy(t *testing.T) {
	def := GetMetricDef(MetricActive)
	if !def.HasStrategy(StrategyT1) {
		t.Error("MetricActive should have T1")
	}
	if !def.HasStrategy(StrategyT2) {
		t.Error("MetricActive should have T2")
	}
	if def.HasStrategy(StrategyT3) {
		t.Error("MetricActive should not have T3")
	}
}

// ── PopulateValues ─────────────────────────────────────────────────

func TestPopulateValues(t *testing.T) {
	s := SentinelSample{
		ActiveNonIdle:   10,
		OnCPU:           3,
		IOWait:          2,
		LockWait:        1,
		LongSQL:         0,
		RedoKBPerSec:    5000,
		HardParsePerSec: 50,
	}
	s.PopulateValues()

	if s.Values[MetricActive] != 10 {
		t.Errorf("active = %f, want 10", s.Values[MetricActive])
	}
	if s.Values[MetricRedoRate] != 5000 {
		t.Errorf("redo = %f, want 5000", s.Values[MetricRedoRate])
	}
}

// ── Extended Threshold Tests ───────────────────────────────────────

func TestSoftAbsoluteMin_NewMetrics(t *testing.T) {
	hw := HardwareProfile{CPUCores: 8}

	tests := []struct {
		metric MetricName
		want   float64
	}{
		{MetricLogFileSyncUs, 5000},
		{MetricDbFileSeqReadUs, 10000},
		{MetricBlockingChains, 2},
		{MetricEnqueueDeadlocks, 1},
		{MetricLogSwitchRate, 10},
		{MetricPlanChangeCount, 3},
	}

	for _, tt := range tests {
		got := SoftAbsoluteMin(tt.metric, hw)
		if got != tt.want {
			t.Errorf("SoftAbsoluteMin(%s) = %v, want %v", tt.metric, got, tt.want)
		}
	}
}

func TestSoftAbsoluteMin_CapacityMetricsAreZero(t *testing.T) {
	hw := HardwareProfile{CPUCores: 8}
	capacityMetrics := []MetricName{
		MetricTablespaceUsedPct, MetricTempUsedPct, MetricUndoUsedPct,
		MetricPGAUsedPct, MetricBufferCacheHit,
	}
	for _, m := range capacityMetrics {
		if got := SoftAbsoluteMin(m, hw); got != 0 {
			t.Errorf("SoftAbsoluteMin(%s) = %v, want 0 (uses T6/T8)", m, got)
		}
	}
}
