/*-------------------------------------------------------------------------
 *
 * hostcheck_test.go
 *	  Test cases for hostcheck.go (trace package): TestIsLoopback,
 *	  TestFindDBProcess_UnknownType, TestProcessPatterns.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/hostcheck_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import "testing"

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"", true},
		{"10.0.0.5", false},
		{"db.example.com", false},
	}
	for _, tt := range tests {
		if got := isLoopback(tt.host); got != tt.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestFindDBProcess_UnknownType(t *testing.T) {
	_, err := findDBProcess("unknown_db")
	if err == nil {
		t.Error("expected error for unknown db type")
	}
}

func TestProcessPatterns(t *testing.T) {
	for _, dbType := range []string{"mysql", "postgres", "oracle", "opengauss"} {
		pat, ok := processPatterns[dbType]
		if !ok || len(pat) == 0 {
			t.Errorf("no process pattern defined for %s", dbType)
		}
	}
}
