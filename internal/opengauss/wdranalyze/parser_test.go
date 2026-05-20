/*-------------------------------------------------------------------------
 *
 * parser_test.go
 *	  Unit tests for the WDR parser. Uses a representative WDR text
 *	  fragment so we can exercise the main code paths without needing
 *	  a live og snapshot.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/parser_test.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"strings"
	"testing"
)

const sampleWDRText = `
========== WDR Workload Diagnosis Report ==========

Database Version : openGauss 5.0.3
Host Name        : 82.4.89.165
Instance ID      : gaussdb_inst_1
Begin Snap Id    : 15234
Begin Snap Time  : 2026-05-16 10:00:00
End Snap Id      : 15247
End Snap Time    : 2026-05-16 11:00:00

== Time Model ==
DB Time          : 29723.5
CPU Time         : 8240.1
Wait Time        : 21483.4
Parse Time       : 142.3
Execution Time   : 28200.0
Hard Parse Count : 1834
Soft Parse Count : 12005

== Top Wait Events ==
event_name              | category   | wait_count | wait_time_ms  | avg_wait_ms | pct
------------------------|------------|------------|---------------|-------------|----
lock_wait_acquire       | Lock       | 4521       | 13432000.0    | 2970.5      | 45.2
io_wait_read            | IO         | 12450      | 4250000.0     | 341.4       | 14.3
network_wait            | Network    | 8120       | 2100000.0     | 258.6       | 7.1
buffer_io_completion    | IO         | 5440       | 1820000.0     | 334.5       | 6.1

== Top SQL by Elapsed Time ==
sql_id            | calls | total_time_ms | avg_time_ms | rows | query
------------------|-------|---------------|-------------|------|-------
3142685891        | 234   | 4142000.0     | 17700.0     | 50   | SELECT c.name, p.product_name FROM customers c JOIN orders o ON c.customer_id = o.customer_id ...
1923014772        | 1827  | 14250000.0    | 7798.6      | 1    | UPDATE fault_lock SET counter = counter + 1 WHERE id = 1
809407316         | 12    | 1842000.0     | 153500.0    | 100  | WITH region_filter AS (SELECT region_id FROM regions WHERE region_id <= 50) SELECT c.name ...

== Top SQL by Buffer Reads ==
sql_id            | calls | total_io   | avg_io   | query
------------------|-------|------------|----------|-------
3142685891        | 234   | 89400000   | 382000   | SELECT c.name, p.product_name FROM customers c JOIN orders o ON c.customer_id = o.customer_id ...
2517886443        | 5432  | 24500000   | 4500     | SELECT * FROM products WHERE category_id = ?

== Buffer Pool ==
Blocks Read      : 2840000
Blocks Hit       : 29400000
Buffer Hit Ratio : 91.18%
WAL Written      : 111600.0
Temp Files       : 0

== Memory ==
max_process_memory   : 32768
process_used_memory  : 24500
dynamic_used_memory  : 8900
shared_buffers       : 4096
work_mem             : 64

== Lock Stats ==
lock_wait_count    : 4521
lock_wait_time_ms  : 13432000.0
deadlock           : 0
lwlock_wait_count  : 280
lwlock_wait_time_ms: 12500.0

== Replication ==
standby_count    : 2
max_lag_seconds  : 12.0
sync_mode        : async

== Configuration ==
shared_buffers              | 4096 MB
work_mem                    | 64 MB
autovacuum                  | off
wal_writer_delay            | 200 ms
effective_cache_size        | 12288 MB
track_stmt_stat_level       | L1,L1
track_activity_query_size   | 1024
default_statistics_target   | 100
`

func TestParse_HeaderOK(t *testing.T) {
	r, err := Parse(sampleWDRText)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if r.Header.DBVersion != "openGauss 5.0.3" {
		t.Errorf("db version mismatch: got %q", r.Header.DBVersion)
	}
	if !strings.Contains(r.Header.InstanceHost, "82.4.89.165") {
		t.Errorf("host mismatch: got %q", r.Header.InstanceHost)
	}
	if r.Header.SnapshotIDStart != 15234 {
		t.Errorf("snap start: got %d, want 15234", r.Header.SnapshotIDStart)
	}
	if r.Header.SnapshotIDEnd != 15247 {
		t.Errorf("snap end: got %d, want 15247", r.Header.SnapshotIDEnd)
	}
	if r.Header.WindowDuration() < 50*60*1e9 { // need ~1h
		t.Errorf("window duration too short: %s", r.Header.WindowDuration())
	}
}

func TestParse_TimeModel(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if r.TimeModel.DBTimeSec != 29723.5 {
		t.Errorf("DB time: got %.1f, want 29723.5", r.TimeModel.DBTimeSec)
	}
	if r.TimeModel.CPUTimeSec != 8240.1 {
		t.Errorf("CPU time: got %.1f", r.TimeModel.CPUTimeSec)
	}
	if r.TimeModel.HardParseCount != 1834 {
		t.Errorf("hard parse: got %d", r.TimeModel.HardParseCount)
	}
	ratio := r.TimeModel.HardParseRatio()
	if ratio < 0.10 || ratio > 0.20 {
		t.Errorf("hard parse ratio: got %.3f, expected ~0.13", ratio)
	}
}

func TestParse_WaitEvents(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if len(r.Waits) < 4 {
		t.Fatalf("expected >=4 wait events, got %d", len(r.Waits))
	}
	// First should be lock_wait_acquire with 45.2% (assuming sorted by PctOfDBTime desc — or first row in section)
	found := false
	for _, w := range r.Waits {
		if w.Name == "lock_wait_acquire" {
			found = true
			if w.PctOfDBTime != 45.2 {
				t.Errorf("lock_wait_acquire pct: got %.1f, want 45.2", w.PctOfDBTime)
			}
			if w.Category != "Lock" {
				t.Errorf("lock_wait_acquire category: got %q", w.Category)
			}
		}
	}
	if !found {
		t.Error("lock_wait_acquire not found in wait events")
	}
}

func TestParse_TopSQLDedup(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if len(r.TopSQLs) < 3 {
		t.Fatalf("expected >=3 top SQLs, got %d", len(r.TopSQLs))
	}
	// SQL 3142685891 appears in both elapsed and buffer reads
	var top *TopSQLEntry
	for i := range r.TopSQLs {
		if r.TopSQLs[i].SQLID == "3142685891" {
			top = &r.TopSQLs[i]
			break
		}
	}
	if top == nil {
		t.Fatal("SQL_ID 3142685891 not found")
	}
	if len(top.Sources) < 2 {
		t.Errorf("3142685891 should be in 2+ sources (elapsed + io), got: %v", top.Sources)
	}
}

func TestParse_TopSQLPctCalculation(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if len(r.TopSQLs) == 0 {
		t.Skip("no top SQLs")
	}
	// SQL 1923014772: total 14250000ms = 14250s; DB Time = 29723.5s → 47.9%
	for _, s := range r.TopSQLs {
		if s.SQLID == "1923014772" {
			pct := s.PctOfDBTime(r.TimeModel.DBTimeSec)
			if pct < 45 || pct > 50 {
				t.Errorf("1923014772 pct of DB Time: got %.1f, want ~47.9", pct)
			}
		}
	}
}

func TestParse_BufferHitRatio(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if r.IO.BlocksRead != 2840000 {
		t.Errorf("blocks_read: got %d", r.IO.BlocksRead)
	}
	if r.IO.BlocksHit != 29400000 {
		t.Errorf("blocks_hit: got %d", r.IO.BlocksHit)
	}
	ratio := r.IO.BufferHitRatio()
	if ratio < 0.90 || ratio > 0.93 {
		t.Errorf("buffer hit ratio: got %.4f, expected ~0.912", ratio)
	}
}

func TestParse_Memory(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if r.Memory.TotalMemoryMB != 32768 {
		t.Errorf("total mem: got %d", r.Memory.TotalMemoryMB)
	}
	if r.Memory.UsedMemoryMB != 24500 {
		t.Errorf("used mem: got %d", r.Memory.UsedMemoryMB)
	}
}

func TestParse_Settings(t *testing.T) {
	r, _ := Parse(sampleWDRText)
	if r.Settings["autovacuum"] != "off" {
		t.Errorf("autovacuum setting: got %q, want 'off'", r.Settings["autovacuum"])
	}
	if !strings.Contains(r.Settings["shared_buffers"], "4096") {
		t.Errorf("shared_buffers: got %q", r.Settings["shared_buffers"])
	}
}

func TestParse_EmptyInput(t *testing.T) {
	_, err := Parse("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParse_GarbageInput(t *testing.T) {
	_, err := Parse("this is not a WDR report at all")
	if err == nil {
		t.Error("expected error for non-WDR text")
	}
}

func TestParseHelpers_ParseFloat(t *testing.T) {
	cases := map[string]float64{
		"12.5":      12.5,
		"12,500.5":  12500.5,
		"45.2%":     45.2,
		"100ms":     100,
		"1024 MB":   1024,
		"":          0,
		"not-a-num": 0,
	}
	for in, want := range cases {
		got := parseFloat(in)
		if got != want {
			t.Errorf("parseFloat(%q): got %.2f, want %.2f", in, got, want)
		}
	}
}

func TestParseHelpers_SplitTableRow(t *testing.T) {
	row := "lock_wait_acquire | Lock | 4521 | 13432000.0 | 2970.5 | 45.2"
	got := splitTableRow(row)
	if len(got) != 6 {
		t.Errorf("expected 6 fields, got %d: %v", len(got), got)
	}
	if got[0] != "lock_wait_acquire" {
		t.Errorf("first field: got %q", got[0])
	}
	if got[5] != "45.2" {
		t.Errorf("last field: got %q", got[5])
	}
}

// TestParse_OGHTMLTableFormat covers og 5.0.3's actual generate_wdr_report
// output: HTML tables where header row and value row are separate <tr>s
// with no "Field : Value" syntax. v1.1.49 fix — pre-fix the parser
// returned "not recognizable as a WDR" because the legacy regex required
// a colon/pipe separator on the same line as the field name.
const ogHTMLWDRSnippet = `<html><head><title>openGauss WDR Workload Diagnosis Report</title></head>
<body>
<h1>Workload Diagnosis Report</h1>
<table><tr>
<th>Report Type</th><th>Report Scope</th><th>Report Node</th></tr>
<tr><td>Summary + Detail</td><td>Cluster</td><td></td></tr>
</table>
<table><tr>
<th>Snapshot Id</th><th>Start Time</th><th>End Time</th></tr>
<tr><td>1</td><td>2026-05-18 11:32:11</td><td>2026-05-18 11:32:12</td></tr>
<tr><td>2</td><td>2026-05-18 11:35:34</td><td>2026-05-18 11:35:34</td></tr>
</table>
<table><tr>
<th>Host Node Name</th><th>CPUs</th><th>Cores</th><th>Sockets</th><th>Physical Memory</th><th>openGauss Version</th></tr>
<tr><td>og5</td><td>18</td><td>18</td><td>1</td><td>63 GB</td><td>(openGauss-lite 5.0.3 build 89d144c2) compiled at 2024-07-31</td></tr>
</table>
</body></html>`

func TestParse_OGHTMLTableFormat(t *testing.T) {
	r, err := Parse(ogHTMLWDRSnippet)
	if err != nil {
		t.Fatalf("parse error on og HTML format: %v", err)
	}
	if r.Header.InstanceHost != "og5" {
		t.Errorf("InstanceHost: got %q, want og5", r.Header.InstanceHost)
	}
	if !strings.Contains(r.Header.DBVersion, "5.0.3") {
		t.Errorf("DBVersion: got %q, want contains 5.0.3", r.Header.DBVersion)
	}
	if r.Header.SnapshotIDStart != 1 {
		t.Errorf("SnapshotIDStart: got %d, want 1", r.Header.SnapshotIDStart)
	}
	if r.Header.SnapshotIDEnd != 2 {
		t.Errorf("SnapshotIDEnd: got %d, want 2", r.Header.SnapshotIDEnd)
	}
	if r.Header.WindowStart.IsZero() {
		t.Errorf("WindowStart should be parsed, got zero")
	}
}

// TestParse_OGColumnAwareTopSQL: og 5.0.3's "SQL ordered by Elapsed Time"
// table has a 25-column layout. v1.1.50 fix — without column-aware parsing,
// every TotalTimeMS/AvgTimeMS came out 0 and Calls was wrong because the
// numeric heuristic put the first integer (Total Elapse us) into Calls and
// skipped everything afterward.
func TestParse_OGColumnAwareTopSQL(t *testing.T) {
	// Minimal og section: header row + 1 data row.
	ogSection := `<html><body>
<h3 class="wdr">-SQL ordered by Elapsed Time</h3>
<table><tr>
<th>Unique SQL Id</th><th>Node Name</th><th>User Name</th><th>Total Elapse Time(us)</th><th>Calls</th><th>Avg Elapse Time(us)</th><th>Min Elapse Time(us)</th><th>Max Elapse Time(us)</th><th>Returned Rows</th><th>Tuples Read</th><th>Tuples Affected</th><th>Logical Read</th><th>Physical Read</th><th>CPU Time(us)</th><th>Data IO Time(us)</th><th>Sort Count</th><th>Sort Time(us)</th><th>Sort Mem Used(KB)</th><th>Sort Spill Count</th><th>Sort Spill Size(KB)</th><th>Hash Count</th><th>Hash Time(us)</th><th>Hash Mem Used(KB)</th><th>Hash Spill Count</th><th>Hash Spill Size(KB)</th><th>SQL Text</th></tr>
<tr><td>4175761868</td><td>og5</td><td>gaussdb</td><td>1299322</td><td>1</td><td>1299322</td><td>1299322</td><td>1299322</td><td>0</td><td>5000232</td><td>5</td><td>42182</td><td>52</td><td>1298823</td><td>124432</td><td>0</td><td>0</td><td>0</td><td>0</td><td>0</td><td>0</td><td>0</td><td>0</td><td>0</td><td>0</td><td>CREATE INDEX IF NOT EXISTS idx_orders ON bench_orders(customer_id)</td></tr>
</table>
Workload Diagnosis Report openGauss-lite 5.0.3 og5
</body></html>`
	r, err := wdranalyze_Parse(t, ogSection)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(r.TopSQLs) != 1 {
		t.Fatalf("expected 1 TopSQL entry, got %d", len(r.TopSQLs))
	}
	e := r.TopSQLs[0]
	if e.SQLID != "4175761868" {
		t.Errorf("SQLID: got %q", e.SQLID)
	}
	if e.Calls != 1 {
		t.Errorf("Calls: got %d, want 1 (column-mapped, NOT 1299322 from heuristic)", e.Calls)
	}
	// 1299322 us = 1299.322 ms
	if e.TotalTimeMS < 1299 || e.TotalTimeMS > 1300 {
		t.Errorf("TotalTimeMS: got %.2f, want ~1299.32 (μs converted)", e.TotalTimeMS)
	}
	if e.AvgTimeMS < 1299 || e.AvgTimeMS > 1300 {
		t.Errorf("AvgTimeMS: got %.2f, want ~1299.32", e.AvgTimeMS)
	}
	if e.UserName != "gaussdb" {
		t.Errorf("UserName: got %q", e.UserName)
	}
	if !strings.Contains(e.QueryPrefix, "CREATE INDEX") {
		t.Errorf("QueryPrefix should have SQL text, got %q", e.QueryPrefix)
	}
}

// wdranalyze_Parse is a small wrapper so the test reads naturally; Parse
// already lives in the same package so we just call it directly.
func wdranalyze_Parse(_ *testing.T, raw string) (*WDRReport, error) {
	return Parse(raw)
}

// TestParse_WDRMarkerFallback: a malformed WDR (no recognizable header)
// but containing the marker string should still parse instead of hard-fail
// — let downstream parsers and LLM see whatever sections survived.
func TestParse_WDRMarkerFallback(t *testing.T) {
	garbageButMarked := `Some preamble text
Workload Diagnosis Report

== Top SQL by Elapsed Time ==
sql_id        | calls | total_time_ms
1111111111    | 100   | 50000.0
`
	r, err := Parse(garbageButMarked)
	if err != nil {
		t.Fatalf("WDR with marker but no header should parse, got: %v", err)
	}
	if r.Header.DBVersion == "" {
		t.Errorf("expected fallback DBVersion to be set")
	}
}

func TestParseHelpers_IsSQLID(t *testing.T) {
	cases := map[string]bool{
		"3142685891": true,
		"1234567":    false, // too short
		"abc123def":  true,  // hex
		"hello":      false, // non-hex
		"":           false,
	}
	for in, want := range cases {
		got := isSQLID(in)
		if got != want {
			t.Errorf("isSQLID(%q): got %v, want %v", in, got, want)
		}
	}
}
