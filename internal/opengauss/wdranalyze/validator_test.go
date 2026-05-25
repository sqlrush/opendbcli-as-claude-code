/*-------------------------------------------------------------------------
 *
 * validator_test.go
 *	  Tests for the post-validation Plan B logic. Covers:
 *	    - Strong model (canonical output) → no patches applied (no-op)
 *	    - Weak model (missing sections) → placeholders appended
 *	    - Weak model (missing fallback findings) → 补充兜底 block appended
 *	    - Synonym matching: English headings / Chinese rewording accepted
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/validator_test.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"strings"
	"testing"
)

func TestValidateAndPatch_StrongModelOutput_NoOp(t *testing.T) {
	// Canonical output from Opus / GLM — all 4 sections present, all 3
	// fallback findings mentioned by name. Should pass through unchanged.
	strongOutput := `## 风险全景
3 严重 / 2 警告 / 1 提示

## 关键瓶颈
autovacuum 关闭导致 bloat 累积，叠加单 SQL 占 60% DB Time。
此外发现 deadlock 2 次。

## 配置调优
- autovacuum = on
- shared_buffers 调到 8G

## 综合评估
P0 需立即开启 autovacuum。
`
	fallback := []Finding{
		{ID: "autovacuum_off", Title: "autovacuum 已关闭", Severity: SeverityCritical},
		{ID: "deadlock_present", Title: "检测到死锁", Severity: SeverityCritical},
		{ID: "single_sql_dominant", Title: "单 SQL 主导 DB Time", Severity: SeverityWarning},
	}
	patched := ValidateAndPatch(strongOutput, fallback)
	if patched != strongOutput {
		t.Errorf("strong model output was patched, expected no-op.\ndiff:\n%s",
			diff(strongOutput, patched))
	}
}

func TestValidateAndPatch_EnglishHeadings_NoOp(t *testing.T) {
	// Some models emit English headings. Synonym matcher should accept.
	engOutput := `## Risk Overview
3 critical findings.

## Key Bottleneck
autovacuum is disabled.

## Configuration Tuning
turn on autovacuum.

## Summary
P0: enable autovacuum.
`
	fallback := []Finding{
		{ID: "autovacuum_off", Title: "autovacuum off"},
	}
	patched := ValidateAndPatch(engOutput, fallback)
	if patched != engOutput {
		t.Errorf("English-heading output was unnecessarily patched.\ndiff:\n%s",
			diff(engOutput, patched))
	}
}

func TestValidateAndPatch_MissingSection_PatchAppended(t *testing.T) {
	// v1.1.51: required sections are now Layer 1/2/3. Old aliases still match.
	// Weak model only writes Layer 1 + Layer 2 → expect Layer 3 placeholder.
	weakOutput := `## Layer 1: 总览评估
| 模块 | 评级 |...

## Layer 2: 风险详解
### R1 ...
`
	// "## Layer 3: 优化方案" missing.
	patched := ValidateAndPatch(weakOutput, []Finding{
		{ID: "autovacuum_off", Title: "autovacuum off"},
	})
	if !strings.Contains(patched, "## Layer 3: 优化方案") {
		t.Errorf("expected missing ## Layer 3 to be patched in, got:\n%s", patched)
	}
	// Existing sections should still be present.
	if !strings.Contains(patched, "## Layer 1: 总览评估") {
		t.Errorf("original section ## Layer 1 lost during patching")
	}
}

// TestValidateAndPatch_LegacyAliasMatch: outputs using the old "## 风险全景 /
// 关键瓶颈 / 配置调优" headers should still validate clean (alias match
// avoids appending Layer 1/2/3 placeholders).
func TestValidateAndPatch_LegacyAliasMatch(t *testing.T) {
	legacyOutput := `## 风险全景
2 严重

## 关键瓶颈
autovacuum 关闭

## 配置调优
开 autovacuum
`
	patched := ValidateAndPatch(legacyOutput, nil)
	if strings.Contains(patched, "*(LLM 未生成此段") {
		t.Errorf("legacy aliases should match — no placeholder expected. got:\n%s", patched)
	}
}

func TestValidateAndPatch_MissingFinding_FallbackBlockAppended(t *testing.T) {
	// LLM mentions only 1 of 2 findings — the other gets a 补充兜底 block.
	weakOutput := `## 风险全景
1 严重

## 关键瓶颈
autovacuum 已关闭。

## 配置调优
开启 autovacuum。

## 综合评估
P0。
`
	fallback := []Finding{
		{ID: "autovacuum_off", Title: "autovacuum 已关闭", Severity: SeverityCritical,
			Evidence:   []string{"pg_settings.autovacuum = off"},
			Suggestion: "ALTER SYSTEM SET autovacuum = on"},
		{ID: "deadlock_present", Title: "检测到死锁", Severity: SeverityCritical,
			Evidence:   []string{"deadlock_count = 5"},
			Suggestion: "review lock contention"},
	}
	patched := ValidateAndPatch(weakOutput, fallback)
	if !strings.Contains(patched, "补充兜底警告") {
		t.Errorf("expected 补充兜底警告 block for missing finding, got:\n%s", patched)
	}
	if !strings.Contains(patched, "检测到死锁") {
		t.Errorf("expected missing deadlock finding title in patched output")
	}
	if !strings.Contains(patched, "deadlock_count = 5") {
		t.Errorf("expected evidence string in patched output")
	}
	// autovacuum was mentioned — should NOT also appear in 补充兜底 block.
	suppIdx := strings.Index(patched, "补充兜底警告")
	if suppIdx >= 0 {
		supp := patched[suppIdx:]
		if strings.Contains(supp, "autovacuum 已关闭") {
			t.Errorf("autovacuum was mentioned but still got added to 补充兜底 block")
		}
	}
}

func TestValidateAndPatch_EmptyOutput_Untouched(t *testing.T) {
	// Empty input → empty output (don't fabricate sections from nothing).
	got := ValidateAndPatch("", []Finding{{ID: "autovacuum_off"}})
	if got != "" {
		t.Errorf("expected empty output for empty input, got %q", got)
	}
	got = ValidateAndPatch("   \n  ", []Finding{{ID: "autovacuum_off"}})
	if got != "   \n  " {
		t.Errorf("expected whitespace-only input untouched, got %q", got)
	}
}

func TestValidateAndPatch_NoFallback_OnlySectionCheck(t *testing.T) {
	// No fallback findings → only section check applies.
	out := `## 风险全景
无风险。

## 关键瓶颈
无。

## 配置调优
无。

## 综合评估
健康。
`
	patched := ValidateAndPatch(out, nil)
	if patched != out {
		t.Errorf("complete output + no fallback should be untouched")
	}
	if strings.Contains(patched, "补充兜底警告") {
		t.Errorf("no fallback findings but 补充兜底警告 block appeared")
	}
}

func TestFindingSignatureKeywords_AllFiveRulesCovered(t *testing.T) {
	// Guard against silent typos in keyword table.
	rules := []string{
		"autovacuum_off", "deadlock_present", "replication_lag_high",
		"buffer_hit_critical", "single_sql_dominant",
	}
	for _, id := range rules {
		if len(findingSignatureKeywords(id)) == 0 {
			t.Errorf("rule %q has no signature keywords — silent miss-detection risk", id)
		}
	}
}

func TestFindingMentioned_LenientMatching(t *testing.T) {
	cases := []struct {
		ruleID string
		text   string
		want   bool
	}{
		// autovacuum_off
		{"autovacuum_off", "autovacuum is disabled", true},
		{"autovacuum_off", "AUTO VACUUM 已关闭", true},
		{"autovacuum_off", "自动 VACUUM 没开", true},
		{"autovacuum_off", "this is unrelated text", false},
		// deadlock_present
		{"deadlock_present", "出现 deadlock", true},
		{"deadlock_present", "死锁 5 次", true},
		// replication_lag_high
		{"replication_lag_high", "replication lag 60s", true},
		{"replication_lag_high", "主备延迟过高", true},
		{"replication_lag_high", "standby lag is critical", true},
		// buffer_hit_critical
		{"buffer_hit_critical", "buffer hit ratio 78%", true},
		{"buffer_hit_critical", "缓冲池命中率不足", true},
		// single_sql_dominant
		{"single_sql_dominant", "单 SQL 占 DB Time 60%", true},
		{"single_sql_dominant", "single SQL dominates", true},
	}
	for _, tc := range cases {
		f := Finding{ID: tc.ruleID}
		got := findingMentioned(strings.ToLower(tc.text), f)
		if got != tc.want {
			t.Errorf("findingMentioned(%q, id=%q) = %v, want %v",
				tc.text, tc.ruleID, got, tc.want)
		}
	}
}

// diff is a tiny helper for readable test failure messages.
func diff(a, b string) string {
	if a == b {
		return "(equal)"
	}
	return "--- expected ---\n" + a + "\n--- got ---\n" + b
}
