/*-------------------------------------------------------------------------
 *
 * detector.go
 *	  MetricHistory is a fixed-size ring buffer that stores recent
 *	  metric values.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/detector.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"math"
	"time"
)

// ──────────────────────────────────────────────────────────────────
// MetricHistory — per-metric ring buffer for value tracking
// ──────────────────────────────────────────────────────────────────

const defaultHistoryWindow = 30

// MetricHistory is a fixed-size ring buffer that stores recent metric values.
type MetricHistory struct {
	buf  []float64
	pos  int
	full bool
	cap  int
}

// NewMetricHistory creates a ring buffer with the given window size.
func NewMetricHistory(window int) *MetricHistory {
	if window <= 0 {
		window = defaultHistoryWindow
	}
	return &MetricHistory{
		buf: make([]float64, window),
		cap: window,
	}
}

// Push appends a value to the ring buffer.
func (h *MetricHistory) Push(v float64) {
	h.buf[h.pos] = v
	h.pos++
	if h.pos >= h.cap {
		h.pos = 0
		h.full = true
	}
}

// Values returns the stored values in chronological order (oldest first).
func (h *MetricHistory) Values() []float64 {
	n := h.Len()
	if n == 0 {
		return nil
	}
	result := make([]float64, n)
	if h.full {
		copy(result, h.buf[h.pos:])
		copy(result[h.cap-h.pos:], h.buf[:h.pos])
	} else {
		copy(result, h.buf[:h.pos])
	}
	return result
}

// Len returns the number of stored values.
func (h *MetricHistory) Len() int {
	if h.full {
		return h.cap
	}
	return h.pos
}

// ──────────────────────────────────────────────────────────────────
// DetectorState — stateful context for T3–T9 detection strategies
// ──────────────────────────────────────────────────────────────────

// DetectorState holds per-metric history and sustained counts for T3–T9.
type DetectorState struct {
	histories   map[MetricName]*MetricHistory
	sustainedT3 map[MetricName]int
	sustainedT6 map[MetricName]int
	sustainedT8 map[MetricName]int
	sustainedT9 map[MetricName]int
	alertStates map[MetricName]bool
}

// NewDetectorState creates a zeroed DetectorState.
func NewDetectorState() *DetectorState {
	return &DetectorState{
		histories:   make(map[MetricName]*MetricHistory),
		sustainedT3: make(map[MetricName]int),
		sustainedT6: make(map[MetricName]int),
		sustainedT8: make(map[MetricName]int),
		sustainedT9: make(map[MetricName]int),
		alertStates: make(map[MetricName]bool),
	}
}

// PushMetricValue records a value into the metric's ring buffer.
func (d *DetectorState) PushMetricValue(metric MetricName, value float64) {
	h, ok := d.histories[metric]
	if !ok {
		h = NewMetricHistory(defaultHistoryWindow)
		d.histories[metric] = h
	}
	h.Push(value)
}

// SetAlertState marks whether a metric is currently in alert.
func (d *DetectorState) SetAlertState(metric MetricName, alert bool) {
	d.alertStates[metric] = alert
}

// ──────────────────────────────────────────────────────────────────
// Helper — linear regression slope
// ──────────────────────────────────────────────────────────────────

// linearSlope computes the least-squares slope (change per sample) of values.
func linearSlope(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	nf := float64(n)
	var sumX, sumY, sumXY, sumX2 float64
	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	denom := nf*sumX2 - sumX*sumX
	if denom == 0 {
		return 0
	}
	return (nf*sumXY - sumX*sumY) / denom
}

// ──────────────────────────────────────────────────────────────────
// T3 — Trend detection (linear regression slope sustained)
// ──────────────────────────────────────────────────────────────────

const minT3Samples = 10

// CheckT3 detects a sustained upward trend via linear regression slope.
// Triggers when the slope exceeds 0.5 * std per sample for TrendWindows
// consecutive checks.
func (d *DetectorState) CheckT3(metric MetricName, bl *MetricBaseline, cfg Config) *TriggerEvent {
	if bl == nil || !bl.Ready {
		return nil
	}
	h, ok := d.histories[metric]
	if !ok || h.Len() < minT3Samples {
		return nil
	}

	vals := h.Values()
	tail := vals[len(vals)-minT3Samples:]
	slope := linearSlope(tail)

	slopeThreshold := 0.5 * bl.Std
	if slope <= slopeThreshold {
		d.sustainedT3[metric] = 0
		return nil
	}

	d.sustainedT3[metric]++

	trendWindows := 3
	if def := GetMetricDef(metric); def != nil && def.TrendWindows > 0 {
		trendWindows = def.TrendWindows
	}
	if d.sustainedT3[metric] < trendWindows {
		return nil
	}

	d.sustainedT3[metric] = 0

	current := vals[len(vals)-1]
	multiplier := 1.0
	if bl.Avg > 0 {
		multiplier = current / bl.Avg
	}
	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   bl.Avg,
		Current:    current,
		Threshold:  slopeThreshold,
		Multiplier: multiplier,
		Strategy:   StrategyT3,
	}
}

// ──────────────────────────────────────────────────────────────────
// T4 — Acceleration detection (2nd derivative)
// ──────────────────────────────────────────────────────────────────

const minT4Samples = 3

// CheckT4 detects sudden acceleration via the second derivative.
// Triggers when accel > std AND current >= SoftAbsoluteMin AND current > avg+std.
func (d *DetectorState) CheckT4(metric MetricName, current float64, bl *MetricBaseline, hw HardwareProfile) *TriggerEvent {
	if bl == nil || !bl.Ready {
		return nil
	}
	h, ok := d.histories[metric]
	if !ok || h.Len() < minT4Samples {
		return nil
	}

	vals := h.Values()
	n := len(vals)
	accel := vals[n-1] - 2*vals[n-2] + vals[n-3]

	if accel <= bl.Std {
		return nil
	}
	if current < SoftAbsoluteMin(metric, hw) {
		return nil
	}
	if current <= bl.Avg+bl.Std {
		return nil
	}

	multiplier := 1.0
	if bl.Avg > 0 {
		multiplier = current / bl.Avg
	}
	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   bl.Avg,
		Current:    current,
		Threshold:  bl.Avg + bl.Std,
		Multiplier: multiplier,
		Strategy:   StrategyT4,
	}
}

// ──────────────────────────────────────────────────────────────────
// T5 — Compound detection (multi-metric AND)
// ──────────────────────────────────────────────────────────────────

// CheckT5 triggers when all companion metrics are in alert and current > 0.
// Special case: pg_is_in_recovery triggers when value changes to 1 (failover).
func (d *DetectorState) CheckT5(metric MetricName, current float64, def *MetricDef, bl *MetricBaseline) *TriggerEvent {
	if def == nil {
		return nil
	}

	// Special case: pg_is_in_recovery triggers on unexpected state change.
	if metric == MetricPGIsInRecovery {
		// Value 1 = in recovery (standby promoted or unexpected).
		if current != 1 {
			return nil
		}
		return &TriggerEvent{
			Timestamp:  time.Now(),
			Metric:     string(metric),
			Baseline:   0,
			Current:    current,
			Threshold:  1,
			Multiplier: current,
			Strategy:   StrategyT5,
		}
	}

	// General compound: all CompoundWith metrics must be in alert.
	if len(def.CompoundWith) == 0 {
		return nil
	}
	for _, companion := range def.CompoundWith {
		if !d.alertStates[companion] {
			return nil
		}
	}
	if current <= 0 {
		return nil
	}

	baseline := 0.0
	if bl != nil {
		baseline = bl.Avg
	}
	multiplier := 1.0
	if baseline > 0 {
		multiplier = current / baseline
	}
	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   baseline,
		Current:    current,
		Threshold:  0,
		Multiplier: multiplier,
		Strategy:   StrategyT5,
	}
}

// ──────────────────────────────────────────────────────────────────
// T6 — Capacity threshold detection
// ──────────────────────────────────────────────────────────────────

// CheckT6 detects capacity breaches (red threshold).
// For normal metrics: current >= CapacityRed.
// For inverted metrics: current <= CapacityRed.
func (d *DetectorState) CheckT6(metric MetricName, current float64, def *MetricDef) *TriggerEvent {
	if def == nil || def.CapacityRed == 0 {
		return nil
	}

	var breached bool
	if def.InvertCapacity {
		breached = current <= def.CapacityRed
	} else {
		breached = current >= def.CapacityRed
	}

	if !breached {
		d.sustainedT6[metric] = 0
		return nil
	}

	// Red threshold — trigger immediately (no sustained count).
	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   def.CapacityRed,
		Current:    current,
		Threshold:  def.CapacityRed,
		Multiplier: current / def.CapacityRed,
		Strategy:   StrategyT6,
	}
}

// ──────────────────────────────────────────────────────────────────
// T7 — Shift detection (window mean comparison)
// ──────────────────────────────────────────────────────────────────

const minT7Samples = 20

// CheckT7 detects a level shift by comparing means of old/new halves.
// Triggers when |newMean - oldMean| > 2 * oldStd.
func (d *DetectorState) CheckT7(metric MetricName, bl *MetricBaseline) *TriggerEvent {
	if bl == nil || !bl.Ready {
		return nil
	}
	h, ok := d.histories[metric]
	if !ok || h.Len() < minT7Samples {
		return nil
	}

	vals := h.Values()
	n := len(vals)
	mid := n / 2

	oldHalf := vals[:mid]
	newHalf := vals[mid:]

	oldMean := sliceMean(oldHalf)
	newMean := sliceMean(newHalf)
	oldStd := sliceStddev(oldHalf, oldMean)

	diff := newMean - oldMean
	if diff < 0 {
		diff = -diff
	}

	if diff <= 2*oldStd {
		return nil
	}

	multiplier := 1.0
	if bl.Avg > 0 {
		multiplier = newMean / bl.Avg
	}
	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   oldMean,
		Current:    newMean,
		Threshold:  oldMean + 2*oldStd,
		Multiplier: multiplier,
		Strategy:   StrategyT7,
	}
}

// ──────────────────────────────────────────────────────────────────
// T8 — Regression detection (value drops below floor)
// ──────────────────────────────────────────────────────────────────

// CheckT8 detects when a metric regresses past its floor.
// For cache_hit_pct: trigger if current < RegressionFloor.
func (d *DetectorState) CheckT8(metric MetricName, current float64, def *MetricDef) *TriggerEvent {
	if def == nil || def.RegressionFloor == 0 {
		return nil
	}

	// Cache hit, etc: lower is worse.
	regressed := current < def.RegressionFloor

	if !regressed {
		d.sustainedT8[metric] = 0
		return nil
	}

	d.sustainedT8[metric]++

	sustain := def.RegressionSustain
	if sustain <= 0 {
		sustain = 5
	}
	if d.sustainedT8[metric] < sustain {
		return nil
	}

	d.sustainedT8[metric] = 0

	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   def.RegressionFloor,
		Current:    current,
		Threshold:  def.RegressionFloor,
		Multiplier: current / def.RegressionFloor,
		Strategy:   StrategyT8,
	}
}

// ──────────────────────────────────────────────────────────────────
// T9 — Absence detection (rate drops from baseline)
// ──────────────────────────────────────────────────────────────────

// CheckT9 detects when a metric drops significantly from baseline.
// Skips if baseline is below MinBaseline. When CheckActiveSync is true,
// suppresses the alert if active_sessions also dropped proportionally.
func (d *DetectorState) CheckT9(
	metric MetricName,
	current float64,
	def *MetricDef,
	bl *MetricBaseline,
	sample MetricSample,
) *TriggerEvent {
	if def == nil || bl == nil || !bl.Ready {
		return nil
	}

	if bl.Avg < def.MinBaseline {
		d.sustainedT9[metric] = 0
		return nil
	}

	dropLimit := bl.Avg * (1 - def.DropThreshold)
	if current >= dropLimit {
		d.sustainedT9[metric] = 0
		return nil
	}

	// Active-session sync check: suppress if business is legitimately low.
	if def.CheckActiveSync {
		activeCurrent, hasActive := sample.Values[MetricActiveSessions]
		if hasActive {
			activeH, hasHistory := d.histories[MetricActiveSessions]
			if hasHistory && activeH.Len() > 0 {
				activeVals := activeH.Values()
				activeBaseline := sliceMean(activeVals)
				if activeBaseline > 0 {
					activeRatio := activeCurrent / activeBaseline
					if activeRatio < 0.5 {
						d.sustainedT9[metric] = 0
						return nil
					}
				}
			}
		}
	}

	d.sustainedT9[metric]++

	sustain := def.AbsenceSustain
	if sustain <= 0 {
		sustain = 5
	}
	if d.sustainedT9[metric] < sustain {
		return nil
	}

	d.sustainedT9[metric] = 0

	multiplier := 1.0
	if bl.Avg > 0 {
		multiplier = current / bl.Avg
	}
	return &TriggerEvent{
		Timestamp:  time.Now(),
		Metric:     string(metric),
		Baseline:   bl.Avg,
		Current:    current,
		Threshold:  dropLimit,
		Multiplier: multiplier,
		Strategy:   StrategyT9,
	}
}

// ──────────────────────────────────────────────────────────────────
// DetectExtended — main entry point for T3–T9
// ──────────────────────────────────────────────────────────────────

// DetectExtended iterates metrics in priority order and runs T3–T9 checks.
// Returns the first TriggerEvent found, or nil if all metrics are normal.
func (d *DetectorState) DetectExtended(sample MetricSample, baseline Baseline, cfg Config, suppressed map[MetricName]float64) *TriggerEvent {
	for _, metric := range metricPriorityOrder {
		// Skip suppressed metrics.
		if suppressed != nil {
			if _, ok := suppressed[metric]; ok {
				continue
			}
		}

		current, present := sample.Values[metric]
		if !present {
			continue
		}

		def := GetMetricDef(metric)
		if def == nil {
			continue
		}

		// Push value into history.
		d.PushMetricValue(metric, current)

		bl := baseline.Metrics[metric]

		for _, strategy := range def.Strategies {
			var trigger *TriggerEvent

			switch strategy {
			case StrategyT1, StrategyT2:
				// Handled by sentinel.go detectAnomaly; skip here.
				continue
			case StrategyT3:
				trigger = d.CheckT3(metric, bl, cfg)
			case StrategyT4:
				trigger = d.CheckT4(metric, current, bl, cfg.Hardware)
			case StrategyT5:
				trigger = d.CheckT5(metric, current, def, bl)
			case StrategyT6:
				trigger = d.CheckT6(metric, current, def)
			case StrategyT7:
				trigger = d.CheckT7(metric, bl)
			case StrategyT8:
				trigger = d.CheckT8(metric, current, def)
			case StrategyT9:
				trigger = d.CheckT9(metric, current, def, bl, sample)
			}

			if trigger != nil {
				return trigger
			}
		}
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────────────────────

// sliceMean computes the arithmetic mean of a float64 slice.
func sliceMean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// sliceStddev computes the population standard deviation given a precomputed mean.
func sliceStddev(vals []float64, avg float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	variance := 0.0
	for _, v := range vals {
		diff := v - avg
		variance += diff * diff
	}
	return math.Sqrt(variance / float64(len(vals)))
}
