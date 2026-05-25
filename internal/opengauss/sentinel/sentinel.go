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
 *	  internal/opengauss/sentinel/sentinel.go
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

// Sentinel is the background anomaly detection engine for OpenGauss.
type Sentinel struct {
	cfg   Config
	fast  *FastProbeCollector
	med   *MediumProbeCollector
	slow  *SlowProbeCollector
	alertCh chan alert.Event

	mu              sync.Mutex
	state           State
	baseline        Baseline
	lastTrigger     time.Time
	sustainedCounts map[MetricName]int
	tickCount       int64
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
	if cfg.CooldownPeriod <= 0 {
		cfg.CooldownPeriod = 5 * time.Minute
	}

	return &Sentinel{
		cfg:     cfg,
		fast:    fast,
		med:     med,
		slow:    slow,
		alertCh: make(chan alert.Event, 4),
		baseline: Baseline{
			Window:  cfg.BaselineWindow,
			Metrics: make(map[MetricName]*MetricBaseline),
		},
		sustainedCounts: make(map[MetricName]int),
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

		trigger := s.detectAnomaly(sample)
		if trigger != nil {
			s.state = StateBurst
			s.lastTrigger = nowFunc()
			// Run burst collection (simplified: collect for BurstDuration).
			s.mu.Unlock()
			s.runBurst(ctx, *trigger)
			s.mu.Lock()
		}

	case StateBurst:
		// Burst runs independently.

	case StateCooldown:
		s.baseline = pushSample(&s.baseline, sample)
		if nowFunc().Sub(s.lastTrigger) >= s.cfg.CooldownPeriod {
			s.state = StateWatch
			s.resetSustainedCounts()
		}
	}
}

// detectAnomaly checks all metrics against 3-sigma thresholds.
func (s *Sentinel) detectAnomaly(sample MetricSample) *TriggerEvent {
	if !s.baseline.Ready {
		return nil
	}

	// Cooldown check.
	if !s.lastTrigger.IsZero() && nowFunc().Sub(s.lastTrigger) < s.cfg.CooldownPeriod {
		return nil
	}

	// Check metrics in priority order.
	priority := []MetricName{
		MetricLockWaits, MetricActiveSessions, MetricLongQueries,
		MetricIdleInTransaction, MetricConnectionsPct, MetricDeadTupleRatio,
		MetricXIDAgeRatio, MetricReplicationLag, MetricWALBytesRate,
		MetricCheckpointsReq, MetricBlockerCount, MetricDeadlocks,
		MetricXactCommitRate, MetricTempBytesRate, MetricCacheHitPct,
	}

	for _, metric := range priority {
		current, ok := sample.Values[metric]
		if !ok {
			continue
		}

		bl, ok := s.baseline.Metrics[metric]
		if !ok || !bl.Ready {
			continue
		}

		threshold := bl.Avg + s.cfg.SigmaThreshold*bl.Std
		// Minimum floor to avoid triggering on tiny values.
		minFloor := bl.Avg + 3
		if threshold < minFloor {
			threshold = minFloor
		}

		// Special handling for "lower is worse" metrics (cache hit).
		if isLowerIsWorse(metric) {
			// For cache hit: trigger when current drops below floor.
			floor := bl.Avg - s.cfg.SigmaThreshold*bl.Std
			if floor < 0 {
				floor = 0
			}
			if bl.Avg > 50 && current < floor && current < 90 {
				s.sustainedCounts[metric]++
				if s.sustainedCounts[metric] >= s.cfg.SustainedCount {
					s.resetSustainedCounts()
					multiplier := 1.0
					if bl.Avg > 0 {
						multiplier = current / bl.Avg
					}
					return &TriggerEvent{
						Timestamp:  sample.Timestamp,
						Metric:     string(metric),
						Baseline:   bl.Avg,
						Current:    current,
						Threshold:  floor,
						Multiplier: multiplier,
					}
				}
			} else {
				s.sustainedCounts[metric] = 0
			}
			continue
		}

		if current > threshold {
			s.sustainedCounts[metric]++
			if s.sustainedCounts[metric] >= s.cfg.SustainedCount {
				multiplier := 1.0
				if bl.Avg > 0 {
					multiplier = current / bl.Avg
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

// isLowerIsWorse returns true for metrics where a lower value indicates a problem.
func isLowerIsWorse(metric MetricName) bool {
	return metric == MetricCacheHitPct
}

// resetSustainedCounts clears all sustained counters.
func (s *Sentinel) resetSustainedCounts() {
	for k := range s.sustainedCounts {
		delete(s.sustainedCounts, k)
	}
}

// runBurst collects high-frequency samples for the burst duration,
// then analyzes and emits an alert. Called WITHOUT the mutex held.
func (s *Sentinel) runBurst(ctx context.Context, trigger TriggerEvent) {
	startTime := nowFunc()
	burstEnd := startTime.Add(s.cfg.BurstDuration)
	var frames []MetricSample
	peakActive := 0

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			goto analyze
		case <-s.stopCh:
			goto analyze
		case <-ticker.C:
			fastMetrics, err := s.fast.Probe(ctx)
			if err != nil {
				continue
			}
			sample := MetricSample{
				Timestamp: nowFunc(),
				Values:    fastMetrics,
			}
			frames = append(frames, sample)

			active := int(sample.Values[MetricActiveSessions])
			if active > peakActive {
				peakActive = active
			}

			if nowFunc().After(burstEnd) {
				goto analyze
			}
		}
	}

analyze:
	endTime := nowFunc()
	durationSec := endTime.Sub(startTime).Seconds()

	// Build metrics summary from frames.
	metrics := buildMetricsSummary(frames)

	report := BurstReport{
		TriggerEvent:   trigger,
		DurationSec:    durationSec,
		PeakActive:     peakActive,
		BaselineActive: s.baseline.Metrics[MetricActiveSessions].Avg,
		Metrics:        metrics,
		Frames:         frames,
		StartTime:      startTime,
		EndTime:        endTime,
	}

	// Classify.
	report.Classification = Classify(report)

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
	desc := FormatAlertDescription(trigger, durationSec)
	evt := alert.Event{
		Timestamp:   nowFunc(),
		Description: desc,
		CauseName:   report.Classification.Cause.String(),
		DurationSec: durationSec,
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

	// Collect all metric names.
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

		// Determine trend.
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
