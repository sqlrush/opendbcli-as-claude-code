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
 *	  internal/mysql/sentinel/sentinel.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/db"
)

// State represents the sentinel FSM states.
type State uint8

const (
	StateIdle     State = iota
	StateWatch
	StateBurst
	StateCooldown
)

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

// Sentinel is the background anomaly detection engine for MySQL.
type Sentinel struct {
	cfg    Config
	driver db.Driver
	fast   *FastProbeCollector
	med    *MediumProbeCollector
	slow   *SlowProbeCollector

	alertCh  chan alert.Event
	detector *DetectorState

	mu              sync.Mutex
	state           State
	baseline        Baseline
	lastTrigger     time.Time
	sustainedCounts map[MetricName]int
	suppressed      map[MetricName]float64
	tickCount       int64
	reports         []*BurstReport

	onReport func(BurstReport)

	stopCh  chan struct{}
	stopped chan struct{}
}

const maxReports = 16

// New creates a Sentinel with the given driver and configuration.
func New(cfg Config, driver db.Driver) *Sentinel {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
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
	if cfg.SustainedCount <= 0 {
		cfg.SustainedCount = 3
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

	fast := NewFastProbeCollector(driver)

	return &Sentinel{
		cfg:      cfg,
		driver:   driver,
		fast:     fast,
		med:      NewMediumProbeCollector(driver),
		slow:     NewSlowProbeCollector(driver),
		alertCh:  make(chan alert.Event, 4),
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

func (s *Sentinel) AlertCh() <-chan alert.Event         { return s.alertCh }
func (s *Sentinel) SetOnReport(fn func(BurstReport))    { s.onReport = fn }

func (s *Sentinel) CurrentState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Sentinel) CurrentBaselines() map[MetricName]MetricBaseline {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[MetricName]MetricBaseline, len(s.baseline.Metrics))
	for k, v := range s.baseline.Metrics {
		result[k] = *v
	}
	return result
}

func (s *Sentinel) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseline.Ready
}

func (s *Sentinel) Start(ctx context.Context) { go s.loop(ctx) }

func (s *Sentinel) Stop() {
	close(s.stopCh)
	<-s.stopped
}

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

