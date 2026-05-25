/*-------------------------------------------------------------------------
 *
 * hotconfig_test.go
 *	  Test cases for hotconfig.go (cluster package):
 *	  TestHotConfig_Blacklist, TestHotConfig_ToolControl,
 *	  TestHotConfig_SaveLoad.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/hotconfig_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import (
	"path/filepath"
	"testing"
)

func TestHotConfig_Blacklist(t *testing.T) {
	hc := NewHotConfig()

	// Default blacklist.
	if !hc.IsBlacklisted("DROP TABLE") {
		t.Error("DROP TABLE should be blacklisted by default")
	}
	if hc.IsBlacklisted("SELECT 1") {
		t.Error("SELECT should not be blacklisted")
	}

	// Add to blacklist.
	hc.AddToBlacklist("ALTER SYSTEM FLUSH SHARED_POOL")
	if !hc.IsBlacklisted("ALTER SYSTEM FLUSH SHARED_POOL") {
		t.Error("newly added should be blacklisted")
	}

	// Duplicate add (should not panic or duplicate).
	hc.AddToBlacklist("DROP TABLE")
	list := hc.GetBlacklist()
	count := 0
	for _, b := range list {
		if b == "DROP TABLE" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("DROP TABLE count = %d, want 1", count)
	}
}

func TestHotConfig_ToolControl(t *testing.T) {
	hc := NewHotConfig()

	// Default is "enabled".
	if hc.GetToolControl("sql") != "enabled" {
		t.Errorf("default = %q, want enabled", hc.GetToolControl("sql"))
	}

	hc.SetToolControl("kill_session", "hidden")
	if hc.GetToolControl("kill_session") != "hidden" {
		t.Errorf("kill_session = %q, want hidden", hc.GetToolControl("kill_session"))
	}
}

func TestHotConfig_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "hotconfig.yaml")

	hc := NewHotConfig()
	hc.AddToBlacklist("CUSTOM_OP")
	hc.SetToolControl("failover", "confirm")

	if err := SaveHotConfig(path, hc); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadHotConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !loaded.IsBlacklisted("CUSTOM_OP") {
		t.Error("loaded should have CUSTOM_OP")
	}
	if loaded.GetToolControl("failover") != "confirm" {
		t.Errorf("failover = %q, want confirm", loaded.GetToolControl("failover"))
	}
}
