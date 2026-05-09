/*-------------------------------------------------------------------------
 *
 * rule_writer.go
 *	  rule_writer.go implements automatic rule generation from
 *	  successful repairs. After a successful diagnosis + repair cycle,
 *	  the LLM generates a rule file with trigger conditions so future
 *	  similar anomalies can be matched instantly.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/rule_writer.go
 *
 *-------------------------------------------------------------------------
 */
// rule_writer.go implements automatic rule generation from successful repairs.
// After a successful diagnosis + repair cycle, the LLM generates a rule file
// with trigger conditions so future similar anomalies can be matched instantly.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/skill"
)

// RuleWriter manages automatic rule generation and frontmatter-based indexing.
type RuleWriter struct {
	mu       sync.RWMutex
	baseDir  string
	instance string
	index    map[string][]string // metric → []rulePaths (in-memory index)
	logger   *slog.Logger
}

// NewRuleWriter creates a rule writer for the given instance.
func NewRuleWriter(baseDir, instance string) *RuleWriter {
	rw := &RuleWriter{
		baseDir:  baseDir,
		instance: instance,
		index:    make(map[string][]string),
		logger:   cluster.DefaultLogger("rule-writer"),
	}
	rw.rebuildIndex()
	return rw
}

// WriteRule saves a new rule file with frontmatter trigger.
func (rw *RuleWriter) WriteRule(metric, condition, content, sourceMemory string, tags []string) (string, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	dir := rw.ruleDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create rule dir: %w", err)
	}

	// Generate filename from metric.
	safeName := strings.ReplaceAll(strings.ToLower(metric), " ", "_")
	fileName := fmt.Sprintf("rule_%s_%s.md", safeName, time.Now().Format("20060102"))
	path := filepath.Join(dir, fileName)

	// Build frontmatter.
	tagStr := ""
	if len(tags) > 0 {
		tagStr = fmt.Sprintf("tags: [%s]\n", strings.Join(tags, ", "))
	}

	fileContent := fmt.Sprintf(`---
trigger:
  metric: %s
  condition: "%s"
%screated: %s
source: %s
---
%s
`, metric, condition, tagStr, time.Now().Format(time.RFC3339), sourceMemory, content)

	if err := os.WriteFile(path, []byte(fileContent), 0644); err != nil {
		return "", fmt.Errorf("write rule: %w", err)
	}

	// Update in-memory index.
	rw.index[metric] = appendUniqueStr(rw.index[metric], path)

	rw.logger.Info("Saved rule", slog.String("file", fileName), slog.String("trigger_metric", metric), slog.String("trigger_condition", condition))
	return path, nil
}

// MatchRules returns rule file paths that match the given metric.
func (rw *RuleWriter) MatchRules(metric string) []string {
	rw.mu.RLock()
	defer rw.mu.RUnlock()

	paths, exists := rw.index[metric]
	if !exists {
		return nil
	}

	result := make([]string, len(paths))
	copy(result, paths)
	return result
}

// LoadMatchedContent reads and returns the content of all rules matching a metric.
func (rw *RuleWriter) LoadMatchedContent(metric string) string {
	paths := rw.MatchRules(metric)
	if len(paths) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- %s ---\n", filepath.Base(path)))
		sb.Write(content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// RuleCount returns the total number of indexed rules.
func (rw *RuleWriter) RuleCount() int {
	rw.mu.RLock()
	defer rw.mu.RUnlock()

	total := 0
	for _, paths := range rw.index {
		total += len(paths)
	}
	return total
}

// rebuildIndex scans all rule files and builds the in-memory index from frontmatter.
func (rw *RuleWriter) rebuildIndex() {
	dir := rw.ruleDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		metric := extractMetricFromFrontmatter(path)
		if metric != "" {
			rw.index[metric] = appendUniqueStr(rw.index[metric], path)
		}
	}

	if len(rw.index) > 0 {
		total := 0
		for _, paths := range rw.index {
			total += len(paths)
		}
		rw.logger.Info("Indexed rules", slog.Int("total", total), slog.Int("metrics", len(rw.index)))
	}
}

// extractMetricFromFrontmatter reads the trigger.metric from a rule file's frontmatter.
func extractMetricFromFrontmatter(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	content := string(data)
	if !strings.HasPrefix(content, "---") {
		return ""
	}

	// Find end of frontmatter.
	endIdx := strings.Index(content[3:], "---")
	if endIdx < 0 {
		return ""
	}

	frontmatter := content[3 : 3+endIdx]
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "metric:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "metric:"))
		}
	}
	return ""
}

func (rw *RuleWriter) ruleDir() string {
	return filepath.Join(rw.baseDir, "rules", rw.instance)
}

func appendUniqueStr(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

// ---- rule_write Skill ----

// RuleWriteSkill allows LLM to persist a diagnostic rule after successful repair.
type RuleWriteSkill struct {
	writer *RuleWriter
}

func NewRuleWriteSkill(writer *RuleWriter) *RuleWriteSkill {
	return &RuleWriteSkill{writer: writer}
}

func (s *RuleWriteSkill) Name() string        { return "rule-write" }
func (s *RuleWriteSkill) Description() string { return "Save a diagnostic rule from successful repair experience" }
func (s *RuleWriteSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *RuleWriteSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "rule_write",
		Description: "After a successful diagnosis and repair, save the experience as a reusable rule. The rule will be auto-loaded when the same anomaly metric triggers in the future.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"metric":    map[string]any{"type": "string", "description": "Sentinel metric name that triggers this rule (e.g., temp_tablespace_usage)"},
				"condition": map[string]any{"type": "string", "description": "Trigger condition (e.g., '> 85%')"},
				"content":   map[string]any{"type": "string", "description": "Rule content: what was the root cause and how to fix it"},
				"tags":      map[string]any{"type": "string", "description": "Comma-separated tags for this rule"},
				"source":    map[string]any{"type": "string", "description": "Source memory file this rule is derived from"},
			},
			"required": []string{"metric", "condition", "content"},
		},
	}
}

func (s *RuleWriteSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "rule-write",
		Usage:   "/rule-write --metric <name> --condition <expr> --content <text>",
	}
}

func (s *RuleWriteSkill) Validate(params skill.Params) error {
	if params.StringOr("metric", "") == "" {
		return fmt.Errorf("--metric is required")
	}
	if params.StringOr("content", "") == "" {
		return fmt.Errorf("--content is required")
	}
	return nil
}

func (s *RuleWriteSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	metric := params.StringOr("metric", "")
	condition := params.StringOr("condition", "")
	content := params.StringOr("content", "")
	tagsStr := params.StringOr("tags", "")
	source := params.StringOr("source", "")

	var tags []string
	if tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			tags = append(tags, strings.TrimSpace(t))
		}
	}

	path, err := s.writer.WriteRule(metric, condition, content, source, tags)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: fmt.Sprintf("Failed to write rule: %v", err),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Rule saved: %s\n  Trigger: %s %s\n  Tags: %s\n", filepath.Base(path), metric, condition, tagsStr),
		Summary:  fmt.Sprintf("rule saved for %s", metric),
	}, nil
}
