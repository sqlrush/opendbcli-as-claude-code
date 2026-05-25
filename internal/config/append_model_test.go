/*-------------------------------------------------------------------------
 *
 * append_model_test.go
 *	  Test cases for append_model.go (config package):
 *	  TestAppendModel_NewBlock, TestAppendModel_ExistingBlock.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/config/append_model_test.go
 *
 *-------------------------------------------------------------------------
 */
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendModel_NewBlock(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(tmp, []byte("active_model: foo\nsecurity:\n  default_level: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := ModelConfig{
		Name:     "glm5",
		Provider: "openai",
		BaseURL:  "https://x/v1",
		Model:    "glm-5",
	}
	if err := AppendModel(tmp, m); err != nil {
		t.Fatalf("AppendModel: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	out := string(got)
	if !strings.Contains(out, "models:") {
		t.Errorf("expected models: section, got:\n%s", out)
	}
	if !strings.Contains(out, "name: glm5") {
		t.Errorf("expected name: glm5, got:\n%s", out)
	}
	// Pre-existing content preserved.
	if !strings.Contains(out, "active_model: foo") {
		t.Errorf("lost active_model field, got:\n%s", out)
	}
}

func TestAppendModel_ExistingBlock(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.yaml")
	initial := `active_model: deepseek
security:
  default_level: 0
models:
- name: deepseek
  provider: openai
  base_url: https://api.deepseek.com/v1
  model: deepseek-chat
sentinel:
  auto_start: true
`
	if err := os.WriteFile(tmp, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	m := ModelConfig{
		Name:     "glm5",
		Provider: "openai",
		BaseURL:  "https://x/v1",
		Model:    "glm-5",
	}
	if err := AppendModel(tmp, m); err != nil {
		t.Fatalf("AppendModel: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	out := string(got)

	// Both models present.
	if !strings.Contains(out, "name: deepseek") {
		t.Errorf("lost original model, got:\n%s", out)
	}
	if !strings.Contains(out, "name: glm5") {
		t.Errorf("missing new model, got:\n%s", out)
	}

	// glm5 must appear AFTER deepseek (inserted at end of models: block).
	dsIdx := strings.Index(out, "name: deepseek")
	glmIdx := strings.Index(out, "name: glm5")
	if !(dsIdx < glmIdx) {
		t.Errorf("glm5 should be after deepseek, got:\n%s", out)
	}

	// glm5 must appear BEFORE sentinel (block boundary respected).
	sentIdx := strings.Index(out, "sentinel:")
	if !(glmIdx < sentIdx) {
		t.Errorf("glm5 should be before sentinel section, got:\n%s", out)
	}

	// Re-parse with yaml to ensure structure is valid.
	cfg, err := LoadFromFile(tmp)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if len(cfg.Models) != 2 {
		t.Errorf("expected 2 models after append, got %d", len(cfg.Models))
	}
}
