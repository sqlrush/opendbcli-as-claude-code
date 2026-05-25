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
 *	  internal/mysql/sentinel/detector.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"math"
	"time"
)

const defaultHistoryWindow = 30

// MetricHistory is a fixed-size ring buffer that stores recent metric values.
type MetricHistory struct {
	buf  []float64
	pos  int
	full bool
	cap  int
}

func NewMetricHistory(window int) *MetricHistory {
	if window <= 0 {
		window = defaultHistoryWindow
	}
	return &MetricHistory{buf: make([]float64, window), cap: window}
}

func (h *MetricHistory) Push(v float64) {
	h.buf[h.pos] = v
	h.pos++
	if h.pos >= h.cap {
		h.pos = 0
		h.full = true
	}
}

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

func (h *MetricHistory) Len() int {
	if h.full {
		return h.cap
	}
	return h.pos
}

// DetectorState holds per-metric history and sustained counts for T3–T9.
type DetectorState struct {
	histories   map[MetricName]*MetricHistory
	sustainedT3 map[MetricName]int
	sustainedT6 map[MetricName]int
	sustainedT8 map[MetricName]int
	sustainedT9 map[MetricName]int
	alertStates map[MetricName]bool
}

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

func (d *DetectorState) PushMetricValue(metric MetricName, value float64) {
	h, ok := d.histories[metric]
	if !ok {
		h = NewMetricHistory(defaultHistoryWindow)
		d.histories[metric] = h
	}
	h.Push(value)
}

func (d *DetectorState) SetAlertState(metric MetricName, alert bool) {
	d.alertStates[metric] = alert
}

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

// ── T3: Trend ──

const minT3Samples = 10

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
	tw := 3
	if def := GetMetricDef(metric); def != nil && def.TrendWindows > 0 {
		tw = def.TrendWindows
	}
	if d.sustainedT3[metric] < tw {
		return nil
	}
	d.sustainedT3[metric] = 0
	current := vals[len(vals)-1]
	mul := 1.0
	if bl.Avg > 0 {
		mul = current / bl.Avg
	}
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: bl.Avg, Current: current, Threshold: slopeThreshold, Multiplier: mul, Strategy: StrategyT3}
}

// ── T4: Acceleration ──

const minT4Samples = 3

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
	if accel <= bl.Std || current < SoftAbsoluteMin(metric, hw) || current <= bl.Avg+bl.Std {
		return nil
	}
	mul := 1.0
	if bl.Avg > 0 {
		mul = current / bl.Avg
	}
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: bl.Avg, Current: current, Threshold: bl.Avg + bl.Std, Multiplier: mul, Strategy: StrategyT4}
}

// ── T5: Compound ──

func (d *DetectorState) CheckT5(metric MetricName, current float64, def *MetricDef, bl *MetricBaseline) *TriggerEvent {
	if def == nil {
		return nil
	}
	// uptime_since_flush: trigger on restart (uptime < 300s).
	if metric == MetricUptimeSinceFlush {
		if current >= 300 {
			return nil
		}
		return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: 300, Current: current, Threshold: 300, Multiplier: current / 300, Strategy: StrategyT5}
	}
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
	mul := 1.0
	if baseline > 0 {
		mul = current / baseline
	}
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: baseline, Current: current, Threshold: 0, Multiplier: mul, Strategy: StrategyT5}
}

// ── T6: Capacity ──

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
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: def.CapacityRed, Current: current, Threshold: def.CapacityRed, Multiplier: current / def.CapacityRed, Strategy: StrategyT6}
}

// ── T7: Shift ──

const minT7Samples = 20

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
	oldMean := sliceMean(vals[:mid])
	newMean := sliceMean(vals[mid:])
	oldStd := sliceStddev(vals[:mid], oldMean)
	diff := newMean - oldMean
	if diff < 0 {
		diff = -diff
	}
	if diff <= 2*oldStd {
		return nil
	}
	mul := 1.0
	if bl.Avg > 0 {
		mul = newMean / bl.Avg
	}
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: oldMean, Current: newMean, Threshold: oldMean + 2*oldStd, Multiplier: mul, Strategy: StrategyT7}
}

// ── T8: Regression ──

func (d *DetectorState) CheckT8(metric MetricName, current float64, def *MetricDef) *TriggerEvent {
	if def == nil || def.RegressionFloor == 0 {
		return nil
	}
	if current >= def.RegressionFloor {
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
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: def.RegressionFloor, Current: current, Threshold: def.RegressionFloor, Multiplier: current / def.RegressionFloor, Strategy: StrategyT8}
}

// ── T9: Absence ──

func (d *DetectorState) CheckT9(metric MetricName, current float64, def *MetricDef, bl *MetricBaseline, sample MetricSample) *TriggerEvent {
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
	if def.CheckActiveSync {
		if ac, ok := sample.Values[MetricThreadsRunning]; ok {
			if ah, has := d.histories[MetricThreadsRunning]; has && ah.Len() > 0 {
				abl := sliceMean(ah.Values())
				if abl > 0 && ac/abl < 0.5 {
					d.sustainedT9[metric] = 0
					return nil
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
	mul := 1.0
	if bl.Avg > 0 {
		mul = current / bl.Avg
	}
	return &TriggerEvent{Timestamp: time.Now(), Metric: string(metric), Baseline: bl.Avg, Current: current, Threshold: dropLimit, Multiplier: mul, Strategy: StrategyT9}
}

// ── DetectExtended ──

func (d *DetectorState) DetectExtended(sample MetricSample, baseline Baseline, cfg Config, suppressed map[MetricName]float64) *TriggerEvent {
	for _, metric := range metricPriorityOrder {
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
		d.PushMetricValue(metric, current)
		bl := baseline.Metrics[metric]
		for _, strategy := range def.Strategies {
			var trigger *TriggerEvent
			switch strategy {
			case StrategyT1, StrategyT2:
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

func sliceMean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

func sliceStddev(vals []float64, avg float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	v := 0.0
	for _, x := range vals {
		d := x - avg
		v += d * d
	}
	return math.Sqrt(v / float64(len(vals)))
}
