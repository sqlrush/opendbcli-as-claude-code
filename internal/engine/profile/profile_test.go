/*-------------------------------------------------------------------------
 *
 * profile_test.go
 *	  Test cases for profile.go (profile package): TestNewProfileOracle,
 *	  TestNewProfileMySQL, TestNewProfileUnknown.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/profile_test.go
 *
 *-------------------------------------------------------------------------
 */
package profile

import (
	"strings"
	"testing"
)

func TestNewProfileOracle(t *testing.T) {
	p := NewProfile("oracle")
	if p.Product() != "oracle" {
		t.Errorf("expected 'oracle', got %q", p.Product())
	}
}

func TestNewProfileMySQL(t *testing.T) {
	p := NewProfile("mysql")
	if p.Product() != "mysql" {
		t.Errorf("expected 'mysql', got %q", p.Product())
	}
}

func TestNewProfileGaussDBUsesOpenGaussKnowledgeWithGaussDBIdentity(t *testing.T) {
	p := NewProfile("gaussdb")
	if p.Product() != "gaussdb" {
		t.Fatalf("expected gaussdb product, got %q", p.Product())
	}
	rules := p.SystemPromptRules()
	for _, want := range []string{"GaussDB", "dbe_perf", "WDR", "MOT"} {
		if !strings.Contains(rules, want) {
			t.Fatalf("GaussDB profile should include %q in rules", want)
		}
	}
}

func TestNewProfileUnknown(t *testing.T) {
	p := NewProfile("unknown")
	if p.Product() != "unknown" {
		t.Errorf("expected 'unknown', got %q", p.Product())
	}
}

func TestOracleSystemPromptRules(t *testing.T) {
	p := &OracleProfile{}
	rules := p.SystemPromptRules()

	checks := []string{
		"对象引用规则",
		"ISEQ$$_",
		"等待事件速查",
		"db file sequential read",
		"enq: TX",
		"参数修改注意",
		"ORA-01555",
	}
	for _, check := range checks {
		if !strings.Contains(rules, check) {
			t.Errorf("Oracle rules should contain %q", check)
		}
	}
}

func TestMySQLSystemPromptRules(t *testing.T) {
	p := &MySQLProfile{}
	rules := p.SystemPromptRules()

	if !strings.Contains(rules, "lower_case_table_names") {
		t.Error("MySQL rules should mention lower_case_table_names")
	}
	if !strings.Contains(rules, "InnoDB") {
		t.Error("MySQL rules should mention InnoDB")
	}
}

func TestPostgresSystemPromptRules(t *testing.T) {
	p := &PostgresProfile{}
	rules := p.SystemPromptRules()

	if !strings.Contains(rules, "VACUUM") {
		t.Error("PG rules should mention VACUUM")
	}
	if !strings.Contains(rules, "XID wraparound") {
		t.Error("PG rules should mention XID wraparound")
	}
}

func TestOracleToolUsageHint(t *testing.T) {
	p := &OracleProfile{}

	hint := p.ToolUsageHint("waits")
	if hint == "" {
		t.Error("waits should have a hint")
	}
	if !strings.Contains(hint, "诊断入口") {
		t.Error("waits hint should mention diagnostic entry")
	}

	hint = p.ToolUsageHint("nonexistent")
	if hint != "" {
		t.Error("nonexistent tool should return empty hint")
	}
}

func TestToolFilterPlaybook(t *testing.T) {
	p := &OracleProfile{}
	filter := p.ToolFilter("playbook")

	if filter("waits", 0) {
		t.Error("playbook mode should exclude all tools")
	}
}

func TestToolFilterAssist(t *testing.T) {
	p := &OracleProfile{}
	filter := p.ToolFilter("assist")

	if !filter("waits", 0) {
		t.Error("assist should include read-only tools")
	}
	if filter("kill", 1) {
		t.Error("assist should exclude write tools")
	}
}

func TestToolFilterAuto(t *testing.T) {
	p := &OracleProfile{}
	filter := p.ToolFilter("auto")

	if !filter("waits", 0) {
		t.Error("auto should include read-only tools")
	}
	if !filter("kill", 1) {
		t.Error("auto should include write tools")
	}
}

func TestDefaultMaxTurns(t *testing.T) {
	p := &OracleProfile{}

	if p.DefaultMaxTurns("playbook") != 1 {
		t.Error("playbook should be 1 turn")
	}
	if p.DefaultMaxTurns("assist") != 10 {
		t.Error("assist should be 10 turns")
	}
	if p.DefaultMaxTurns("auto") != 20 {
		t.Error("auto should be 20 turns")
	}
}

func TestOpenGaussReusesPostgresHints(t *testing.T) {
	og := &OpenGaussProfile{}
	pg := &PostgresProfile{}

	// Shared tools (no OG-specific nuance) fall through to PG hints so the
	// four-DB base vocabulary stays aligned. Add new entries to ogHints only
	// when OG-specific framing helps the LLM.
	sharedTools := []string{"waits", "topsql", "slowsql", "explain", "locks", "blocktree", "sql"}
	for _, tool := range sharedTools {
		if og.ToolUsageHint(tool) != pg.ToolUsageHint(tool) {
			t.Errorf("OpenGauss should reuse PG hint for shared tool %q", tool)
		}
	}
}

func TestOpenGaussSystemPromptRules(t *testing.T) {
	p := &OpenGaussProfile{}
	rules := p.SystemPromptRules()

	// These markers guard against regressions in the OG-specific knowledge
	// the LLM is given. If you remove a section, the test tells you loudly.
	checks := []string{
		// OG-specific core
		"gs_",
		"dbe_perf",
		"WDR",
		"MOT",
		"CM",
		"Workload Manager",
		// Wait event taxonomy must be present
		"LWLock:BufferContent",
		"LWLock:WALInsert",
		"Lock:transactionid",
		"IO:DataFileRead",
		// MVCC/XID knowledge
		"XID wraparound",
		"xid_age",
		"autovacuum_freeze_max_age",
		// Safety rules
		"CREATE INDEX CONCURRENTLY",
		"pg_repack",
	}
	for _, check := range checks {
		if !strings.Contains(rules, check) {
			t.Errorf("OpenGauss rules should contain %q", check)
		}
	}

	// Density floor: v1.1.11 deliberately slimmed OG profile to align with
	// Oracle's "knowledge over meta-rules" approach (≈120 lines). Guard
	// against accidental regression in either direction:
	//   - too few lines → core knowledge tables got deleted
	//   - too many lines → meta-rules / few-shot / restrictions crept back
	lineCount := strings.Count(rules, "\n")
	if lineCount < 100 || lineCount > 200 {
		t.Errorf("OpenGauss rules line count out of band: %d (want 100-200)", lineCount)
	}
}

func TestOpenGaussToolUsageHintOGSpecific(t *testing.T) {
	og := &OpenGaussProfile{}

	// OG-specific tools must not fall through to PG (which has no hint for
	// them or a less specific one) — the LLM needs OG-tailored phrasing.
	ogOnlyTools := []string{
		"gsmem", "respool", "wdr", "planhistory", "lwlocks",
		"autovacuum", "checkpoint", "bgworker", "hotkey", "mot", "cmha",
	}
	for _, tool := range ogOnlyTools {
		hint := og.ToolUsageHint(tool)
		if hint == "" {
			t.Errorf("OG-specific tool %q has no hint", tool)
		}
	}
}
