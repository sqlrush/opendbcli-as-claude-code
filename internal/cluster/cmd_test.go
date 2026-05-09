/*-------------------------------------------------------------------------
 *
 * cmd_test.go
 *	  Test cases for cmd.go (cluster package):
 *	  TestParseClusterArgs_Init, TestParseClusterArgs_Join,
 *	  TestParseClusterArgs_Empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/cmd_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import "testing"

func TestParseClusterArgs_Init(t *testing.T) {
	args := []string{"init", "--role", "manager", "--listen", "0.0.0.0:9100"}
	got, err := ParseClusterArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subcmd != SubcmdInit {
		t.Errorf("subcmd = %q, want %q", got.Subcmd, SubcmdInit)
	}
	if got.Role != "manager" {
		t.Errorf("role = %q, want %q", got.Role, "manager")
	}
}

func TestParseClusterArgs_Join(t *testing.T) {
	args := []string{"join", "--role", "memory", "--cerebrate", "10.0.1.1:9100", "--token", "abc123", "--region", "china-east"}
	got, err := ParseClusterArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subcmd != SubcmdJoin {
		t.Errorf("subcmd = %q, want %q", got.Subcmd, SubcmdJoin)
	}
	if got.Cerebrate != "10.0.1.1:9100" {
		t.Errorf("cerebrate = %q, want %q", got.Cerebrate, "10.0.1.1:9100")
	}
	if got.Token != "abc123" {
		t.Errorf("token = %q, want %q", got.Token, "abc123")
	}
	if got.Region != "china-east" {
		t.Errorf("region = %q, want %q", got.Region, "china-east")
	}
}

func TestParseClusterArgs_Empty(t *testing.T) {
	_, err := ParseClusterArgs(nil)
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRunInit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENDB_HOME", tmpDir)

	args := ClusterArgs{
		Subcmd: SubcmdInit,
		Role:   "manager",
		Listen: "0.0.0.0:9100",
	}

	if err := RunCluster(args); err != nil {
		t.Fatalf("RunCluster init: %v", err)
	}

	// Should be initialized now.
	if !IsInitialized(tmpDir) {
		t.Error("cluster should be initialized after init")
	}

	// Second init should fail.
	if err := RunCluster(args); err == nil {
		t.Error("second init should fail")
	}
}

func TestRunJoinWorker(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENDB_HOME", tmpDir)

	args := ClusterArgs{
		Subcmd:   SubcmdJoin,
		Role:     "worker",
		Overlord: "10.0.2.1:9200",
		Token:    "test-token",
	}

	if err := RunCluster(args); err != nil {
		t.Fatalf("RunCluster join: %v", err)
	}

	cfg, err := LoadClusterConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadClusterConfig: %v", err)
	}
	if cfg.Role != "worker" {
		t.Errorf("role = %q, want worker", cfg.Role)
	}
	if cfg.Overlord != "10.0.2.1:9200" {
		t.Errorf("overlord = %q, want 10.0.2.1:9200", cfg.Overlord)
	}
}

func TestRunJoinOverlord(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENDB_HOME", tmpDir)

	args := ClusterArgs{
		Subcmd:    SubcmdJoin,
		Role:      "memory",
		Cerebrate: "10.0.1.1:9100",
		Region:    "china-east",
		Token:     "test-token",
	}

	if err := RunCluster(args); err != nil {
		t.Fatalf("RunCluster join: %v", err)
	}

	cfg, err := LoadClusterConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadClusterConfig: %v", err)
	}
	if cfg.Role != "memory" {
		t.Errorf("role = %q, want memory", cfg.Role)
	}
	if cfg.Region != "china-east" {
		t.Errorf("region = %q, want china-east", cfg.Region)
	}
}
