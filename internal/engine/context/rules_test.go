/*-------------------------------------------------------------------------
 *
 * rules_test.go
 *	  Test cases for rules.go (context package): TestRulesLoaderEmpty,
 *	  TestRulesLoaderGlobal, TestRulesLoaderInstance.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/rules_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRulesLoaderEmpty(t *testing.T) {
	loader := NewRulesLoader("/nonexistent/path")
	result := loader.Load("")
	if result != "" {
		t.Errorf("expected empty for nonexistent dir, got %q", result)
	}
}

func TestRulesLoaderGlobal(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "standards.md"), []byte("# Company Standards\n- Always check locks first"), 0644)
	os.WriteFile(filepath.Join(dir, "safety.md"), []byte("# Safety\n- No kill in production"), 0644)

	loader := NewRulesLoader(dir)
	result := loader.Load("")

	if !strings.Contains(result, "Company Standards") {
		t.Error("expected global rules to contain 'Company Standards'")
	}
	if !strings.Contains(result, "Safety") {
		t.Error("expected global rules to contain 'Safety'")
	}
}

func TestRulesLoaderInstance(t *testing.T) {
	dir := t.TempDir()

	// Global rule
	os.WriteFile(filepath.Join(dir, "global.md"), []byte("# Global Rule"), 0644)

	// Instance-specific rule
	instanceDir := filepath.Join(dir, "orcl-prod")
	os.MkdirAll(instanceDir, 0755)
	os.WriteFile(filepath.Join(instanceDir, "strict.md"), []byte("# Strict Mode\n- No DDL allowed"), 0644)

	loader := NewRulesLoader(dir)
	result := loader.Load("orcl-prod")

	if !strings.Contains(result, "Global Rule") {
		t.Error("expected global rules")
	}
	if !strings.Contains(result, "Strict Mode") {
		t.Error("expected instance rules")
	}
	if !strings.Contains(result, "orcl-prod") {
		t.Error("expected instance name in output")
	}
}

func TestRulesLoaderNoInstance(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "global.md"), []byte("# Global"), 0644)

	loader := NewRulesLoader(dir)
	result := loader.Load("nonexistent-instance")

	if !strings.Contains(result, "Global") {
		t.Error("expected global rules even when instance dir missing")
	}
}

func TestRulesLoaderSortOrder(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "b-second.md"), []byte("SECOND"), 0644)
	os.WriteFile(filepath.Join(dir, "a-first.md"), []byte("FIRST"), 0644)

	loader := NewRulesLoader(dir)
	result := loader.Load("")

	firstIdx := strings.Index(result, "FIRST")
	secondIdx := strings.Index(result, "SECOND")
	if firstIdx > secondIdx {
		t.Error("expected alphabetical order: a-first before b-second")
	}
}

func TestRulesLoaderIgnoresNonMd(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "rules.md"), []byte("# Rules"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not loaded"), 0644)
	os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0644)

	loader := NewRulesLoader(dir)
	result := loader.Load("")

	if !strings.Contains(result, "Rules") {
		t.Error("expected .md file to be loaded")
	}
	if strings.Contains(result, "not loaded") {
		t.Error(".txt files should not be loaded")
	}
}

func TestRulesLoaderEmptyDir(t *testing.T) {
	loader := NewRulesLoader("")
	// Empty rulesDir → tries to use home dir, but won't have rules
	result := loader.Load("")
	// Should not panic, just return empty
	_ = result
}
