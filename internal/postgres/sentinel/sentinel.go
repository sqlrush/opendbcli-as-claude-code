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
 *	  internal/postgres/sentinel/sentinel.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
)

// State represents the sentinel FSM states.
type State uint8

const (
	StateIdle     State = iota // waiting for baseline to be ready
	StateWatch                 // baseline ready, watching for anomalies
	StateBurst                 // burst collection in progress
	StateCooldown              // post-burst cooldown
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

// Sentinel is the background anomaly detection engine for PostgreSQL.
type Sentinel struct {
	cfg  Config
	fast *FastProbeCollector
	med  *MediumProbeCollector
	slow *SlowProbeCollector

	alertCh chan alert.Event

	// Extended detection state for T3–T9 strategies.
	detector  *DetectorState
	tickCount int64

	mu              sync.Mutex
	state           State
	baseline        Baseline
	lastTrigger     time.Time
	sustainedCounts map[MetricName]int
	suppressed      map[MetricName]float64 // metric → threshold; suppressed until recovered
	stopCh          chan struct{}
	stopped         chan struct{}
	reports         []*BurstReport
}

const maxReports = 16

// New creates a PG Sentinel with the given probes.
func New(
	cfg Config,
	fast *FastProbeCollector,
	med *MediumProbeCollector,
	slow *SlowProbeCollector,
) *Sentinel {
	if cfg.SustainedCount <= 0 {
		cfg.SustainedCount = 3
	}
	if cfg.BaselineWindow <= 0 {
		cfg.BaselineWindow = 60
	}
	if cfg.MinSamples <= 0 {
		cfg.MinSamples = 10
	}
	if cfg.SigmaThreshold <= 0 {
		cfg.SigmaThreshold = 3.0
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.BurstDuration <= 0 {
		cfg.BurstDuration = 30 * time.Second
	}
	if cfg.BurstInterval <= 0 {
		cfg.BurstInterval = 200 * time.Millisecond
	}
	if cfg.BurstCalmDelay <= 0 {
		cfg.BurstCalmDelay = 5 * time.Second
	}
	if cfg.CooldownPeriod <= 0 {
		cfg.CooldownPeriod = 5 * time.Minute
	}

	return &Sentinel{
		cfg:     cfg,
		fast:    fast,
		med:     med,
		slow:    slow,
		alertCh: make(chan alert.Event, 4),
		detector: NewDetectorState(),
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

// CurrentState returns the current FSM state.
func (s *Sentinel) CurrentState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// CurrentBaseline returns a copy of the current baseline.
func (s *Sentinel) CurrentBaseline() Baseline {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseline
}

// IsRunning returns true if sentinel has been started and not stopped.
func (s *Sentinel) IsRunning() bool {
	select {
	case <-s.stopCh:
		return false
	default:
		return true
	}
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
	ticker := time.NewTicker(s.cfg.PollInterval)
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
	// Fast probe (every tick).
	fastMetrics, err := s.fast.Probe(ctx)
	if err != nil {
		return
	}

	sample := MetricSample{
		Timestamp: nowFunc(),
		Values:    fastMetrics,
	}

	// Medium probe (every 10 ticks).
	s.tickCount++
	if s.med != nil && s.tickCount%10 == 0 {
		medMetrics := s.med.Probe(ctx)
		for k, v := range medMetrics {
			sample.Values[k] = v
		}
	}

	// Slow probe (every 30 ticks).
	if s.slow != nil && s.tickCount%30 == 0 {
		slowMetrics := s.slow.Probe(ctx)
		for k, v := range slowMetrics {
			sample.Values[k] = v
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.state {
	case StateIdle:
		s.baseline = pushSample(&s.baseline, sample)
		if s.baseline.Ready {
			s.state = StateWatch
		}

	case StateWatch:
		s.baseline = pushSample(&s.baseline, sample)

		// Check suppressed metric recovery.
		s.checkSuppressedRecovery(sample)

		// T1+T2 detection for all metrics with T1/T2 strategies.
		if trigger := s.detectAnomaly(sample); trigger != nil {
			s.suppressed[MetricName(trigger.Metric)] = trigger.Threshold
			s.state = StateBurst
			s.lastTrigger = nowFunc()
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
				s.lastTrigger = nowFunc()
				s.mu.Unlock()
				s.startBurst(ctx, *trigger)
				s.mu.Lock()
			}
		}

	case StateBurst:
		// Burst runs independently.

	case StateCooldown:
		s.baseline = pushSample(&s.baseline, sample)
		s.checkSuppressedRecovery(sample)
		if nowFunc().Sub(s.lastTrigger) >= s.cfg.CooldownPeriod {
			s.state = StateWatch
			s.resetSustainedCounts()
		}
	}
}

// ──────────────────────────────────────────────────────────────────
// Detection: T1+T2 dual-path
// ──────────────────────────────────────────────────────────────────

// detectAnomaly checks all metrics with T1/T2 strategies against thresholds.
//
// Two trigger paths (both require SustainedCount consecutive ticks):
//
//	Path A (T1 trend anomaly): 3σ/multiplier exceeded AND current ≥ soft absolute min
//	Path B (T2 hard ceiling):  current ≥ hard ceiling (regardless of baseline trend)
func (s *Sentinel) detectAnomaly(sample MetricSample) *TriggerEvent {
	if !s.baseline.Ready {
		return nil
	}

	// Cooldown check.
	if !s.lastTrigger.IsZero() && nowFunc().Sub(s.lastTrigger) < s.cfg.CooldownPeriod {
		return nil
	}

	hw := s.cfg.Hardware

	for _, metric := range metricPriorityOrder {
		current, ok := sample.Values[metric]
		if !ok {
			continue
		}

		// Skip suppressed metrics.
		if _, suppressed := s.suppressed[metric]; suppressed {
			continue
		}

		def := GetMetricDef(metric)
		if def == nil {
			continue
		}

		// Only handle T1/T2 here; T3-T9 are handled by DetectExtended.
		hasT1 := def.HasStrategy(StrategyT1)
		hasT2 := def.HasStrategy(StrategyT2)
		if !hasT1 && !hasT2 {
			continue
		}

		bl, blOK := s.baseline.Metrics[metric]
		if !blOK || !bl.Ready {
			continue
		}

		softMin := SoftAbsoluteMin(metric, hw)
		hardCeil := HardCeiling(metric, hw)

		// Path A: T1 trend anomaly (3σ or fixed multiplier) AND soft absolute min.
		pathA := false
		var threshold float64
		if hasT1 {
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
		}

		// Path B: T2 hard ceiling (absolute danger, ignores baseline).
		pathB := hasT2 && hardCeil > 0 && current >= hardCeil

		if pathA || pathB {
			sustained := s.cfg.SustainedCount
			if def.ImmediateTrigger {
				sustained = 1
			}

			s.sustainedCounts[metric]++
			if s.sustainedCounts[metric] >= sustained {
				multiplier := 1.0
				if bl.Avg > 0 {
					multiplier = current / bl.Avg
				}
				if pathB && !pathA {
					threshold = hardCeil
				}
				strategy := StrategyT1
				if pathB && !pathA {
					strategy = StrategyT2
				}
				s.resetSustainedCounts()
				return &TriggerEvent{
					Timestamp:  sample.Timestamp,
					Metric:     string(metric),
					Baseline:   bl.Avg,
					Current:    current,
					Threshold:  threshold,
					Multiplier: multiplier,
					Strategy:   strategy,
				}
			}
		} else {
			s.sustainedCounts[metric] = 0
		}
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────
// Suppression recovery
// ──────────────────────────────────────────────────────────────────

// checkSuppressedRecovery removes metrics from suppressed set when recovered.
// Spike metrics: recovered when current < threshold * 0.9.
// Lower-is-worse metrics (T8/InvertCapacity): recovered when current >= threshold.
func (s *Sentinel) checkSuppressedRecovery(sample MetricSample) {
	for metric, threshold := range s.suppressed {
		current, found := sample.Values[metric]
		if !found {
			continue
		}

		if isLowerIsWorse(metric) {
			if current >= threshold {
				delete(s.suppressed, metric)
			}
		} else {
			if current < threshold*0.9 {
				delete(s.suppressed, metric)
			}
		}
	}
}

// isLowerIsWorse returns true for metrics where a lower value indicates a problem.
func isLowerIsWorse(metric MetricName) bool {
	def := GetMetricDef(metric)
	if def == nil {
		return false
	}
	if def.HasStrategy(StrategyT8) {
		return true
	}
	if def.InvertCapacity {
		return true
	}
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

// ──────────────────────────────────────────────────────────────────
// Burst: BurstController delegation
// ──────────────────────────────────────────────────────────────────

// startBurst runs burst collection via BurstController.
// Called WITHOUT the mutex held.
func (s *Sentinel) startBurst(ctx context.Context, trigger TriggerEvent) {
	bc := NewBurstController(s.cfg, s.fast)
	result := bc.Run(ctx, trigger, s.stopCh)

	// Collect enrichment data.
	topSQLs := CollectTopSQL(ctx, s.fast.Driver())
	waitProfile := CollectWaitProfile(ctx, s.fast.Driver())
	blockingChains := CollectBlockingChains(ctx, s.fast.Driver())

	// Build metrics summary from frames.
	metrics := buildMetricsSummary(result.Frames)

	s.mu.Lock()
	baselineActive := 0.0
	if bl, ok := s.baseline.Metrics[MetricActiveSessions]; ok {
		baselineActive = bl.Avg
	}
	s.mu.Unlock()

	report := BurstReport{
		TriggerEvent:   trigger,
		DurationSec:    result.EndTime.Sub(result.StartTime).Seconds(),
		PeakActive:     result.PeakActive,
		BaselineActive: baselineActive,
		Metrics:        metrics,
		Frames:         result.Frames,
		StartTime:      result.StartTime,
		EndTime:        result.EndTime,
		TopSQLs:        topSQLs,
		WaitProfile:    waitProfile,
		BlockingChains: blockingChains,
		RawFrameCount:  len(result.Frames),
	}

	// Classify.
	report.Classification = Classify(report)

	// Enrich with space details and params.
	EnrichReport(ctx, s.fast.Driver(), &report)

	// Store report.
	s.mu.Lock()
	s.reports = append(s.reports, &report)
	if len(s.reports) > maxReports {
		s.reports = s.reports[len(s.reports)-maxReports:]
	}
	s.state = StateCooldown
	s.lastTrigger = nowFunc()
	s.mu.Unlock()

	// Emit alert.
	desc := FormatAlertDescription(trigger, report.DurationSec)
	evt := alert.Event{
		Timestamp:   nowFunc(),
		Description: desc,
		CauseName:   report.Classification.Cause.String(),
		DurationSec: report.DurationSec,
		Report:      &report,
	}
	select {
	case s.alertCh <- evt:
	default:
	}
}

// buildMetricsSummary aggregates metric samples into summaries.
func buildMetricsSummary(frames []MetricSample) map[string]MetricSummary {
	if len(frames) == 0 {
		return nil
	}

	metricSet := make(map[MetricName]bool)
	for _, f := range frames {
		for k := range f.Values {
			metricSet[k] = true
		}
	}

	result := make(map[string]MetricSummary)
	for metric := range metricSet {
		var sum, maxVal, minVal float64
		count := 0
		first := true
		for _, f := range frames {
			v, ok := f.Values[metric]
			if !ok {
				continue
			}
			sum += v
			count++
			if first {
				maxVal = v
				minVal = v
				first = false
			} else {
				if v > maxVal {
					maxVal = v
				}
				if v < minVal {
					minVal = v
				}
			}
		}
		if count == 0 {
			continue
		}
		avg := sum / float64(count)

		trend := "stable"
		if len(frames) >= 3 {
			firstVal := frames[0].Values[metric]
			lastVal := frames[len(frames)-1].Values[metric]
			if lastVal > firstVal*1.5 {
				trend = "rising"
			} else if lastVal < firstVal*0.5 {
				trend = "falling"
			}
			if maxVal > avg*2 {
				trend = "spike"
			}
		}

		result[string(metric)] = MetricSummary{
			Avg:   avg,
			Max:   maxVal,
			Min:   minVal,
			Trend: trend,
		}
	}

	return result
}

// ──────────────────────────────────────────────────────────────────
// Report access
// ──────────────────────────────────────────────────────────────────

// Reports returns a copy of all stored reports (newest last).
func (s *Sentinel) Reports() []*BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]*BurstReport, len(s.reports))
	copy(cp, s.reports)
	return cp
}

// ReportAt returns the report at 1-based index (1=newest).
func (s *Sentinel) ReportAt(oneBasedIdx int) *BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if oneBasedIdx < 1 || oneBasedIdx > len(s.reports) {
		return nil
	}
	return s.reports[len(s.reports)-oneBasedIdx]
}

// ReportCount returns the number of stored reports.
func (s *Sentinel) ReportCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

// LastReport returns the most recent burst report, if any.
func (s *Sentinel) LastReport() *BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == 0 {
		return nil
	}
	return s.reports[len(s.reports)-1]
}
