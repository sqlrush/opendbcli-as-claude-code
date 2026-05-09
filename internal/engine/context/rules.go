/*-------------------------------------------------------------------------
 *
 * rules.go
 *	  RulesLoader loads user-defined rules from ~/.opendb/rules/
 *	  directory. 3-level priority: global → instance-specific →
 *	  session.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/rules.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RulesLoader loads user-defined rules from ~/.opendb/rules/ directory.
// 3-level priority: global → instance-specific → session.
type RulesLoader struct {
	rulesDir string // Default: ~/.opendb/rules/
}

// NewRulesLoader creates a loader. If rulesDir is empty, defaults to ~/.opendb/rules/.
func NewRulesLoader(rulesDir string) *RulesLoader {
	if rulesDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			rulesDir = filepath.Join(home, ".opendb", "policies")
		}
	}
	return &RulesLoader{rulesDir: rulesDir}
}

// Load reads and merges rules for the given instance.
// Returns empty string if no rules found (not an error).
func (l *RulesLoader) Load(instanceName string) string {
	if l.rulesDir == "" {
		return ""
	}

	var sections []string

	// Level 1: Global rules (*.md files directly in rulesDir)
	globalRules := l.loadDir(l.rulesDir)
	if globalRules != "" {
		sections = append(sections, globalRules)
	}

	// Level 2: Instance-specific rules (rulesDir/{instance}/*.md)
	if instanceName != "" {
		instanceDir := filepath.Join(l.rulesDir, instanceName)
		instanceRules := l.loadDir(instanceDir)
		if instanceRules != "" {
			sections = append(sections,
				"# 实例 "+instanceName+" 专属规则\n\n"+instanceRules)
		}
	}

	if len(sections) == 0 {
		return ""
	}

	return strings.Join(sections, "\n\n")
}

// loadDir reads all *.md files from a directory, sorted alphabetically.
func (l *RulesLoader) loadDir(dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) == 0 {
		return ""
	}

	sort.Strings(files) // Deterministic order

	var parts []string
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(content))
		if text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n\n")
}
