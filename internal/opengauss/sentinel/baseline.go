/*-------------------------------------------------------------------------
 *
 * baseline.go
 *	  Rolling baseline manager for openGauss Sentinel — maintains
 *	  per-metric mean and stddev windows used by the adaptive 3σ
 *	  detector to flag anomalies without static thresholds.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/baseline.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "math"

// pushSample adds a sample to the baseline ring buffer and recomputes stats.
func pushSample(bl *Baseline, sample MetricSample) Baseline {
	newSamples := make([]MetricSample, len(bl.Samples), len(bl.Samples)+1)
	copy(newSamples, bl.Samples)
	newSamples = append(newSamples, sample)

	// Trim to window size.
	if len(newSamples) > bl.Window {
		excess := len(newSamples) - bl.Window
		newSamples = newSamples[excess:]
	}

	result := Baseline{
		Samples: newSamples,
		Window:  bl.Window,
		Ready:   bl.Ready,
		Metrics: make(map[MetricName]*MetricBaseline),
	}

	if len(newSamples) >= 10 {
		result.Ready = true
	}

	recomputeBaseline(&result)
	return result
}

// recomputeBaseline calculates rolling mean and stddev for all metrics
// present in the sample set.
func recomputeBaseline(bl *Baseline) {
	n := len(bl.Samples)
	if n == 0 {
		return
	}

	// Collect all metric names present across samples.
	metricSet := make(map[MetricName]bool)
	for _, s := range bl.Samples {
		for k := range s.Values {
			metricSet[k] = true
		}
	}

	for metric := range metricSet {
		sum := 0.0
		count := 0
		for _, s := range bl.Samples {
			if v, ok := s.Values[metric]; ok {
				sum += v
				count++
			}
		}
		if count == 0 {
			continue
		}

		mean := sum / float64(count)
		variance := 0.0
		for _, s := range bl.Samples {
			if v, ok := s.Values[metric]; ok {
				diff := v - mean
				variance += diff * diff
			}
		}
		std := math.Sqrt(variance / float64(count))

		bl.Metrics[metric] = &MetricBaseline{
			Avg:   mean,
			Std:   std,
			Ready: bl.Ready,
		}
	}
}
