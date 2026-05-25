/*-------------------------------------------------------------------------
 *
 * burst.go
 *	  BurstResult is the output of a burst collection window.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/burst.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"time"
)

// BurstResult is the output of a burst collection window.
type BurstResult struct {
	Trigger     TriggerEvent   `json:"trigger"`
	Frames      []MetricSample `json:"-"`
	StartTime   time.Time      `json:"start_time"`
	EndTime     time.Time      `json:"end_time"`
	PeakActive  int            `json:"peak_active"`
	RecoverTime time.Time      `json:"recover_time"`
}

// BurstController manages a single burst collection window.
type BurstController struct {
	cfg  Config
	fast *FastProbeCollector
}

func NewBurstController(cfg Config, fast *FastProbeCollector) *BurstController {
	return &BurstController{cfg: cfg, fast: fast}
}

// Run executes burst collection: 200ms frames, early termination via BurstCalmDelay.
func (bc *BurstController) Run(ctx context.Context, trigger TriggerEvent, stopCh <-chan struct{}) BurstResult {
	startTime := nowFunc()
	burstEnd := startTime.Add(bc.cfg.BurstDuration)
	maxFrames := int(bc.cfg.BurstDuration/bc.cfg.BurstInterval) + 1
	frames := make([]MetricSample, 0, maxFrames)
	peakActive := 0

	calmStart := time.Time{}
	calmThreshold := trigger.Baseline*1.5 + 2

	ticker := time.NewTicker(bc.cfg.BurstInterval)
	defer ticker.Stop()

	result := BurstResult{Trigger: trigger, StartTime: startTime}

	for {
		select {
		case <-ctx.Done():
			goto done
		case <-stopCh:
			goto done
		case <-ticker.C:
			fastMetrics, err := bc.fast.Probe(ctx)
			if err != nil {
				continue
			}
			sample := MetricSample{Timestamp: nowFunc(), Values: fastMetrics}
			frames = append(frames, sample)

			if active, ok := fastMetrics[MetricThreadsRunning]; ok {
				if int(active) > peakActive {
					peakActive = int(active)
				}
			}

			currentActive := 0.0
			if v, ok := fastMetrics[MetricThreadsRunning]; ok {
				currentActive = v
			}
			if currentActive < calmThreshold {
				if calmStart.IsZero() {
					calmStart = nowFunc()
				} else if nowFunc().Sub(calmStart) >= bc.cfg.BurstCalmDelay {
					result.RecoverTime = calmStart
					goto done
				}
			} else {
				calmStart = time.Time{}
			}

			if nowFunc().After(burstEnd) {
				goto done
			}
		}
	}

done:
	result.EndTime = nowFunc()
	result.Frames = frames
	result.PeakActive = peakActive
	return result
}
