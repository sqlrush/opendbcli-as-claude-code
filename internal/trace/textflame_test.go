/*-------------------------------------------------------------------------
 *
 * textflame_test.go
 *	  Test cases for textflame.go (trace package):
 *	  TestFormatTextFlame_TreeStyle, TestFormatTextFlame_Empty,
 *	  TestFormatTextFlame_Simple.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/textflame_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"fmt"
	"strings"
	"testing"
)

func TestFormatTextFlame_TreeStyle(t *testing.T) {
	// Simulate MySQL ordered_commit scenario
	collapsed := `start_thread;handle_connection;do_command;dispatch_command;dispatch_sql_command;mysql_execute_command;trans_commit_stmt;ha_commit_trans;MYSQL_BIN_LOG::commit;ordered_commit;process_flush_stage_queue;flush_cache_to_file;write 15
start_thread;handle_connection;do_command;dispatch_command;dispatch_sql_command;mysql_execute_command;trans_commit_stmt;ha_commit_trans;MYSQL_BIN_LOG::commit;ordered_commit;sync_binlog_file;fsync 12
start_thread;handle_connection;do_command;dispatch_command;dispatch_sql_command;mysql_execute_command;trans_commit_stmt;ha_commit_trans;MYSQL_BIN_LOG::commit;ordered_commit;process_commit_stage_queue;ha_commit_low;log_free_check_wait;os_event_wait 25
start_thread;handle_connection;do_command;dispatch_command;dispatch_sql_command;mysql_execute_command;trans_commit_stmt;ha_commit_trans;MYSQL_BIN_LOG::commit;ordered_commit;process_commit_stage_queue;ha_commit_low;innobase_commit;log_write_up_to 10
start_thread;io_handler_thread;LinuxAIOHandler::poll;io_getevents;schedule 8
start_thread;handle_connection;do_command;dispatch_command;__int_malloc 5`

	result := FormatTextFlame(collapsed, 90)
	fmt.Println(result)

	if !strings.Contains(result, "Flame Graph") {
		t.Error("expected header")
	}
	if !strings.Contains(result, "▓") {
		t.Error("expected bar characters")
	}
	if !strings.Contains(result, "75") {
		t.Error("expected total 75 samples")
	}
	// Should contain tree connectors
	if !strings.Contains(result, "├─") || !strings.Contains(result, "└─") {
		t.Error("expected tree connectors ├─ and └─")
	}
	// Should mark hotspots
	if !strings.Contains(result, "* =") {
		t.Error("expected * hotspot markers")
	}
}

func TestFormatTextFlame_Empty(t *testing.T) {
	result := FormatTextFlame("", 60)
	if !strings.Contains(result, "no stack data") {
		t.Errorf("expected empty message, got: %s", result)
	}
}

func TestFormatTextFlame_Simple(t *testing.T) {
	collapsed := "a;b;c 10\na;b;d 5\na;e 3"
	result := FormatTextFlame(collapsed, 80)
	fmt.Println(result)

	if !strings.Contains(result, "18") {
		t.Errorf("expected 18 samples, got: %s", result)
	}
}

func TestFindHotspots(t *testing.T) {
	collapsed := "a;b;c 50\na;b;d 30\na;e 20"
	root := buildFrameTree(collapsed)
	hotspots := findHotspots(root, root.samples)

	// c and d and e are leaf nodes with significant percentages
	if !hotspots["c"] {
		t.Error("expected 'c' to be a hotspot (50%)")
	}
	if !hotspots["d"] {
		t.Error("expected 'd' to be a hotspot (30%)")
	}
	if !hotspots["e"] {
		t.Error("expected 'e' to be a hotspot (20%)")
	}
}

func TestFormatCollapsedSummary(t *testing.T) {
	collapsed := "a;b;c 10\na;b;d 5\na;e 3"
	result := FormatCollapsedSummary(collapsed, 2)

	if !strings.Contains(result, "10") {
		t.Error("expected count 10")
	}
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}

func TestShortenStack(t *testing.T) {
	short := shortenStack([]string{"a", "b", "c"})
	if short != "a → b → c" {
		t.Errorf("expected 'a → b → c', got '%s'", short)
	}

	long := shortenStack([]string{"a", "b", "c", "d", "e", "f", "g"})
	if !strings.Contains(long, "...") {
		t.Errorf("expected '...' in long stack, got '%s'", long)
	}
}
