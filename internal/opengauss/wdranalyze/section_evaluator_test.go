/*-------------------------------------------------------------------------
 *
 * section_evaluator_test.go
 *	  Tests for the v1.1.51 deterministic rule engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/section_evaluator_test.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"strings"
	"testing"
)

// makeReport builds a WDRReport with a single section populated for one test.
func makeReport(sectionKey, content string, topSQLs ...TopSQLEntry) *WDRReport {
	return &WDRReport{
		RawSections: map[string]string{sectionKey: content},
		TopSQLs:     topSQLs,
	}
}

func TestEvaluator_DatabaseStat_TempBytesExtreme(t *testing.T) {
	// 12 GB temp bytes → 🔴
	stat := `Database Stat
DB Name | Backends | Xact Commit | Xact Rollback | Blks Read | Blks Hit | Tuple Returned | Tuple Fetched | Tuple Inserted | Tuple Updated | Tup Deleted | Conflicts | Temp Files | Temp Bytes | Deadlocks | Blk Read Time | Blk Write Time | Stats Reset |
postgres | 29 | 407 | 1 | 1096 | 258229 | 8888123 | 44576 | 14069 | 277 | 42 | 0 | 807 | 12493550144 | 0 | 0 | 0 | 2026-05-01 |
`
	r := makeReport(SectionDatabaseStat, stat)
	s := evaluateDatabaseStat(r)
	if s.Level != SectionRisk {
		t.Errorf("level: got %s, want risk", s.Level)
	}
	if len(s.Rules) == 0 || s.Rules[0].ID != "temp_bytes_extreme" {
		t.Errorf("expected temp_bytes_extreme rule, got %+v", s.Rules)
	}
}

func TestEvaluator_DatabaseStat_Clean(t *testing.T) {
	stat := `Database Stat
DB Name | Backends | Xact Commit | Xact Rollback | Blks Read | Blks Hit | Tuple Returned | Tuple Fetched | Tuple Inserted | Tuple Updated | Tup Deleted | Conflicts | Temp Files | Temp Bytes | Deadlocks | Blk Read Time | Blk Write Time | Stats Reset |
postgres | 5 | 100 | 0 | 50 | 50000 | 1000 | 100 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 2026-05-01 |
`
	r := makeReport(SectionDatabaseStat, stat)
	s := evaluateDatabaseStat(r)
	if s.Level != SectionGood {
		t.Errorf("clean stats should be good, got %s with rules %+v", s.Level, s.Rules)
	}
}

func TestEvaluator_DatabaseStat_Deadlock(t *testing.T) {
	stat := `Database Stat
DB Name | Backends | Xact Commit | Xact Rollback | Blks Read | Blks Hit | Tuple Returned | Tuple Fetched | Tuple Inserted | Tuple Updated | Tup Deleted | Conflicts | Temp Files | Temp Bytes | Deadlocks | Blk Read Time | Blk Write Time | Stats Reset |
postgres | 5 | 100 | 0 | 50 | 50000 | 1000 | 100 | 0 | 0 | 0 | 0 | 0 | 0 | 2 | 0 | 0 | 2026-05-01 |
`
	r := makeReport(SectionDatabaseStat, stat)
	s := evaluateDatabaseStat(r)
	if s.Level != SectionRisk {
		t.Errorf("deadlock should trigger risk, got %s", s.Level)
	}
	foundDeadlock := false
	for _, rule := range s.Rules {
		if rule.ID == "deadlock_present" {
			foundDeadlock = true
		}
	}
	if !foundDeadlock {
		t.Errorf("expected deadlock_present rule, got %+v", s.Rules)
	}
}

func TestEvaluator_InstanceEfficiency_SoftParseCritical(t *testing.T) {
	eff := `Instance Efficiency Percentages
Metric Name | Metric Value |
Buffer Hit % | 99.40 |
Soft Parse % | 5 |
WalWrite NoWait % | 100 |
Effective CPU % | 96 |
`
	r := makeReport(SectionInstanceEfficiency, eff)
	s := evaluateInstanceEfficiency(r)
	if s.Level != SectionRisk {
		t.Errorf("soft parse 5%% should be risk, got %s", s.Level)
	}
	if s.KeyMetrics["Soft Parse %"] != "5.00" {
		t.Errorf("KeyMetrics Soft Parse: got %q", s.KeyMetrics["Soft Parse %"])
	}
}

func TestEvaluator_InstanceEfficiency_BufferHitClean(t *testing.T) {
	eff := `Instance Efficiency Percentages
Metric Name | Metric Value |
Buffer Hit % | 99.40 |
Soft Parse % | 96 |
WalWrite NoWait % | 100 |
Effective CPU % | 95 |
`
	r := makeReport(SectionInstanceEfficiency, eff)
	s := evaluateInstanceEfficiency(r)
	if s.Level != SectionGood {
		t.Errorf("all healthy should be good, got %s with rules %+v", s.Level, s.Rules)
	}
}

func TestEvaluator_LoadProfile_P95High(t *testing.T) {
	load := `Load Profile
Metric | Per Second | Per Transaction | Per Exec |
DB Time(us) | 100 | 50 | 200 |
Logical read (blocks) | 100 | 50 | 200 |
Physical read (blocks) | 5 | 2 | 10 |
SQL response time P95(us) | 150000 |
SQL response time P80(us) | 100000 |
`
	r := makeReport(SectionLoadProfile, load)
	s := evaluateLoadProfile(r)
	if s.Level != SectionRisk {
		t.Errorf("P95=150ms should be risk, got %s", s.Level)
	}
}

func TestEvaluator_TopSQL_Top1Dominant(t *testing.T) {
	r := &WDRReport{
		TopSQLs: []TopSQLEntry{
			{SQLID: "111", TotalTimeMS: 5000, Calls: 1, QueryPrefix: "SELECT * FROM t"},
			{SQLID: "222", TotalTimeMS: 100, Calls: 10, QueryPrefix: "SELECT 1"},
			{SQLID: "333", TotalTimeMS: 50, Calls: 5, QueryPrefix: "SELECT 2"},
		},
	}
	s := evaluateTopSQL(r)
	if s.Level != SectionWarning {
		t.Errorf("top1 97%% should be warning, got %s", s.Level)
	}
	foundDominant := false
	for _, rule := range s.Rules {
		if rule.ID == "top1_dominant" {
			foundDominant = true
		}
	}
	if !foundDominant {
		t.Errorf("expected top1_dominant rule, got %+v", s.Rules)
	}
}

func TestEvaluator_TopSQL_ConnectionProbeFlood(t *testing.T) {
	r := &WDRReport{
		TopSQLs: []TopSQLEntry{
			{SQLID: "1", TotalTimeMS: 100, Calls: 40, QueryPrefix: "SET client_encoding to 'UTF8'"},
			{SQLID: "2", TotalTimeMS: 100, Calls: 30, QueryPrefix: "SHOW sql_compatibility"},
			{SQLID: "3", TotalTimeMS: 100, Calls: 15, QueryPrefix: "SELECT version()"},
		},
	}
	s := evaluateTopSQL(r)
	if s.Level != SectionWarning {
		t.Errorf("85 probes should be warning, got %s", s.Level)
	}
	foundFlood := false
	for _, rule := range s.Rules {
		if rule.ID == "connection_probe_flood" {
			foundFlood = true
		}
	}
	if !foundFlood {
		t.Errorf("expected connection_probe_flood rule")
	}
}

func TestEvaluator_TopSQL_Empty(t *testing.T) {
	s := evaluateTopSQL(&WDRReport{})
	if s.Level != SectionGood {
		t.Errorf("empty topsql should be good, got %s", s.Level)
	}
	if s.Summary != "无 TopSQL 数据" {
		t.Errorf("summary: got %q", s.Summary)
	}
}

func TestEvaluator_IsMaintenanceSQL(t *testing.T) {
	cases := map[string]bool{
		"CREATE INDEX foo ON bar(c)":     true,
		"create table x (id int)":        true,
		"ANALYZE bench_orders":           true,
		"VACUUM ANALYZE t":               true,
		"SELECT * FROM users":            false,
		"INSERT INTO t VALUES (1)":       false,
		"UPDATE t SET x=1":               false,
	}
	for sql, want := range cases {
		got := isMaintenanceSQL(sql)
		if got != want {
			t.Errorf("isMaintenanceSQL(%q): got %v, want %v", sql, got, want)
		}
	}
}

func TestEvaluator_IsConnectionProbe(t *testing.T) {
	cases := map[string]bool{
		"SET client_encoding to 'UTF8'":         true,
		"SHOW sql_compatibility":                true,
		"SELECT version()":                      true,
		"SELECT inet_server_addr()":             true,
		"SELECT * FROM users":                   false,
		"UPDATE t SET x=1":                      false,
	}
	for sql, want := range cases {
		got := isConnectionProbe(sql)
		if got != want {
			t.Errorf("isConnectionProbe(%q): got %v, want %v", sql, got, want)
		}
	}
}

func TestExtractRawSections_Empty(t *testing.T) {
	// Text-format input (no HTML anchors) returns empty map
	got := ExtractRawSections("just some text, no html\n")
	if len(got) != 0 {
		t.Errorf("text input should return empty map, got %d keys", len(got))
	}
}

func TestExtractRawSections_HasAnchor(t *testing.T) {
	html := `<html><body>
<h3 class="wdr" id="Database_Stat" onclick="msg()">-Database Stat</h3>
<table><tr><th>DB Name</th><th>Backends</th></tr><tr><td>postgres</td><td>5</td></tr></table>
<h3 class="wdr" id="Load_Profile" onclick="msg()">-Load Profile</h3>
<table><tr><th>Metric</th><th>Per Sec</th></tr><tr><td>DB Time</td><td>100</td></tr></table>
</body></html>`
	got := ExtractRawSections(html)
	if _, ok := got[SectionDatabaseStat]; !ok {
		t.Errorf("Database_Stat section missing")
	}
	if _, ok := got[SectionLoadProfile]; !ok {
		t.Errorf("Load_Profile section missing")
	}
	// Verify boundary cut: Database Stat should NOT contain "Load Profile"
	if dbStat := got[SectionDatabaseStat]; dbStat != "" {
		if containsLoadProfile := contains(dbStat, "Load Profile"); containsLoadProfile {
			t.Errorf("Database Stat section bled into Load Profile: %s", dbStat)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
