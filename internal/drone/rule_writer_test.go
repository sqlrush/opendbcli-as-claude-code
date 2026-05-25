/*-------------------------------------------------------------------------
 *
 * rule_writer_test.go
 *	  Test cases for rule_writer.go (drone package):
 *	  TestRuleWriter_WriteAndMatch, TestRuleWriter_RebuildIndex.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/rule_writer_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuleWriter_WriteAndMatch(t *testing.T) {
	tmpDir := t.TempDir()

	rw := NewRuleWriter(tmpDir, "test-instance")

	path, err := rw.WriteRule(
		"temp_tablespace_usage",
		"> 85%",
		"TEMP 爆满通常由 orders 表 HASH JOIN 导致",
		"incident_temp_20260409.md",
		[]string{"temp", "hash_join"},
	)
	if err != nil {
		t.Fatalf("WriteRule: %v", err)
	}

	// Check file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("rule file not created")
	}

	// Check content has frontmatter.
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "metric: temp_tablespace_usage") {
		t.Error("missing metric in frontmatter")
	}
	if !strings.Contains(string(content), "tags: [temp, hash_join]") {
		t.Error("missing tags in frontmatter")
	}

	// Check index matching.
	matched := rw.MatchRules("temp_tablespace_usage")
	if len(matched) != 1 {
		t.Errorf("expected 1 match, got %d", len(matched))
	}

	// No match for different metric.
	matched = rw.MatchRules("io_latency")
	if len(matched) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matched))
	}
}

func TestRuleWriter_RebuildIndex(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a rule file manually.
	ruleDir := filepath.Join(tmpDir, "rules", "test-instance")
	os.MkdirAll(ruleDir, 0755)
	os.WriteFile(filepath.Join(ruleDir, "rule_io.md"), []byte(`---
trigger:
  metric: io_read_latency
  condition: "> 100ms"
---
Check multipath status.
`), 0644)

	// Create RuleWriter — should rebuild index from existing files.
	rw := NewRuleWriter(tmpDir, "test-instance")

	if rw.RuleCount() != 1 {
		t.Errorf("expected 1 rule, got %d", rw.RuleCount())
	}

	matched := rw.MatchRules("io_read_latency")
	if len(matched) != 1 {
		t.Errorf("expected 1 match, got %d", len(matched))
	}
}
