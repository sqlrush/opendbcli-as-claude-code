/*-------------------------------------------------------------------------
 *
 * burst.go
 *	  BurstController manages high-frequency data collection during an
 *	  anomaly. It collects frames at BurstInterval (200ms) for up to
 *	  BurstDuration (30s), with early exit when calm is detected for
 *	  BurstCalmDelay (5s).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/burst.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"time"
)

// BurstController manages high-frequency data collection during an anomaly.
// It collects frames at BurstInterval (200ms) for up to BurstDuration (30s),
// with early exit when calm is detected for BurstCalmDelay (5s).
type BurstController struct {
	cfg   Config
	probe ProbeFunc
}

// NewBurstController creates a BurstController with the given config and probe.
func NewBurstController(cfg Config, probe ProbeFunc) *BurstController {
	return &BurstController{cfg: cfg, probe: probe}
}

// Run executes the burst collection loop and returns the aggregated result.
func (bc *BurstController) Run(ctx context.Context, trigger TriggerEvent) BurstResult {
	result := BurstResult{
		Trigger:   trigger,
		StartTime: time.Now(),
		Frames:    make([]BurstFrame, 0, 150), // pre-alloc ~30s / 200ms
	}

	deadline := time.After(bc.cfg.BurstDuration)
	ticker := time.NewTicker(bc.cfg.BurstInterval)
	defer ticker.Stop()

	calmStart := time.Time{}
	seq := 0

	for {
		select {
		case <-ctx.Done():
			return bc.finalize(result)
		case <-deadline:
			return bc.finalize(result)
		case <-ticker.C:
			frame := bc.collectFrame(ctx, seq)
			result.Frames = append(result.Frames, frame)
			seq++

			// Track peak
			if frame.Snapshot.ActiveCount > result.PeakActive {
				result.PeakActive = frame.Snapshot.ActiveCount
			}

			// Calm detection: if active sessions drop below trigger threshold
			if bc.isCalm(frame, trigger) {
				if calmStart.IsZero() {
					calmStart = time.Now()
				} else if time.Since(calmStart) >= bc.cfg.BurstCalmDelay {
					result.RecoverTime = calmStart
					return bc.finalize(result)
				}
			} else {
				calmStart = time.Time{} // reset calm timer
			}
		}
	}
}

// collectFrame runs a single probe and wraps it in a BurstFrame.
func (bc *BurstController) collectFrame(ctx context.Context, seq int) BurstFrame {
	snap := bc.probe(ctx)
	return BurstFrame{
		Seq:       seq,
		Snapshot:  snap,
		Timestamp: snap.Timestamp,
	}
}

// isCalm returns true when the anomaly appears to have subsided.
// "Calm" means active sessions have returned to near the trigger baseline.
func (bc *BurstController) isCalm(frame BurstFrame, trigger TriggerEvent) bool {
	current := float64(frame.Snapshot.ActiveCount)
	// Calm when active count drops to within 1.5x of the pre-trigger baseline
	calmThreshold := trigger.Baseline * 1.5
	if calmThreshold < trigger.Baseline+2 {
		calmThreshold = trigger.Baseline + 2
	}
	return current <= calmThreshold
}

// finalize sets the end time and returns the result.
func (bc *BurstController) finalize(result BurstResult) BurstResult {
	result.EndTime = time.Now()
	return result
}
