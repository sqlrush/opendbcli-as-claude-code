/*-------------------------------------------------------------------------
 *
 * json_provider.go
 *	  JSONRuleProvider loads and serves rules from JSON files.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/json_provider.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlrush/opendb/internal/oracle/ruleengine/rules_json"
)

// ─── JSON Rule Provider ──────────────────────────────────────────────────────
//
// Loads JSON rules from an embedded filesystem (go:embed) and provides them
// to the engine via the RuleProvider interface.

// JSONRuleProvider loads and serves rules from JSON files.
type JSONRuleProvider struct {
	rules    []*Rule
	registry *DynamicQueryRegistry
	version  string
	edition  string
	errors   []string // rules that failed to load
}

// NewJSONRuleProvider creates a provider by loading all JSON files from the given fs.
// Files must match *.json. Malformed files are skipped with warnings.
func NewJSONRuleProvider(fsys fs.FS, version string) *JSONRuleProvider {
	p := &JSONRuleProvider{
		registry: NewDynamicQueryRegistry(),
		version:  version,
		edition:  "json",
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		log.Printf("[json-provider] failed to read directory: %v", err)
		return p
	}

	seen := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// Skip metadata/summary files
		if strings.HasPrefix(entry.Name(), "_") {
			continue
		}

		data, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			p.errors = append(p.errors, entry.Name()+": "+err.Error())
			continue
		}

		rule, err := ParseJSONRuleBytes(data, p.registry)
		if err != nil {
			p.errors = append(p.errors, entry.Name()+": "+err.Error())
			continue
		}

		// Skip duplicates silently
		if seen[rule.ID] {
			continue
		}
		seen[rule.ID] = true

		p.rules = append(p.rules, rule)
	}

	// Startup log suppressed — noisy in batch mode and interactive REPL.
	if false && len(p.errors) > 0 {
		log.Printf("[json-provider] loaded %d rules (%d errors) from %s",
			len(p.rules), len(p.errors), version)
	}

	return p
}

// NewJSONRuleProviderFromDir creates a provider by loading JSON files from a directory path.
// This is used for development/testing when files are not embedded.
func NewJSONRuleProviderFromDir(dir string, version string) *JSONRuleProvider {
	p := &JSONRuleProvider{
		registry: NewDynamicQueryRegistry(),
		version:  version,
		edition:  "json",
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		log.Printf("[json-provider] failed to glob directory: %v", err)
		return p
	}

	seen := make(map[string]bool)

	for _, path := range matches {
		name := filepath.Base(path)
		// Skip metadata files
		if strings.HasPrefix(name, "_") {
			continue
		}

		data, err := readFileBytes(path)
		if err != nil {
			p.errors = append(p.errors, name+": "+err.Error())
			continue
		}

		rule, err := ParseJSONRuleBytes(data, p.registry)
		if err != nil {
			p.errors = append(p.errors, name+": "+err.Error())
			continue
		}

		if seen[rule.ID] {
			continue
		}
		seen[rule.ID] = true

		p.rules = append(p.rules, rule)
	}

	if len(p.errors) > 0 {
		log.Printf("[json-provider] loaded %d rules (%d errors) from dir %s",
			len(p.rules), len(p.errors), dir)
	}

	return p
}

// NewJSONRuleProviderFromEmbedded creates a provider from the embedded Oracle JSON rules.
func NewJSONRuleProviderFromEmbedded(version string) *JSONRuleProvider {
	return NewJSONRuleProvider(rules_json.RuleFiles, version)
}

// readFileBytes reads a file by path.
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Rules returns all loaded rules.
func (p *JSONRuleProvider) Rules() []*Rule { return p.rules }

// Version returns the JSON rule set version.
func (p *JSONRuleProvider) Version() string { return p.version }

// Edition returns "json".
func (p *JSONRuleProvider) Edition() string { return p.edition }

// QueryRegistry returns the dynamic query registry for this provider.
func (p *JSONRuleProvider) QueryRegistry() *DynamicQueryRegistry { return p.registry }

// Errors returns any loading errors that occurred.
func (p *JSONRuleProvider) Errors() []string { return p.errors }

// RuleCount returns the number of successfully loaded rules.
func (p *JSONRuleProvider) RuleCount() int { return len(p.rules) }