func (s *Sentinel) tick(ctx context.Context) {
	fastMetrics, err := s.fast.Probe(ctx)
	if err != nil {
		return
	}
	sample := MetricSample{Timestamp: nowFunc(), Values: fastMetrics}

	s.tickCount++
	if s.tickCount%10 == 0 {
		for k, v := range s.med.Probe(ctx) {
			sample.Values[k] = v
		}
	}
	if s.tickCount%30 == 0 {
		for k, v := range s.slow.Probe(ctx) {
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
		s.checkSuppressedRecovery(sample)

		if trigger := s.detectAnomaly(sample); trigger != nil {
			s.suppressed[MetricName(trigger.Metric)] = trigger.Threshold
			s.state = StateBurst
			s.lastTrigger = nowFunc()
			s.mu.Unlock()
			s.startBurst(ctx, *trigger)
			s.mu.Lock()
			return
		}

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

// ── Detection: T1+T2 dual-path ──

func (s *Sentinel) detectAnomaly(sample MetricSample) *TriggerEvent {
	if !s.baseline.Ready {
		return nil
	}
	if !s.lastTrigger.IsZero() && nowFunc().Sub(s.lastTrigger) < s.cfg.CooldownPeriod {
		return nil
	}

	hw := s.cfg.Hardware

	for _, metric := range metricPriorityOrder {
		current, ok := sample.Values[metric]
		if !ok {
			continue
		}
		if _, suppressed := s.suppressed[metric]; suppressed {
			continue
		}
		def := GetMetricDef(metric)
		if def == nil {
			continue
		}
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
			default:
				threshold = bl.Avg + s.cfg.SigmaThreshold*bl.Std
				minFloor := bl.Avg + 3
				if threshold < minFloor {
					threshold = minFloor
				}
				pathA = current > threshold && current >= softMin
			}
		}

		pathB := hasT2 && hardCeil > 0 && current >= hardCeil

		if pathA || pathB {
			sustained := s.cfg.SustainedCount
			if def.ImmediateTrigger {
				sustained = 1
			}
			s.sustainedCounts[metric]++
			if s.sustainedCounts[metric] >= sustained {
				mul := 1.0
				if bl.Avg > 0 {
					mul = current / bl.Avg
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
					Timestamp: sample.Timestamp, Metric: string(metric),
					Baseline: bl.Avg, Current: current, Threshold: threshold,
					Multiplier: mul, Strategy: strategy,
				}
			}
		} else {
			s.sustainedCounts[metric] = 0
		}
	}
	return nil
}

// ── Suppression recovery ──

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

func isLowerIsWorse(metric MetricName) bool {
	def := GetMetricDef(metric)
	if def == nil {
		return false
	}
	if def.HasStrategy(StrategyT8) || def.InvertCapacity || def.HasStrategy(StrategyT9) {
		return true
	}
	return false
}

func (s *Sentinel) resetSustainedCounts() {
	for k := range s.sustainedCounts {
		delete(s.sustainedCounts, k)
	}
}

// ── Burst ──

func (s *Sentinel) startBurst(ctx context.Context, trigger TriggerEvent) {
	bc := NewBurstController(s.cfg, s.fast)
	result := bc.Run(ctx, trigger, s.stopCh)

	topSQLs := CollectTopSQL(ctx, s.driver)
	blockingChains := CollectBlockingChains(ctx, s.driver)
	waitProfile := CollectWaitProfile(ctx, s.driver)

	metrics := buildMetricsSummary(result.Frames)

	s.mu.Lock()
	baselineActive := 0.0
	if bl, ok := s.baseline.Metrics[MetricThreadsRunning]; ok {
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
		TopSQLs:        topSQLs,
		BlockingChains: blockingChains,
		WaitProfile:    waitProfile,
		RawFrameCount:  len(result.Frames),
		StartTime:      result.StartTime,
		EndTime:        result.EndTime,
	}

	report.Classification = Classify(report)
	EnrichReport(ctx, s.driver, &report)

	s.mu.Lock()
	s.reports = append(s.reports, &report)
	if len(s.reports) > maxReports {
		s.reports = s.reports[len(s.reports)-maxReports:]
	}
	s.state = StateCooldown
	s.lastTrigger = nowFunc()
	s.mu.Unlock()

	if s.onReport != nil {
		s.onReport(report)
	}

	desc := FormatAlertDescription(trigger, report.DurationSec)
	blockerSummary := ""
	topBlockers := blockingChains
	if len(topBlockers) > 3 {
		topBlockers = topBlockers[:3]
	}
	for i, b := range topBlockers {
		if i > 0 {
			blockerSummary += ", "
		}
		blockerSummary += fmt.Sprintf("Thread %d(阻塞者) -> %d个等待", b.BlockerThreadID, b.VictimCount)
	}

	evt := alert.Event{
		Timestamp: nowFunc(), Description: desc,
		CauseName: report.Classification.Cause.String(),
		DurationSec: report.DurationSec, BlockerSummary: blockerSummary,
		Report: &report,
	}
	select {
	case s.alertCh <- evt:
	default:
	}
}

func buildMetricsSummary(frames []MetricSample) map[string]MetricSummary {
	if len(frames) == 0 {
		return make(map[string]MetricSummary)
	}
	metricSet := make(map[MetricName]bool)
	for _, f := range frames {
		for k := range f.Values {
			metricSet[k] = true
		}
	}
	result := make(map[string]MetricSummary, len(metricSet))
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
				maxVal, minVal = v, v
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
			fv := frames[0].Values[metric]
			lv := frames[len(frames)-1].Values[metric]
			if lv > fv*1.5 {
				trend = "rising"
			} else if lv < fv*0.5 {
				trend = "falling"
			}
			if maxVal > avg*2 {
				trend = "spike"
			}
		}
		result[string(metric)] = MetricSummary{Avg: avg, Max: maxVal, Min: minVal, Trend: trend}
	}
	return result
}

// ── Report access ──

func (s *Sentinel) Reports() []*BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]*BurstReport, len(s.reports))
	copy(cp, s.reports)
	return cp
}

func (s *Sentinel) ReportAt(idx int) *BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 1 || idx > len(s.reports) {
		return nil
	}
	return s.reports[len(s.reports)-idx]
}

func (s *Sentinel) ReportCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

func (s *Sentinel) LastReport() *BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == 0 {
		return nil
	}
	return s.reports[len(s.reports)-1]
}
