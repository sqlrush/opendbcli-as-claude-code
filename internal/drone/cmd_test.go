/*-------------------------------------------------------------------------
 *
 * cmd_test.go
 *	  Test cases for cmd.go (drone package): TestParseAgentArgs_Start,
 *	  TestParseAgentArgs_Empty, TestParseAgentArgs_InvalidSubcmd.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/cmd_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import "testing"

func TestParseAgentArgs_Start(t *testing.T) {
	args := []string{"start", "--role", "worker", "--overlord", "10.0.2.1:9200"}
	got, err := ParseAgentArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subcmd != SubcmdStart {
		t.Errorf("subcmd = %q, want %q", got.Subcmd, SubcmdStart)
	}
	if got.Role != "worker" {
		t.Errorf("role = %q, want %q", got.Role, "worker")
	}
	if got.Overlord != "10.0.2.1:9200" {
		t.Errorf("overlord = %q, want %q", got.Overlord, "10.0.2.1:9200")
	}
}

func TestParseAgentArgs_Empty(t *testing.T) {
	_, err := ParseAgentArgs(nil)
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestParseAgentArgs_InvalidSubcmd(t *testing.T) {
	_, err := ParseAgentArgs([]string{"invalid"})
	if err == nil {
		t.Fatal("expected error for invalid subcommand")
	}
}

func TestParseAgentArgs_StatusAndStop(t *testing.T) {
	for _, sub := range []string{"status", "stop"} {
		got, err := ParseAgentArgs([]string{sub})
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", sub, err)
		}
		if string(got.Subcmd) != sub {
			t.Errorf("subcmd = %q, want %q", got.Subcmd, sub)
		}
	}
}

func TestParseAgentArgs_EqualsSyntax(t *testing.T) {
	args := []string{"start", "--role=memory", "--listen=0.0.0.0:9200", "--web=0.0.0.0:8080"}
	got, err := ParseAgentArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Role != "memory" {
		t.Errorf("role = %q, want %q", got.Role, "memory")
	}
	if got.Listen != "0.0.0.0:9200" {
		t.Errorf("listen = %q, want %q", got.Listen, "0.0.0.0:9200")
	}
	if got.Web != "0.0.0.0:8080" {
		t.Errorf("web = %q, want %q", got.Web, "0.0.0.0:8080")
	}
}
