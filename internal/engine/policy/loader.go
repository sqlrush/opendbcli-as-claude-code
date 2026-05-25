/*-------------------------------------------------------------------------
 *
 * loader.go
 *	  Loader loads and merges policies from a 4-level hierarchy:
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/policy/loader.go
 *
 *-------------------------------------------------------------------------
 */
package policy

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader loads and merges policies from a 4-level hierarchy:
//
//	Level 1 — Platform default (PromptProfile, passed as argument)
//	Level 2 — Organization  (policies/org/*.md)
//	Level 3 — Instance      (policies/{instance}/*.md)
//	Level 4 — Session       (API parameter, passed as argument)
//
// Priority: Organization > Instance > Session.
// Organization rules are the highest priority and cannot be overridden.
// Policies are READ-ONLY to the LLM.
type Loader struct {
	policiesDir string // e.g. ~/.opendb/policies/
}

// NewLoader creates a policy loader.
func NewLoader(policiesDir string) *Loader {
	return &Loader{policiesDir: policiesDir}
}

// LoadedPolicies holds the merged result of all policy levels.
type LoadedPolicies struct {
	Platform string // Level 1: from PromptProfile (not included in Merged)
	Org      string // Level 2: organization policies
	Instance string // Level 3: instance policies
	Session  string // Level 4: session-level policies
	Merged   string // Final merged text for prompt injection
}

// Load reads policies from all levels and merges them.
func (l *Loader) Load(platformRules, instanceName, sessionPolicies string) LoadedPolicies {
	p := LoadedPolicies{
		Platform: platformRules,
		Session:  sessionPolicies,
	}

	// Level 2: Organization policies
	if l.policiesDir != "" {
		orgDir := filepath.Join(l.policiesDir, "org")
		p.Org = loadDir(orgDir)
	}

	// Level 3: Instance policies
	if l.policiesDir != "" && instanceName != "" {
		instanceDir := filepath.Join(l.policiesDir, instanceName)
		p.Instance = loadDir(instanceDir)
	}

	p.Merged = merge(p)
	return p
}

// merge combines all policy levels into a single prompt-ready text.
// Includes explicit priority declarations so the LLM knows which rules
// take precedence when they conflict.
func merge(p LoadedPolicies) string {
	var parts []string

	if p.Org != "" {
		parts = append(parts, "## 组织规范（优先级最高，所有实例必须遵守）\n\n"+p.Org)
	}
	if p.Instance != "" {
		parts = append(parts, "## 实例规范（当与组织规范冲突时，以组织规范为准）\n\n"+p.Instance)
	}
	if p.Session != "" {
		parts = append(parts, "## 本次会话规范（仅限本次诊断）\n\n"+p.Session)
	}

	if len(parts) == 0 {
		return ""
	}

	return "# 诊断规范\n\n" +
		"IMPORTANT: 以下规范必须严格遵守。优先级：组织规范 > 实例规范 > 会话规范。\n" +
		"当规范之间冲突时，遵守优先级更高的规范。\n\n" +
		strings.Join(parts, "\n\n")
}

// loadDir reads all .md files from a directory, sorted alphabetically,
// and concatenates their contents.
func loadDir(dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) == 0 {
		return ""
	}
	sort.Strings(files)

	var parts []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}
