/*-------------------------------------------------------------------------
 *
 * correlation_test.go
 *	  Test cases for correlation.go (overlord package):
 *	  TestCorrelationEngine_NoCorrelation,
 *	  TestCorrelationEngine_SameMetric.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/correlation_test.go
 *
 *-------------------------------------------------------------------------
 */
package overlord

import (
	"fmt"
	"testing"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

func TestCorrelationEngine_NoCorrelation(t *testing.T) {
	wm := NewWorkerManager("overlord-test", "test-region")
	ce := NewCorrelationEngine(wm)

	// Single alert — no correlation.
	results := ce.AddAlert(AlertRecord{
		WorkerID: "worker-1",
		Alert:    &pb.AlertEvent{CauseName: "io_latency"},
		Received: time.Now(),
	})
	if len(results) != 0 {
		t.Errorf("expected 0 correlations, got %d", len(results))
	}
}

func TestCorrelationEngine_SameMetric(t *testing.T) {
	wm := NewWorkerManager("overlord-test", "test-region")
	ce := NewCorrelationEngine(wm)

	// 3 alerts with same cause → should correlate.
	for i := 1; i <= 3; i++ {
		ce.AddAlert(AlertRecord{
			WorkerID: fmt.Sprintf("worker-%d", i),
			Alert:    &pb.AlertEvent{CauseName: "io_latency", WorkerId: fmt.Sprintf("worker-%d", i)},
			Received: time.Now(),
		})
	}

	// The 3rd alert should trigger correlation.
	results := ce.AddAlert(AlertRecord{
		WorkerID: "worker-4",
		Alert:    &pb.AlertEvent{CauseName: "io_latency", WorkerId: "worker-4"},
		Received: time.Now(),
	})

	found := false
	for _, r := range results {
		if r.Pattern == "same_metric" {
			found = true
			if len(r.AffectedIDs) < 3 {
				t.Errorf("expected >= 3 affected workers, got %d", len(r.AffectedIDs))
			}
		}
	}
	if !found {
		t.Error("expected same_metric correlation pattern")
	}
}
