/*-------------------------------------------------------------------------
 *
 * collapse_test.go
 *	  Test cases for collapse.go (trace package): TestCollapse_Basic,
 *	  TestCollapse_Empty, TestExtractTopFuncs.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/collapse_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"strings"
	"testing"
)

func TestCollapse_Basic(t *testing.T) {
	raw := "mysqld 12345 99.00: cycles:\n" +
		"\t7f1234 ha_innodb::write_row (/usr/sbin/mysqld)\n" +
		"\t7f1235 handler::ha_write_row (/usr/sbin/mysqld)\n" +
		"\t7f1236 mysql_insert (/usr/sbin/mysqld)\n" +
		"\n" +
		"mysqld 12345 99.00: cycles:\n" +
		"\t7f1234 ha_innodb::write_row (/usr/sbin/mysqld)\n" +
		"\t7f1235 handler::ha_write_row (/usr/sbin/mysqld)\n" +
		"\t7f1236 mysql_insert (/usr/sbin/mysqld)\n" +
		"\n" +
		"mysqld 12345 99.00: cycles:\n" +
		"\t7f1237 lock_wait_timeout (/usr/sbin/mysqld)\n" +
		"\t7f1238 os_event_wait (/usr/sbin/mysqld)\n"

	collapsed := CollapseStacks(raw)
	lines := strings.Split(strings.TrimSpace(collapsed), "\n")

	if len(lines) != 2 {
		t.Fatalf("expected 2 collapsed lines, got %d: %v", len(lines), lines)
	}

	// First line should have count 2 (two identical stacks, sorted by count desc).
	if !strings.HasSuffix(lines[0], " 2") {
		t.Errorf("expected first line to end with ' 2', got: %s", lines[0])
	}
}

func TestCollapse_Empty(t *testing.T) {
	collapsed := CollapseStacks("")
	if collapsed != "" {
		t.Errorf("expected empty output for empty input, got %q", collapsed)
	}
}

func TestExtractTopFuncs(t *testing.T) {
	collapsed := "mysqld;mysql_insert;ha_innodb::write_row 100\nmysqld;lock_wait 50\nmysqld;other 10"
	funcs := ExtractTopFuncs(collapsed, 2)
	if len(funcs) != 2 {
		t.Fatalf("expected 2 funcs, got %d", len(funcs))
	}
	if funcs[0].Name != "ha_innodb::write_row" {
		t.Errorf("expected top func 'ha_innodb::write_row', got %q", funcs[0].Name)
	}
	if funcs[0].Samples != 100 {
		t.Errorf("expected 100 samples, got %d", funcs[0].Samples)
	}
	// Percentage: 100/160 = 62.5%
	if funcs[0].Percentage < 62.0 || funcs[0].Percentage > 63.0 {
		t.Errorf("expected ~62.5%% percentage, got %.1f%%", funcs[0].Percentage)
	}
}

func TestExtractTopFuncs_Empty(t *testing.T) {
	funcs := ExtractTopFuncs("", 10)
	if funcs != nil {
		t.Errorf("expected nil for empty input, got %v", funcs)
	}
}
