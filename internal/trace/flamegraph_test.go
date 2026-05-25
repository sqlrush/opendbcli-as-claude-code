/*-------------------------------------------------------------------------
 *
 * flamegraph_test.go
 *	  Test cases for flamegraph.go (trace package):
 *	  TestGenerateSVG_Basic, TestGenerateSVG_Empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/flamegraph_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSVG_Basic(t *testing.T) {
	collapsed := "mysqld;mysql_insert;ha_innodb::write_row 100\nmysqld;lock_wait 50"
	dir := t.TempDir()
	path := filepath.Join(dir, "test.svg")

	err := GenerateSVG(collapsed, path, "mysqld (PID 12345)")
	if err != nil {
		t.Fatalf("GenerateSVG failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SVG: %v", err)
	}

	svg := string(data)
	if !strings.Contains(svg, "<svg") {
		t.Error("output is not valid SVG")
	}
	if !strings.Contains(svg, "ha_innodb::write_row") {
		t.Error("SVG should contain function name 'ha_innodb::write_row'")
	}
}

func TestGenerateSVG_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.svg")
	err := GenerateSVG("", path, "test")
	if err == nil {
		t.Error("expected error for empty collapsed data")
	}
}
