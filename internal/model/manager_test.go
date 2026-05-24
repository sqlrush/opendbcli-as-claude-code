/*-------------------------------------------------------------------------
 *
 * manager_test.go
 *	  Test cases for manager.go (model package): TestLoadProfiles_Empty,
 *	  TestLoadProfiles_MissingDir, TestLoadProfiles_ValidYAML.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/model/manager_test.go
 *
 *-------------------------------------------------------------------------
 */
package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProfiles_Empty(t *testing.T) {
	dir := t.TempDir()
	profiles, err := LoadProfiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestLoadProfiles_MissingDir(t *testing.T) {
	profiles, err := LoadProfiles("/nonexistent/path")
	if err != nil {
		t.Fatalf("missing dir should return empty map, got error: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestLoadProfiles_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := `group: local
models:
  - name: qwen9b
    provider: ollama
    base_url: http://localhost:11434
    model: qwen3.5:9b
    capability: small
    description: "Local Qwen 9B"
  - name: qwen35b
    provider: ollama
    base_url: http://localhost:11434
    model: qwen3.5:35b-a3b
    capability: large
`
	os.WriteFile(filepath.Join(dir, "local.yaml"), []byte(yaml), 0644)

	profiles, err := LoadProfiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	q9 := profiles["qwen9b"]
	if q9.Provider != "ollama" || q9.Model != "qwen3.5:9b" || q9.Capability != "small" {
		t.Errorf("qwen9b profile mismatch: %+v", q9)
	}
	if q9.Group != "local" {
		t.Errorf("qwen9b group = %q, want local", q9.Group)
	}

	q35 := profiles["qwen35b"]
	if q35.Capability != "large" {
		t.Errorf("qwen35b capability = %q, want large", q35.Capability)
	}
}

func TestLoadProfiles_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(`group: a
models:
  - name: dup
    provider: ollama
    base_url: http://localhost:11434
    model: m1
`), 0644)
	os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(`group: b
models:
  - name: dup
    provider: ollama
    base_url: http://localhost:11434
    model: m2
`), 0644)

	_, err := LoadProfiles(dir)
	if err == nil {
		t.Error("expected duplicate name error")
	}
}

func TestNewManager_FallbackToDefault(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "", FallbackLLM{
		Provider:   "ollama",
		BaseURL:    "http://localhost:11434",
		Model:      "qwen3.5:9b",
		Capability: "small",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	profiles := mgr.List()
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile (default), got %d", len(profiles))
	}
	if profiles[0].Name != "default" {
		t.Errorf("expected profile name 'default', got %q", profiles[0].Name)
	}
	if mgr.ActiveName() != "default" {
		t.Errorf("active = %q, want 'default'", mgr.ActiveName())
	}
	if mgr.Provider() == nil {
		t.Error("provider should not be nil")
	}
	if mgr.Capability() != "small" {
		t.Errorf("capability = %q, want 'small'", mgr.Capability())
	}
}

func TestNewManager_InlineToolMode(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "qwen-prompt", FallbackLLM{
		InlineModels: []InlineModel{{
			Name:       "qwen-prompt",
			Provider:   "openai",
			BaseURL:    "http://localhost:8000/v1",
			Model:      "qwen3.6",
			Capability: "large",
			ToolMode:   "prompt",
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := mgr.ToolMode(); got != "prompt" {
		t.Fatalf("ToolMode() = %q, want prompt", got)
	}
	active := mgr.Active()
	if active == nil {
		t.Fatal("expected active model")
	}
	if active.ToolMode != "prompt" {
		t.Fatalf("active.ToolMode = %q, want prompt", active.ToolMode)
	}
}

func TestManager_ReloadPreservesInlineToolMode(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "qwen-prompt", FallbackLLM{
		InlineModels: []InlineModel{{
			Name:       "qwen-prompt",
			Provider:   "openai",
			BaseURL:    "http://localhost:8000/v1",
			Model:      "qwen3.6",
			Capability: "large",
			ToolMode:   "prompt",
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := mgr.Reload(); err != nil {
		t.Fatalf("Reload error: %v", err)
	}
	if got := mgr.ToolMode(); got != "prompt" {
		t.Fatalf("ToolMode() after reload = %q, want prompt", got)
	}
}

func TestNewManager_NoLLM(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "", FallbackLLM{Provider: "none"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mgr.Active() != nil {
		t.Error("should have no active model")
	}
	if mgr.Provider() != nil {
		t.Error("provider should be nil")
	}
}

func TestManager_SwitchAndDisable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(`group: test
models:
  - name: a
    provider: ollama
    base_url: http://localhost:11434
    model: model-a
    capability: small
  - name: b
    provider: ollama
    base_url: http://localhost:11434
    model: model-b
    capability: large
`), 0644)

	mgr, err := NewManager(dir, "a", FallbackLLM{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mgr.ActiveName() != "a" {
		t.Errorf("active = %q, want 'a'", mgr.ActiveName())
	}

	profile, err := mgr.Switch("b")
	if err != nil {
		t.Fatalf("Switch error: %v", err)
	}
	if profile.Name != "b" {
		t.Errorf("switched to %q, want 'b'", profile.Name)
	}
	if mgr.Capability() != "large" {
		t.Errorf("capability = %q, want 'large'", mgr.Capability())
	}

	_, err = mgr.Switch("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent model")
	}

	mgr.Disable()
	if mgr.Active() != nil {
		t.Error("should have no active model after disable")
	}
	if mgr.Provider() != nil {
		t.Error("provider should be nil after disable")
	}
}

func TestManager_Reload(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(`group: test
models:
  - name: old
    provider: ollama
    base_url: http://localhost:11434
    model: old-model
`), 0644)

	mgr, err := NewManager(dir, "old", FallbackLLM{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.ActiveName() != "old" {
		t.Errorf("active = %q, want 'old'", mgr.ActiveName())
	}

	os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(`group: test
models:
  - name: new
    provider: ollama
    base_url: http://localhost:11434
    model: new-model
`), 0644)

	count, err := mgr.Reload()
	if err != nil {
		t.Fatalf("Reload error: %v", err)
	}
	if count != 1 {
		t.Errorf("reload count = %d, want 1", count)
	}
	if mgr.Active() != nil {
		t.Error("old model was removed, should have no active")
	}
}

func TestExpandEnvVars(t *testing.T) {
	os.Setenv("TEST_MODEL_KEY", "secret123")
	defer os.Unsetenv("TEST_MODEL_KEY")

	result := ExpandEnvVars("${TEST_MODEL_KEY}")
	if result != "secret123" {
		t.Errorf("got %q, want 'secret123'", result)
	}

	result = ExpandEnvVars("Bearer ${TEST_MODEL_KEY}")
	if result != "Bearer secret123" {
		t.Errorf("got %q, want 'Bearer secret123'", result)
	}

	result = ExpandEnvVars("${UNSET_VAR}")
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}
