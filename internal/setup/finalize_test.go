/*-------------------------------------------------------------------------
 *
 * finalize_test.go
 *	  Test cases for finalize.go (setup package): TestGenerateConfig,
 *	  TestGenerateConfig_SingleFile.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/finalize_test.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
)

func TestGenerateConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &SetupConfig{
		BaseDir: tmpDir,
		Mode:    "quickstart",
		DBType:  "oracle",
		Connection: config.Connection{
			Name:     "test-conn",
			DBType:   "oracle",
			Host:     "192.168.1.100",
			Port:     1521,
			Service:  "ORCL",
			User:     "opendb",
			AuthMode: "prompt",
			Credential: config.Credential{Provider: "prompt"},
		},
		LLM: config.LLMConfig{
			Provider: "openai",
			BaseURL:  "https://api.moonshot.cn/v1",
			Model:    "kimi-k2.5",
		},
		LLMVendor: "Moonshot",
		LLMApiKey: "sk-test",
		Security: config.SecurityConfig{
			ConfirmOnDangerous: true,
		},
		Sentinel: config.Default().Sentinel,
	}

	err := GenerateConfig(cfg)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	// Check config.yaml exists
	configPath := filepath.Join(tmpDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("config.yaml not created")
	}

	content := string(data)

	// Check inline connection is present
	if !strings.Contains(content, "test-conn") {
		t.Error("config.yaml should contain inline connection 'test-conn'")
	}

	// Check inline model is present
	if !strings.Contains(content, "kimi-k2.5") {
		t.Error("config.yaml should contain inline model 'kimi-k2.5'")
	}

	// kimi-k2 is a mid-tier cloud model — v1.1.17 introduced "medium" tier
	// and reclassified Kimi/GLM/Qwen/DeepSeek from "large" → "medium" to
	// route them to the templated prompt variant. Frontier models (Opus,
	// GPT-5) stay at "large".
	if !strings.Contains(content, "capability: medium") {
		t.Errorf("config.yaml should contain 'capability: medium' for kimi-k2.5, got:\n%s", content)
	}

	// active_model uses the "default" alias since v1.1.20 (was raw model ID
	// like "kimi-k2.5", which broke /model switching when users wanted a
	// short alias). active_model + entry name must match.
	if !strings.Contains(content, "active_model: default") {
		t.Errorf("config.yaml should contain 'active_model: default' alias, got:\n%s", content)
	}
	if !strings.Contains(content, "name: default") {
		t.Errorf("config.yaml should contain inline model with name 'default', got:\n%s", content)
	}

	// models_dir must NOT be written: setup unifies all model config into
	// config.yaml `models:` (post-v1.1.20). The manager still merges any
	// pre-existing models_dir for backward compat, but new installs only use
	// inline so users have one place to look.
	if strings.Contains(content, "models_dir:") {
		t.Errorf("config.yaml should NOT contain 'models_dir:' (use inline models:), got:\n%s", content)
	}

	// Check no separate connection directory files
	if !strings.Contains(content, "connections:") {
		t.Error("config.yaml should have connections section")
	}
}

func TestGenerateConfig_SingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &SetupConfig{
		BaseDir: tmpDir,
		Connection: config.Connection{
			Name:       "mydb",
			Host:       "localhost",
			Port:       1521,
			Credential: config.Credential{Provider: "prompt"},
		},
		Sentinel: config.Default().Sentinel,
	}

	err := GenerateConfig(cfg)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	// Should only have config.yaml + history dir, no connections/ or models/ dirs
	configPath := filepath.Join(tmpDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config.yaml not created")
	}

	histDir := filepath.Join(tmpDir, "history")
	if _, err := os.Stat(histDir); os.IsNotExist(err) {
		t.Error("history directory not created")
	}
}
