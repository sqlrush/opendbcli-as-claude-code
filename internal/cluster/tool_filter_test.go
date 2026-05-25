/*-------------------------------------------------------------------------
 *
 * tool_filter_test.go
 *	  Test cases for tool_filter.go (cluster package):
 *	  TestToolFilter_FilterForLLM, TestToolFilter_NeedsConfirmation,
 *	  TestToolFilter_CheckExecution.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/tool_filter_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestToolFilter_FilterForLLM(t *testing.T) {
	hc := NewHotConfig()
	hc.SetToolControl("kill_session", "hidden")
	hc.SetToolControl("execute_sql", "disabled")
	hc.SetToolControl("broadcast", "confirm")

	tf := NewToolFilter(hc)

	tools := []skill.ToolDef{
		{Name: "sql"},
		{Name: "kill_session"},
		{Name: "execute_sql"},
		{Name: "broadcast"},
		{Name: "health"},
	}

	filtered := tf.FilterForLLM(tools)

	// hidden + disabled removed → sql, broadcast, health remain
	if len(filtered) != 3 {
		t.Errorf("filtered count = %d, want 3", len(filtered))
	}

	names := make(map[string]bool)
	for _, f := range filtered {
		names[f.Name] = true
	}
	if names["kill_session"] {
		t.Error("kill_session should be filtered out (hidden)")
	}
	if names["execute_sql"] {
		t.Error("execute_sql should be filtered out (disabled)")
	}
}

func TestToolFilter_NeedsConfirmation(t *testing.T) {
	hc := NewHotConfig()
	hc.SetToolControl("broadcast", "confirm")

	tf := NewToolFilter(hc)

	if !tf.NeedsConfirmation("broadcast") {
		t.Error("broadcast should need confirmation")
	}
	if tf.NeedsConfirmation("sql") {
		t.Error("sql should not need confirmation")
	}
}

func TestToolFilter_CheckExecution(t *testing.T) {
	hc := NewHotConfig()
	hc.SetToolControl("kill_session", "hidden")
	hc.SetToolControl("execute_sql", "disabled")

	tf := NewToolFilter(hc)

	if err := tf.CheckExecution("kill_session"); err == nil {
		t.Error("hidden tool should return error")
	}
	if err := tf.CheckExecution("execute_sql"); err == nil {
		t.Error("disabled tool should return error")
	}
	if err := tf.CheckExecution("sql"); err != nil {
		t.Errorf("enabled tool should not error: %v", err)
	}
}
