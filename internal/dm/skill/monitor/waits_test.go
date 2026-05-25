/*-------------------------------------------------------------------------
 *
 * waits_test.go
 *	  Test cases for waits.go (monitor package):
 *	  TestWaitsSkill_Metadata, TestWaitsSkill_NoEvents,
 *	  TestWaitsSkill_WithEvents.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/waits_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestWaitsSkill_Metadata(t *testing.T) {
	s := NewWaitsSkill(makeRoutedDriver())
	if s.Name() != "waits" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestWaitsSkill_NoEvents(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SYSTEM_EVENT",
		result: &db.QueryResult{
			Columns: []string{"EVENT", "WAIT_CLASS", "TOTAL_WAITS", "TIME_WAITED", "TIME_WAITED_MICRO", "AVERAGE_WAIT_MICRO"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewWaitsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "wait_event_count: 0")
}

func TestWaitsSkill_WithEvents(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SYSTEM_EVENT",
		result: &db.QueryResult{
			Columns: []string{"EVENT", "WAIT_CLASS", "TOTAL_WAITS", "TIME_WAITED", "TIME_WAITED_MICRO", "AVERAGE_WAIT_MICRO"},
			Rows: [][]any{
				{"trxid lock wait", "Application", int64(774976), int64(10050), int64(1005033), int64(1297)},
				{"db file scattered read", "User I/O", int64(45000), int64(2300), int64(230000), int64(5111)},
			},
		},
	})
	r, _ := NewWaitsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "wait_event_count: 2")
	assertSummaryContains(t, r.Rendered, "top_event: trxid lock wait")
	assertSummaryContains(t, r.Rendered, "top_event_class: Application")
	assertSummaryContains(t, r.Rendered, "top_event_total_waits: 774976")
}
