/*-------------------------------------------------------------------------
 *
 * config_test.go
 *	  Test cases for config.go (config package): TestLoadDefault,
 *	  TestLoadFromFile, TestLoadFromFile_InvalidYAML.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/config/config_test.go
 *
 *-------------------------------------------------------------------------
 */
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	cfg := Default()
	if cfg.ConnectionsDir == "" {
		t.Error("ConnectionsDir should have a default value")
	}
	if cfg.Security.DefaultLevel != 0 {
		t.Errorf("default security level = %d, want 0", cfg.Security.DefaultLevel)
	}
	if !cfg.Security.ConfirmOnDangerous {
		t.Error("ConfirmOnDangerous should default to true")
	}
	if cfg.Output.Format != "terminal" {
		t.Errorf("output format = %q, want %q", cfg.Output.Format, "terminal")
	}
	if cfg.Output.MaxRows != 1000 {
		t.Errorf("max rows = %d, want 1000", cfg.Output.MaxRows)
	}
	if cfg.LLM.Provider != "none" {
		t.Errorf("llm provider = %q, want %q", cfg.LLM.Provider, "none")
	}
	if cfg.LLM.Model != "qwen3.5:9b" {
		t.Errorf("llm model = %q, want %q", cfg.LLM.Model, "qwen3.5:9b")
	}
	if cfg.LLM.DiagnoseMode != "auto" {
		t.Errorf("diagnose mode = %q, want %q", cfg.LLM.DiagnoseMode, "auto")
	}
	if cfg.LLM.MaxRounds != 0 {
		t.Errorf("max rounds = %d, want 0", cfg.LLM.MaxRounds)
	}
	if cfg.LLM.MaxResultTokens != 4000 {
		t.Errorf("max result tokens = %d, want 4000", cfg.LLM.MaxResultTokens)
	}
	if !cfg.Session.RestoreOnSwitch {
		t.Error("RestoreOnSwitch should default to true")
	}
	if cfg.Session.HistoryDir == "" {
		t.Error("HistoryDir should have a default value")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
connections_dir: /custom/path
security:
  default_level: 1
  confirm_on_dangerous: false
output:
  format: json
  max_rows: 500
llm:
  provider: anthropic
session:
  restore_on_switch: false
  history_dir: /custom/history
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ConnectionsDir != "/custom/path" {
		t.Errorf("ConnectionsDir = %q, want %q", cfg.ConnectionsDir, "/custom/path")
	}
	if cfg.Security.DefaultLevel != 1 {
		t.Errorf("DefaultLevel = %d, want 1", cfg.Security.DefaultLevel)
	}
	if cfg.Security.ConfirmOnDangerous {
		t.Error("ConfirmOnDangerous should be false")
	}
	if cfg.Output.Format != "json" {
		t.Errorf("output format = %q, want %q", cfg.Output.Format, "json")
	}
	if cfg.Output.MaxRows != 500 {
		t.Errorf("max rows = %d, want 500", cfg.Output.MaxRows)
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("llm provider = %q, want %q", cfg.LLM.Provider, "anthropic")
	}
	// New fields should retain defaults when not in YAML
	if cfg.LLM.DiagnoseMode != "auto" {
		t.Errorf("diagnose mode = %q, want default %q", cfg.LLM.DiagnoseMode, "auto")
	}
	if cfg.Session.RestoreOnSwitch {
		t.Error("RestoreOnSwitch should be false")
	}
	if cfg.Session.HistoryDir != "/custom/history" {
		t.Errorf("HistoryDir = %q, want %q", cfg.Session.HistoryDir, "/custom/history")
	}
}

func TestLoadFromFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")
	err := os.WriteFile(cfgPath, []byte(`{{{not valid yaml`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadFromFile(cfgPath)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoadFromFile_MissingFile(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoadFromFile_LLMConfigExtended(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
llm:
  provider: ollama
  model: qwen3.5:35b
  base_url: http://192.168.1.100:11434
  diagnose_mode: assist
  max_rounds: 5
  max_result_tokens: 2000
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "ollama")
	}
	if cfg.LLM.Model != "qwen3.5:35b" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "qwen3.5:35b")
	}
	if cfg.LLM.BaseURL != "http://192.168.1.100:11434" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "http://192.168.1.100:11434")
	}
	if cfg.LLM.DiagnoseMode != "assist" {
		t.Errorf("diagnose_mode = %q, want %q", cfg.LLM.DiagnoseMode, "assist")
	}
	if cfg.LLM.MaxRounds != 5 {
		t.Errorf("max_rounds = %d, want 5", cfg.LLM.MaxRounds)
	}
	if cfg.LLM.MaxResultTokens != 2000 {
		t.Errorf("max_result_tokens = %d, want 2000", cfg.LLM.MaxResultTokens)
	}
}

func TestLoad_FallsBackToDefault(t *testing.T) {
	// Unset the env var to ensure we hit the fallback path
	t.Setenv("OPENDB_CONFIG", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ConnectionsDir == "" {
		t.Error("ConnectionsDir should have a default value")
	}
}

func TestLoad_FromEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "env-config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
connections_dir: /from/env
security:
  default_level: 2
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("OPENDB_CONFIG", cfgPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ConnectionsDir != "/from/env" {
		t.Errorf("ConnectionsDir = %q, want %q", cfg.ConnectionsDir, "/from/env")
	}
	if cfg.Security.DefaultLevel != 2 {
		t.Errorf("DefaultLevel = %d, want 2", cfg.Security.DefaultLevel)
	}
}
