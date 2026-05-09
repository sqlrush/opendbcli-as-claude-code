/*-------------------------------------------------------------------------
 *
 * source_test.go
 *	  Test cases for source.go (trace package): TestSourceLookup_Grep,
 *	  TestSourceLookup_NoDir, TestSourceLookup_NonexistentDir.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/source_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceLookup_Grep(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "storage", "innobase", "handler", "ha_innodb.cc")
	os.MkdirAll(filepath.Dir(srcFile), 0755)
	os.WriteFile(srcFile, []byte(`
int ha_innodb::write_row(uchar* record) {
    trx_t* trx = thd_to_trx(ha_thd());
    return row_insert_for_mysql(record, trx);
}

void ha_innodb::other_func() {
    // something else
}
`), 0644)

	lookup := &SourceLookup{SourceDir: dir}
	funcs := []HotFunc{
		{Name: "ha_innodb::write_row"},
		{Name: "nonexistent_func"},
	}
	results, err := lookup.Grep(funcs)
	if err != nil {
		t.Fatalf("Grep failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FuncName != "ha_innodb::write_row" {
		t.Errorf("unexpected func name: %s", results[0].FuncName)
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

func TestSourceLookup_NoDir(t *testing.T) {
	lookup := &SourceLookup{SourceDir: ""}
	results, err := lookup.Grep([]HotFunc{{Name: "test"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected empty results when no source dir configured")
	}
}

func TestSourceLookup_NonexistentDir(t *testing.T) {
	lookup := &SourceLookup{SourceDir: "/nonexistent/path/12345"}
	results, err := lookup.Grep([]HotFunc{{Name: "test"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected empty results for nonexistent dir")
	}
}
