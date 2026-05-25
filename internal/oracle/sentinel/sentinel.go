/*-------------------------------------------------------------------------
 *
 * sentinel.go
 *	  State represents the sentinel FSM states.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/sentinel.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
)

// State represents the sentinel FSM states.
type State uint8

const (
	StateIdle    State = iota // waiting for baseline to be ready
	StateWatch               // baseline ready, watching for anomalies
	StateBurst               // burst collection in progress
	StateCooldown            // post-burst cooldown
)

// String returns a human-readable state name.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateWatch:
		return "watch"
	case StateBurst:
		return "burst"
	case StateCooldown:
		return "cooldown"
	default:
		return "unknown"
	}
}

// ProbeFunc is the function sentinel calls to collect a lightweight sample.
// In production this wraps Collector.Collect; in tests a stub can be injected.
type ProbeFunc func(ctx context.Context) dbtop.Snapshot

// OnTriggerFunc is called when an anomaly is detected, before burst starts.
type OnTriggerFunc func(trigger TriggerEvent)

// OnReportFunc is called when a burst analysis is complete.
type OnReportFunc func(report BurstReport)

// Sentinel is the background anomaly detection engine.
type Sentinel struct {
	cfg            Config
	probe          ProbeFunc // full collector probe (used in burst mode)
	probeCollector *ProbeCollector // lightweight probe (used in watch mode)
	onTrigger      OnTriggerFunc
	onReport       OnReportFunc
	collector      *dbtop.Collector
	alertCh        chan alert.Event

	// Tiered probes (optional, set via SetMediumProbe/SetSlowProbe).
	mediumCollector *MediumProbeCollector
	slowCollector   *SlowProbeCollector

	// Extended detection state for T3–T9 strategies.
	detector  *DetectorState
	tickCount int64

	mu              sync.Mutex
	state           State
	baseline        Baseline
	lastTrigger     time.Time
	currentBurst    *BurstController
	sustainedCounts map[MetricName]int     // consecutive ticks each metric exceeded threshold
	suppressed      map[MetricName]float64 // metric → threshold; suppressed until recovered
	stopCh          chan struct{}
	stopped         chan struct{}
}

// New creates a Sentinel with the given probes and callbacks.
// probeCollector is the lightweight probe (1 SQL/s, used in watch mode).
// fullProbe is the heavy collector (used in burst mode).
func New(cfg Config, probeCollector *ProbeCollector, fullProbe ProbeFunc, collector *dbtop.Collector) *Sentinel {
	sustainedCount := cfg.SustainedCount
	if sustainedCount <= 0 {
		sustainedCount = 3
	}
	cfg.SustainedCount = sustainedCount

	return &Sentinel{
		cfg:            cfg,
		probeCollector: probeCollector,
		probe:          fullProbe,
		collector:      collector,
		alertCh:        make(chan alert.Event, 4),
		detector:       NewDetectorState(),
		baseline: Baseline{
			Window:  cfg.BaselineWindow,
			Metrics: make(map[MetricName]*MetricBaseline),
		},
		sustainedCounts: make(map[MetricName]int),
		suppressed:      make(map[MetricName]float64),
		stopCh:          make(chan struct{}),
		stopped:         make(chan struct{}),
	}
}

// AlertCh returns the channel that receives alert events.
func (s *Sentinel) AlertCh() <-chan alert.Event {
	return s.alertCh
}

// SetOnTrigger sets the callback for anomaly triggers.
func (s *Sentinel) SetOnTrigger(fn OnTriggerFunc) { s.onTrigger = fn }

// SetOnReport sets the callback for completed burst reports.
func (s *Sentinel) SetOnReport(fn OnReportFunc) { s.onReport = fn }

// SetMediumProbe sets the medium-tier (10s) probe collector.
func (s *Sentinel) SetMediumProbe(m *MediumProbeCollector) { s.mediumCollector = m }

// SetSlowProbe sets the slow-tier (30s) probe collector.
func (s *Sentinel) SetSlowProbe(sl *SlowProbeCollector) { s.slowCollector = sl }

// State returns the current FSM state.
func (s *Sentinel) CurrentState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Baseline returns a copy of the current baseline.
func (s *Sentinel) CurrentBaseline() Baseline {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseline
}

// Start begins the sentinel loop in a goroutine.
func (s *Sentinel) Start(ctx context.Context) {
	go s.loop(ctx)
}

// Stop signals the sentinel to stop and waits for it.
func (s *Sentinel) Stop() {
	close(s.stopCh)
	<-s.stopped
}

// loop is the main sentinel event loop.
func (s *Sentinel) loop(ctx context.Context) {
	defer close(s.stopped)
	interval := s.cfg.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick handles one probe cycle.
func (s *Sentinel) tick(ctx context.Context) {
	// Build sample from lightweight probe or fallback to full probe (for tests).
	var sample SentinelSample

	if s.probeCollector != nil {
		probeResult, sysDelta, err := s.probeCollector.Probe(ctx)
		if err != nil {
			return // skip this tick on error
		}
		sample = SentinelSample{
			Timestamp:     probeResult.Timestamp,
			ActiveNonIdle: probeResult.Active,
			OnCPU:         probeResult.OnCPU,
			IOWait:        probeResult.IOWait,
			LockWait:      probeResult.LockWait,
			LongSQL:       probeResult.LongSQL,
		}
		if sysDelta.Valid {
			sample.RedoKBPerSec = sysDelta.RedoKBPerSec
			sample.HardParsePerSec = sysDelta.HardParsePerSec
		}
	} else if s.probe != nil {
		// Fallback for tests: use the full probe function directly.
		snap := s.probe(ctx)
		sample = SentinelSample{
			Timestamp:     snap.Timestamp,
			ActiveNonIdle: snap.ActiveCount,
		}
	} else {
		return
	}

	// Populate the Values map from legacy fields.
	sample.PopulateValues()

	// Tiered probing: medium (every 10 ticks) and slow (every 30 ticks).
	// These run outside the mutex since they execute SQL queries.
	s.tickCount++
	if s.mediumCollector != nil && s.tickCount%10 == 0 {
		mediumValues := s.mediumCollector.Probe(ctx)
		for k, v := range mediumValues {
			sample.Values[k] = v
		}
	}
	if s.slowCollector != nil && s.tickCount%30 == 0 {
		slowValues := s.slowCollector.Probe(ctx)
		for k, v := range slowValues {
			sample.Values[k] = v
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.state {
	case StateIdle:
		s.pushSample(sample)
		if s.baseline.Ready {
			s.state = StateWatch
		}

	case StateWatch:
		s.pushSample(sample)

		// Check if any suppressed metric has recovered.
		s.checkSuppressedRecovery(sample)

		// T1+T2 detection for original 7 fast-tier metrics.
		if trigger := s.detectAnomaly(sample); trigger != nil {
			s.suppressed[MetricName(trigger.Metric)] = trigger.Threshold
			s.state = StateBurst
			s.lastTrigger = time.Now()
			s.mu.Unlock()
			s.startBurst(ctx, *trigger)
			s.mu.Lock()
			return
		}

		// T3–T9 extended detection for all metrics.
		if s.detector != nil {
			if trigger := s.detector.DetectExtended(sample, s.baseline, s.cfg, s.suppressed); trigger != nil {
				s.suppressed[MetricName(trigger.Metric)] = trigger.Threshold
				s.state = StateBurst
				s.lastTrigger = time.Now()
				s.mu.Unlock()
				s.startBurst(ctx, *trigger)
				s.mu.Lock()
			}
		}

	case StateBurst:
		// Burst controller runs independently; nothing to do in tick.

	case StateCooldown:
		// Check if any suppressed metric has recovered (current < threshold * 0.9).
		s.checkSuppressedRecovery(sample)

		if time.Since(s.lastTrigger) >= s.cfg.CooldownPeriod {
			s.state = StateWatch
			s.resetSustainedCounts()
		}
	}
}

// pushSample adds a sample to the baseline ring buffer and recomputes stats.
func (s *Sentinel) pushSample(sample SentinelSample) {
	s.baseline.Samples = append(s.baseline.Samples, sample)

	// Trim to window size
	if len(s.baseline.Samples) > s.baseline.Window {
		excess := len(s.baseline.Samples) - s.baseline.Window
		s.baseline.Samples = s.baseline.Samples[excess:]
	}

	if len(s.baseline.Samples) >= s.cfg.MinSamples {
		s.baseline.Ready = true
	}

	s.recomputeBaseline()
}

// recomputeBaseline calculates rolling mean and stddev for all metrics.
func (s *Sentinel) recomputeBaseline() {
	n := len(s.baseline.Samples)
	if n == 0 {
		return
	}

	// Helper to compute mean+std for a metric extractor.
	computeStats := func(extract func(SentinelSample) float64) (float64, float64) {
		sum := 0.0
		for _, sample := range s.baseline.Samples {
			sum += extract(sample)
		}
		mean := sum / float64(n)
		variance := 0.0
		for _, sample := range s.baseline.Samples {
			diff := extract(sample) - mean
			variance += diff * diff
		}
		return mean, math.Sqrt(variance / float64(n))
	}

	// Active (legacy fields for compatibility).
	s.baseline.AvgActive, s.baseline.StdActive = computeStats(func(s SentinelSample) float64 {
		return float64(s.ActiveNonIdle)
	})

	// Multi-metric baselines.
	extractors := map[MetricName]func(SentinelSample) float64{
		MetricActive:    func(s SentinelSample) float64 { return float64(s.ActiveNonIdle) },
		MetricCPU:       func(s SentinelSample) float64 { return float64(s.OnCPU) },
		MetricIO:        func(s SentinelSample) float64 { return float64(s.IOWait) },
		MetricLock:      func(s SentinelSample) float64 { return float64(s.LockWait) },
		MetricLongSQL:   func(s SentinelSample) float64 { return float64(s.LongSQL) },
		MetricRedoRate:  func(s SentinelSample) float64 { return s.RedoKBPerSec },
		MetricHardParse: func(s SentinelSample) float64 { return s.HardParsePerSec },
	}

	for name, extract := range extractors {
		avg, std := computeStats(extract)
		s.baseline.Metrics[name] = &MetricBaseline{Avg: avg, Std: std, Ready: s.baseline.Ready}
	}
}

// detectAnomaly checks all metrics against thresholds and sustained counts.
//
// Two trigger paths (both require SustainedCount consecutive ticks):
//
//	Path A (trend anomaly): 3σ/multiplier exceeded AND current ≥ soft absolute min
//	Path B (hard ceiling):  current ≥ hard ceiling (regardless of baseline trend)
//
// Returns the first TriggerEvent (by priority) whose sustained count is reached.
func (s *Sentinel) detectAnomaly(sample SentinelSample) *TriggerEvent {
	if !s.baseline.Ready {
		return nil
	}

	// Cooldown check.
	if !s.lastTrigger.IsZero() && time.Since(s.lastTrigger) < s.cfg.CooldownPeriod {
		return nil
	}

	hw := s.cfg.Hardware

	// Check metrics in deterministic priority order.
	type metricCheck struct {
		name  MetricName
		value float64
	}
	checks := []metricCheck{
		{MetricActive, float64(sample.ActiveNonIdle)},
		{MetricCPU, float64(sample.OnCPU)},
		{MetricIO, float64(sample.IOWait)},
		{MetricLock, float64(sample.LockWait)},
		{MetricLongSQL, float64(sample.LongSQL)},
		{MetricRedoRate, sample.RedoKBPerSec},
		{MetricHardParse, sample.HardParsePerSec},
	}

	for _, check := range checks {
		metric := check.name
		current := check.value

		// Skip suppressed metrics (already triggered, not yet recovered).
		if _, suppressed := s.suppressed[metric]; suppressed {
			continue
		}

		bl, ok := s.baseline.Metrics[metric]
		if !ok || !bl.Ready {
			continue
		}

		softMin := SoftAbsoluteMin(metric, hw)
		hardCeil := HardCeiling(metric, hw)

		// Path A: trend anomaly (3σ or fixed multiplier) AND soft absolute min.
		pathA := false
		var threshold float64
		switch s.cfg.TriggerMode {
		case TriggerFixed:
			ft, exists := s.cfg.FixedThresholds[metric]
			if exists && ft.Enabled {
				if ft.Absolute > 0 {
					threshold = ft.Absolute
					pathA = current > threshold && current >= softMin
				} else if ft.Multiplier > 0 && bl.Avg > 0 {
					threshold = bl.Avg * ft.Multiplier
					pathA = current > threshold && current >= softMin
				}
			}
		default: // TriggerAdaptive
			threshold = bl.Avg + s.cfg.SigmaThreshold*bl.Std
			minFloor := bl.Avg + 3
			if threshold < minFloor {
				threshold = minFloor
			}
			pathA = current > threshold && current >= softMin
		}

		// Path B: hard ceiling (absolute danger, ignores baseline trend).
		pathB := current >= hardCeil

		if pathA || pathB {
			s.sustainedCounts[metric]++
			if s.sustainedCounts[metric] >= s.cfg.SustainedCount {
				// Sustained threshold reached — fire trigger.
				multiplier := 1.0
				if bl.Avg > 0 {
					multiplier = current / bl.Avg
				}
				if pathB && !pathA {
					threshold = hardCeil
				}
				s.resetSustainedCounts()
				return &TriggerEvent{
					Timestamp:  sample.Timestamp,
					Metric:     string(metric),
					Baseline:   bl.Avg,
					Current:    current,
					Threshold:  threshold,
					Multiplier: multiplier,
				}
			}
		} else {
			s.sustainedCounts[metric] = 0
		}
	}

	return nil
}

// checkSuppressedRecovery removes metrics from the suppressed set when they
// genuinely recover. For "spike" metrics (higher = worse), recovery means
// dropping below 90% of the trigger threshold. For "regression" metrics
// (lower = worse, e.g. cache hit rates), recovery means rising ABOVE the floor.
func (s *Sentinel) checkSuppressedRecovery(sample SentinelSample) {
	for metric, threshold := range s.suppressed {
		// Check fast-tier metrics from sample fields.
		var current float64
		var found bool
		switch metric {
		case MetricActive:
			current, found = float64(sample.ActiveNonIdle), true
		case MetricCPU:
			current, found = float64(sample.OnCPU), true
		case MetricIO:
			current, found = float64(sample.IOWait), true
		case MetricLock:
			current, found = float64(sample.LockWait), true
		case MetricLongSQL:
			current, found = float64(sample.LongSQL), true
		case MetricRedoRate:
			current, found = sample.RedoKBPerSec, true
		case MetricHardParse:
			current, found = sample.HardParsePerSec, true
		default:
			// Extended metrics: check sample.Values map.
			if v, ok := sample.Values[metric]; ok {
				current, found = v, true
			}
		}

		if !found {
			continue
		}

		// Determine recovery direction based on metric type.
		if isLowerIsWorseMetric(metric) {
			// Regression metrics: recovered when value rises ABOVE the floor.
			if current >= threshold {
				delete(s.suppressed, metric)
			}
		} else {
			// Spike metrics: recovered when value drops below 90% of threshold.
			if current < threshold*0.9 {
				delete(s.suppressed, metric)
			}
		}
	}
}

// isLowerIsWorseMetric returns true for metrics where a lower value indicates
// a problem (e.g., cache hit rates, free space percentages).
func isLowerIsWorseMetric(metric MetricName) bool {
	def := GetMetricDef(metric)
	if def == nil {
		return false
	}
	// T8 regression metrics (floor-based): lower is worse.
	if def.HasStrategy(StrategyT8) {
		return true
	}
	// T6 inverted capacity (e.g., Shared Pool Free %): lower is worse.
	if def.InvertCapacity {
		return true
	}
	// T9 absence metrics (rate drop): lower is worse.
	if def.HasStrategy(StrategyT9) {
		return true
	}
	return false
}

// resetSustainedCounts clears all sustained counters.
func (s *Sentinel) resetSustainedCounts() {
	for k := range s.sustainedCounts {
		delete(s.sustainedCounts, k)
	}
}

// startBurst switches to burst mode and runs burst collection.
// Called WITHOUT the mutex held.
func (s *Sentinel) startBurst(ctx context.Context, trigger TriggerEvent) {
	if s.onTrigger != nil {
		s.onTrigger(trigger)
	}

	// Switch collector to burst mode
	s.collector.SetMode(dbtop.ModeBurst)

	bc := NewBurstController(s.cfg, s.probe)
	s.mu.Lock()
	s.currentBurst = bc
	s.mu.Unlock()

	result := bc.Run(ctx, trigger)

	// Switch collector back to normal mode
	s.collector.SetMode(dbtop.ModeNormal)

	// Analyze and classify
	s.mu.Lock()
	baseline := s.baseline
	s.currentBurst = nil
	s.state = StateCooldown
	s.lastTrigger = time.Now()
	s.mu.Unlock()

	report := Analyze(result, baseline)

	// Enrich with Block G (space details) and Block H (related params).
	if s.probeCollector != nil {
		EnrichReport(ctx, s.probeCollector.Driver(), &report)
	}

	if s.onReport != nil {
		s.onReport(report)
	}

	// Push alert to REPL (non-blocking).
	// Pre-format description so shell layer doesn't need sentinel internals.
	desc := FormatAlertDescription(report.TriggerEvent, report.DurationSec)

	blockerSummary := ""
	topBlockers := report.BlockingChains
	if len(topBlockers) > 3 {
		topBlockers = topBlockers[:3]
	}
	for i, b := range topBlockers {
		if i > 0 {
			blockerSummary += ", "
		}
		blockerSummary += fmt.Sprintf("SID %d(根阻塞者) → %d个等待", b.RootSID, b.VictimCount)
	}

	evt := alert.Event{
		Timestamp:      time.Now(),
		Description:    desc,
		CauseName:      report.Classification.Cause.String(),
		DurationSec:    report.DurationSec,
		BlockerSummary: blockerSummary,
		Report:         &report,
	}
	select {
	case s.alertCh <- evt:
	default:
		// Channel full, skip alert (REPL not consuming).
	}
}
