/*-------------------------------------------------------------------------
 *
 * existingconfig_test.go
 *	  Test cases for existingconfig.go (setup package):
 *	  TestExistingConfigStep_NoFile_AutoCompletes,
 *	  TestExistingConfigStep_FileExists_PromptShown.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/existingconfig_test.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingConfigStep_NoFile_AutoCompletes(t *testing.T) {
	tmp := t.TempDir()
	cfg := &SetupConfig{BaseDir: tmp}
	s := NewExistingConfigStep(cfg)

	if s.exists {
		t.Fatal("expected exists=false on fresh tmpdir")
	}
	// Init should signal done immediately so wizard skips this step.
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("expected emitDone cmd from Init when no file exists")
	}
	if !s.Done() {
		t.Errorf("expected Done()=true after Init when no file")
	}
	if !strings.Contains(s.Summary(), "fresh install") {
		t.Errorf("Summary should say 'fresh install', got: %q", s.Summary())
	}
}

func TestExistingConfigStep_FileExists_PromptShown(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("active_model: foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &SetupConfig{BaseDir: tmp}
	s := NewExistingConfigStep(cfg)

	if !s.exists {
		t.Fatal("expected exists=true when config.yaml present")
	}
	if s.confirm == nil {
		t.Fatal("confirm prompt not initialized when file exists")
	}
	if s.Done() {
		t.Errorf("step should not be done before user confirms")
	}
	view := s.View()
	if !strings.Contains(view, "configure") {
		t.Errorf("View should mention `configure` as alt path, got: %q", view)
	}
}
