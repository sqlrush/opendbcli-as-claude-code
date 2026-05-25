/*-------------------------------------------------------------------------
 *
 * perfsnap_test.go
 *	  Test cases for perfsnap.go (monitor package):
 *	  TestOGPerfSnap_Metadata, TestOGPerfSnap_Validate,
 *	  TestOGSnapStore_PersistsAcrossReload.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/perfsnap_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// Metadata alignment: /perfsnap on OG should mirror the Oracle and PG
// signatures so LLMs switching DB contexts don't see different surfaces.
func TestOGPerfSnap_Metadata(t *testing.T) {
	dir := t.TempDir()
	s := NewOGPerfSnapSkill(mock.NewMockDriver(), dir)

	if s.Name() != "perfsnap" {
		t.Errorf("Name() = %q, want 'perfsnap'", s.Name())
	}
	if s.Description() == "" {
		t.Errorf("Description() is empty")
	}
	if s.ToolDef().Name != "perfsnap" {
		t.Errorf("ToolDef().Name = %q, want 'perfsnap'", s.ToolDef().Name)
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}

	// OG/PG accept "compare" (Oracle uses "diff"); the CLI Aliases include
	// "psnap" for parity with Oracle/PG.
	cli := s.CLIDef()
	if cli.Command != "perfsnap" {
		t.Errorf("CLIDef().Command = %q, want 'perfsnap'", cli.Command)
	}
	wantActions := map[string]bool{"snap": false, "compare": false, "list": false, "baseline": false}
	for _, a := range cli.ArgCompletions {
		if _, ok := wantActions[a]; ok {
			wantActions[a] = true
		}
	}
	for a, seen := range wantActions {
		if !seen {
			t.Errorf("CLIDef().ArgCompletions missing %q", a)
		}
	}
}

// Validate guards against typos like "diff" (Oracle uses diff, OG/PG use
// compare). A silently-ignored typo would take a snap instead of a diff.
func TestOGPerfSnap_Validate(t *testing.T) {
	s := NewOGPerfSnapSkill(mock.NewMockDriver(), t.TempDir())

	// Valid actions.
	for _, arg := range []string{"", "snap", "compare", "list", "baseline"} {
		if err := s.Validate(skill.ParamsFromMap(map[string]any{"args": arg})); err != nil {
			t.Errorf("Validate(%q) unexpected error: %v", arg, err)
		}
	}

	// Typos and Oracle-style "diff" must be rejected so users see the OG
	// vocabulary ("compare") instead of silently taking a snap.
	for _, arg := range []string{"diff", "snaphot", "snapshot", "xx"} {
		if err := s.Validate(skill.ParamsFromMap(map[string]any{"args": arg})); err == nil {
			t.Errorf("Validate(%q) should error", arg)
		}
	}
}

// The on-disk store survives an init cycle and preserves snapshot order.
// This guards the persistence path which /perfsnap relies on for compare.
func TestOGSnapStore_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()

	store := NewOGSnapStore(dir)
	_ = store.Add(OGPerfSnapshot{Timestamp: time.Now().Add(-2 * time.Minute)})
	_ = store.Add(OGPerfSnapshot{Timestamp: time.Now().Add(-1 * time.Minute)})

	if store.Count() != 2 {
		t.Fatalf("Count() = %d after two Adds, want 2", store.Count())
	}

	// Must have created a persistence file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected store to persist at least one file under %s", filepath.Clean(dir))
	}

	// Reopening should see the same two entries.
	reopened := NewOGSnapStore(dir)
	if reopened.Count() != 2 {
		t.Errorf("reloaded Count() = %d, want 2", reopened.Count())
	}
}

func TestOGRenderPerfEvidenceIncludesClassifiedDiff(t *testing.T) {
	d := OGPerfDiff{
		From:           time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 5, 23, 10, 5, 0, 0, time.UTC),
		TPSDelta:       42,
		CacheHitOld:    99,
		CacheHitNew:    75,
		TempBytesOld:   0,
		TempBytesNew:   2 * 1024 * 1024 * 1024,
		DeadlocksOld:   0,
		DeadlocksNew:   1,
		CheckpointsOld: 2,
		CheckpointsNew: 20,
		SQLTimeChanges: []ogSQLChange{{QueryID: "581990336", DeltaMs: 12000}},
	}
	out := ogRenderPerfEvidence(OGPerfSnapshot{Timestamp: d.To}, &d)
	for _, want := range []string{"perfsnap 结构化证据", "时间窗口", "Cache Hit", "IO风险", "Lock P0", "/sqltune 581990336"} {
		if !strings.Contains(out, want) {
			t.Fatalf("perfsnap evidence missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{"Evidence Builder", "<SQL_ID>"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("perfsnap evidence should not contain %q:\n%s", notWant, out)
		}
	}
}
