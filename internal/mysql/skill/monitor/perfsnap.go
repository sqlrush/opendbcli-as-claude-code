/*-------------------------------------------------------------------------
 *
 * perfsnap.go
 *	  MySQLPerfSnapshot is a point-in-time performance snapshot for
 *	  MySQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/perfsnap.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
)

// ── SQL ──

const perfSnapGlobalStatusSQL = `SELECT VARIABLE_NAME, VARIABLE_VALUE
FROM performance_schema.global_status
WHERE VARIABLE_NAME IN (
  'Queries','Com_commit','Com_rollback',
  'Innodb_buffer_pool_read_requests','Innodb_buffer_pool_reads',
  'Threads_connected','Threads_running',
  'Innodb_os_log_written','Innodb_row_lock_waits',
  'Innodb_row_lock_time','Slow_queries',
  'Bytes_sent','Bytes_received'
)`

const perfSnapTopSQLSQL = `SELECT
  LEFT(DIGEST_TEXT, 120) AS sql_text,
  SCHEMA_NAME AS db_name,
  COUNT_STAR AS exec_count,
  ROUND(SUM_TIMER_WAIT / 1e12, 2) AS total_sec,
  ROUND(SUM_TIMER_WAIT / COUNT_STAR / 1e12, 4) AS avg_sec,
  SUM_ROWS_EXAMINED AS rows_examined
FROM performance_schema.events_statements_summary_by_digest
WHERE COUNT_STAR > 0
  AND LAST_SEEN > NOW() - INTERVAL 10 MINUTE
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 10`

const perfSnapWaitsSQL = `SELECT
  EVENT_NAME,
  COUNT_STAR,
  ROUND(SUM_TIMER_WAIT / 1e12, 2) AS total_sec
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE EVENT_NAME NOT LIKE 'wait/idle/%'
  AND COUNT_STAR > 0
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 15`

// ── Data types ──

// MySQLPerfSnapshot is a point-in-time performance snapshot for MySQL.
type MySQLPerfSnapshot struct {
	ID        int                `json:"id"`
	Timestamp time.Time          `json:"timestamp"`
	Status    map[string]int64   `json:"status"`
	TopSQL    []MySQLSQLSample   `json:"top_sql"`
	Waits     []MySQLWaitSample  `json:"waits"`
	Baseline  bool               `json:"baseline,omitempty"`
}

// MySQLSQLSample is a top SQL entry from performance_schema.
type MySQLSQLSample struct {
	SQLText      string  `json:"sql_text"`
	DBName       string  `json:"db_name"`
	ExecCount    int64   `json:"exec_count"`
	TotalSec     float64 `json:"total_sec"`
	AvgSec       float64 `json:"avg_sec"`
	RowsExamined int64   `json:"rows_examined"`
}

// MySQLWaitSample is a wait event entry from performance_schema.
type MySQLWaitSample struct {
	Event    string  `json:"event"`
	Count    int64   `json:"count"`
	TotalSec float64 `json:"total_sec"`
}

// MySQLPerfDiff describes differences between two snapshots.
type MySQLPerfDiff struct {
	From       time.Time          `json:"from"`
	To         time.Time          `json:"to"`
	StatusDiff map[string]int64   `json:"status_diff,omitempty"`
	WaitDrift  []MySQLWaitDrift   `json:"wait_drift,omitempty"`
	NewHotSQL  []MySQLSQLSample   `json:"new_hot_sql,omitempty"`
}

// MySQLWaitDrift shows a wait event that shifted significantly.
type MySQLWaitDrift struct {
	Event    string  `json:"event"`
	OldCount int64   `json:"old_count"`
	NewCount int64   `json:"new_count"`
	Delta    int64   `json:"delta"`
}

// ── Snapshot Store ──

const maxMySQLSnapshots = 144

// MySQLSnapStore manages a ring buffer of MySQL snapshots persisted to disk.
type MySQLSnapStore struct {
	mu     sync.Mutex
	dir    string
	snaps  []MySQLPerfSnapshot
	nextID int
}

// NewMySQLSnapStore creates a store at the given directory.
func NewMySQLSnapStore(dir string) *MySQLSnapStore {
	s := &MySQLSnapStore{dir: dir}
	s.load()
	return s
}

func (s *MySQLSnapStore) Add(snap MySQLPerfSnapshot) MySQLPerfSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	snap.ID = s.nextID
	s.snaps = append(s.snaps, snap)
	if len(s.snaps) > maxMySQLSnapshots {
		s.snaps = s.snaps[len(s.snaps)-maxMySQLSnapshots:]
	}
	s.save()
	return snap
}

func (s *MySQLSnapStore) Latest(n int) []MySQLPerfSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n > len(s.snaps) {
		n = len(s.snaps)
	}
	result := make([]MySQLPerfSnapshot, n)
	for i := 0; i < n; i++ {
		result[i] = s.snaps[len(s.snaps)-1-i]
	}
	return result
}

func (s *MySQLSnapStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.snaps)
}

func (s *MySQLSnapStore) At(idx int) (MySQLPerfSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 {
		idx = len(s.snaps) + idx
	}
	if idx < 0 || idx >= len(s.snaps) {
		return MySQLPerfSnapshot{}, false
	}
	return s.snaps[idx], true
}

func (s *MySQLSnapStore) load() {
	path := filepath.Join(s.dir, "mysql_perf_snapshots.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snaps []MySQLPerfSnapshot
	if json.Unmarshal(data, &snaps) == nil {
		if len(snaps) > maxMySQLSnapshots {
			snaps = snaps[len(snaps)-maxMySQLSnapshots:]
		}
		s.snaps = snaps
		for _, snap := range snaps {
			if snap.ID >= s.nextID {
				s.nextID = snap.ID
			}
		}
	}
}

func (s *MySQLSnapStore) save() {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s.snaps)
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, "mysql_perf_snapshots.json")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// ── Skill ──

// PerfSnapSkill captures and compares MySQL performance snapshots.
type PerfSnapSkill struct {
	driver db.Driver
	store  *MySQLSnapStore
}

// NewPerfSnapSkill creates a PerfSnapSkill with the given driver and data directory.
func NewPerfSnapSkill(driver db.Driver, dataDir string) *PerfSnapSkill {
	return &PerfSnapSkill{
		driver: driver,
		store:  NewMySQLSnapStore(dataDir),
	}
}

func (s *PerfSnapSkill) Name() string                       { return "perfsnap" }
func (s *PerfSnapSkill) Description() string                { return "Performance snapshot — capture and diff" }
func (s *PerfSnapSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *PerfSnapSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "perfsnap",
		Description: "Capture MySQL performance snapshot and compare with previous",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "snap | compare | list",
				},
			},
		},
	}
}

func (s *PerfSnapSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "perfsnap",
		Aliases:        []string{"psnap"},
		Usage:          "/perfsnap [snap|compare|list]",
		ArgCompletions: []string{"snap", "compare", "list"},
		Examples: []string{
			"/perfsnap",
			"/perfsnap snap",
			"/perfsnap compare",
			"/perfsnap list",
		},
	}
}

func (s *PerfSnapSkill) Validate(params skill.Params) error {
	action := strings.Fields(params.StringOr("args", "snap"))
	if len(action) == 0 {
		return nil
	}
	switch action[0] {
	case "snap", "compare", "list", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: snap, compare, list", action[0])
	}
}

func (s *PerfSnapSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.Fields(params.StringOr("args", "snap"))
	action := "snap"
	if len(args) > 0 {
		action = args[0]
	}

	switch action {
	case "snap", "":
		return s.snap(ctx)
	case "compare":
		return s.compare()
	case "list":
		return s.list()
	default:
		return &skill.Result{Type: skill.ResultError, Rendered: fmt.Sprintf("unknown action: %s", action)}, nil
	}
}

// Store returns the snapshot store (for scheduler integration).
func (s *PerfSnapSkill) Store() *MySQLSnapStore {
	return s.store
}

func (s *PerfSnapSkill) snap(ctx context.Context) (*skill.Result, error) {
	snap, err := s.capture(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	saved := s.store.Add(snap)

	var diffResult *MySQLPerfDiff
	if prev, ok := s.store.At(-2); ok {
		d := computeMySQLDiff(prev, saved)
		diffResult = &d
	}

	rendered := renderMySQLSnap(saved, diffResult)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &saved,
		Rendered: rendered,
		Summary:  fmt.Sprintf("Snapshot #%d, %d waits, %d SQL", saved.ID, len(saved.Waits), len(saved.TopSQL)),
	}, nil
}

func (s *PerfSnapSkill) compare() (*skill.Result, error) {
	if s.store.Count() < 2 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Need at least 2 snapshots to compare (run /perfsnap snap first)",
		}, nil
	}

	snap1, _ := s.store.At(-2)
	snap2, _ := s.store.At(-1)
	d := computeMySQLDiff(snap1, snap2)

	rendered := renderMySQLDiff(d)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &d,
		Rendered: rendered,
		Summary:  fmt.Sprintf("Compare #%d vs #%d", snap1.ID, snap2.ID),
	}, nil
}

func (s *PerfSnapSkill) list() (*skill.Result, error) {
	snaps := s.store.Latest(20)
	if len(snaps) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No snapshots yet",
		}, nil
	}

	var rows [][]any
	for _, snap := range snaps {
		topWait := "-"
		if len(snap.Waits) > 0 {
			topWait = fmt.Sprintf("%s (%d)", snap.Waits[0].Event, snap.Waits[0].Count)
		}
		topSQL := "-"
		if len(snap.TopSQL) > 0 {
			topSQL = fmt.Sprintf("%.2fs %s", snap.TopSQL[0].TotalSec, truncSQLForList(snap.TopSQL[0].SQLText, 30))
		}
		base := ""
		if snap.Baseline {
			base = "BASE"
		}
		rows = append(rows, []any{
			snap.ID,
			snap.Timestamp.Format("01-02 15:04:05"),
			topWait,
			topSQL,
			base,
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"#", "Time", "Top Wait", "Top SQL", "Base"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 20, TermWidth: 120})

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Performance Snapshots (%d)\n\n%s", len(snaps), table),
		Summary:  fmt.Sprintf("%d snapshots", len(snaps)),
	}, nil
}

func truncSQLForList(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-2] + ".."
}

// ── Capture ──

func (s *PerfSnapSkill) capture(ctx context.Context) (MySQLPerfSnapshot, error) {
	snap := MySQLPerfSnapshot{Timestamp: time.Now()}

	// Global status.
	statusResult, err := s.driver.Query(ctx, perfSnapGlobalStatusSQL)
	if err != nil {
		return snap, fmt.Errorf("querying global status: %w", err)
	}
	snap.Status = make(map[string]int64)
	for _, row := range statusResult.Rows {
		if len(row) < 2 {
			continue
		}
		snap.Status[shared.AsString(row[0])] = int64(shared.ToFloat64(row[1]))
	}

	// Top SQL.
	sqlResult, err := s.driver.Query(ctx, perfSnapTopSQLSQL)
	if err != nil {
		return snap, fmt.Errorf("querying top SQL: %w", err)
	}
	for _, row := range sqlResult.Rows {
		if len(row) < 6 {
			continue
		}
		snap.TopSQL = append(snap.TopSQL, MySQLSQLSample{
			SQLText:      shared.AsString(row[0]),
			DBName:       shared.AsString(row[1]),
			ExecCount:    int64(shared.ToFloat64(row[2])),
			TotalSec:     shared.ToFloat64(row[3]),
			AvgSec:       shared.ToFloat64(row[4]),
			RowsExamined: int64(shared.ToFloat64(row[5])),
		})
	}

	// Wait events.
	waitResult, err := s.driver.Query(ctx, perfSnapWaitsSQL)
	if err != nil {
		return snap, fmt.Errorf("querying waits: %w", err)
	}
	for _, row := range waitResult.Rows {
		if len(row) < 3 {
			continue
		}
		snap.Waits = append(snap.Waits, MySQLWaitSample{
			Event:    shared.AsString(row[0]),
			Count:    int64(shared.ToFloat64(row[1])),
			TotalSec: shared.ToFloat64(row[2]),
		})
	}

	return snap, nil
}

// ── Diff ──

func computeMySQLDiff(old, new_ MySQLPerfSnapshot) MySQLPerfDiff {
	d := MySQLPerfDiff{
		From: old.Timestamp,
		To:   new_.Timestamp,
	}

	// Status delta for key metrics.
	d.StatusDiff = make(map[string]int64)
	keyMetrics := []string{
		"Queries", "Com_commit", "Com_rollback",
		"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads",
		"Threads_connected", "Threads_running",
		"Innodb_os_log_written", "Slow_queries",
	}
	for _, k := range keyMetrics {
		oldVal := old.Status[k]
		newVal := new_.Status[k]
		delta := newVal - oldVal
		if delta != 0 {
			d.StatusDiff[k] = delta
		}
	}

	// Wait drift: events with significant count change.
	oldWaits := make(map[string]int64)
	for _, w := range old.Waits {
		oldWaits[w.Event] = w.Count
	}
	for _, w := range new_.Waits {
		oldCount := oldWaits[w.Event]
		delta := w.Count - oldCount
		if delta > 100 || delta < -100 {
			d.WaitDrift = append(d.WaitDrift, MySQLWaitDrift{
				Event:    w.Event,
				OldCount: oldCount,
				NewCount: w.Count,
				Delta:    delta,
			})
		}
	}
	sort.Slice(d.WaitDrift, func(i, j int) bool {
		return absInt64(d.WaitDrift[i].Delta) > absInt64(d.WaitDrift[j].Delta)
	})

	// New hot SQL: SQL in new snapshot but not in old.
	oldSQL := make(map[string]bool)
	for _, s := range old.TopSQL {
		oldSQL[s.SQLText] = true
	}
	for _, s := range new_.TopSQL {
		if !oldSQL[s.SQLText] {
			d.NewHotSQL = append(d.NewHotSQL, s)
		}
	}

	return d
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ── Render ──

func renderMySQLSnap(snap MySQLPerfSnapshot, diff *MySQLPerfDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Performance Snapshot #%d (%s)\n\n", snap.ID, snap.Timestamp.Format("15:04:05"))

	// Key status metrics.
	b.WriteString("-- Key Metrics --\n")
	metricNames := []string{"Queries", "Com_commit", "Com_rollback",
		"Threads_connected", "Threads_running", "Innodb_os_log_written",
		"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads", "Slow_queries"}
	var metricRows [][]any
	for _, k := range metricNames {
		if v, ok := snap.Status[k]; ok {
			metricRows = append(metricRows, []any{k, v})
		}
	}
	if len(metricRows) > 0 {
		qr := &db.QueryResult{
			Columns: []string{"Metric", "Value"},
			Rows:    metricRows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 20, TermWidth: 100}))
	}

	// Top waits.
	b.WriteString("\n-- Top Wait Events --\n")
	if len(snap.Waits) == 0 {
		b.WriteString(" (no data)\n")
	} else {
		var rows [][]any
		for i, w := range snap.Waits {
			if i >= 10 {
				break
			}
			rows = append(rows, []any{w.Event, w.Count, fmt.Sprintf("%.2fs", w.TotalSec)})
		}
		qr := &db.QueryResult{
			Columns: []string{"Event", "Count", "Total Time"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	// Top SQL.
	b.WriteString("\n-- Top SQL --\n")
	if len(snap.TopSQL) == 0 {
		b.WriteString(" (no data)\n")
	} else {
		var rows [][]any
		for i, s := range snap.TopSQL {
			if i >= 10 {
				break
			}
			rows = append(rows, []any{
				truncSQLForList(s.SQLText, 60),
				s.ExecCount,
				fmt.Sprintf("%.2fs", s.TotalSec),
				fmt.Sprintf("%.4fs", s.AvgSec),
			})
		}
		qr := &db.QueryResult{
			Columns: []string{"SQL", "Exec", "Total", "Avg"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	// Diff section.
	if diff != nil && hasMySQLDiffContent(*diff) {
		b.WriteString("\n-- Diff vs Previous --\n")
		b.WriteString(renderMySQLDiffBrief(*diff))
	}

	return b.String()
}

func renderMySQLDiff(d MySQLPerfDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Performance Compare (%s -> %s)\n\n",
		d.From.Format("15:04:05"), d.To.Format("15:04:05"))

	if !hasMySQLDiffContent(d) {
		b.WriteString(" No significant changes\n")
		return b.String()
	}

	b.WriteString(renderMySQLDiffBrief(d))
	return b.String()
}

func renderMySQLDiffBrief(d MySQLPerfDiff) string {
	var b strings.Builder

	if len(d.StatusDiff) > 0 {
		b.WriteString("Status Changes:\n")
		var rows [][]any
		for k, v := range d.StatusDiff {
			sign := "+"
			if v < 0 {
				sign = ""
			}
			rows = append(rows, []any{k, fmt.Sprintf("%s%d", sign, v)})
		}
		qr := &db.QueryResult{
			Columns: []string{"Metric", "Delta"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 20, TermWidth: 100}))
	}

	if len(d.WaitDrift) > 0 {
		b.WriteString("\nWait Event Changes:\n")
		var rows [][]any
		for _, w := range d.WaitDrift {
			sign := "+"
			if w.Delta < 0 {
				sign = ""
			}
			rows = append(rows, []any{w.Event, w.OldCount, w.NewCount, fmt.Sprintf("%s%d", sign, w.Delta)})
		}
		qr := &db.QueryResult{
			Columns: []string{"Event", "Before", "After", "Delta"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	if len(d.NewHotSQL) > 0 {
		b.WriteString("\nNew Hot SQL:\n")
		var rows [][]any
		for _, s := range d.NewHotSQL {
			rows = append(rows, []any{
				truncSQLForList(s.SQLText, 60),
				s.ExecCount,
				fmt.Sprintf("%.2fs", s.TotalSec),
			})
		}
		qr := &db.QueryResult{
			Columns: []string{"SQL", "Exec", "Total"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	return b.String()
}

func hasMySQLDiffContent(d MySQLPerfDiff) bool {
	return len(d.StatusDiff) > 0 || len(d.WaitDrift) > 0 || len(d.NewHotSQL) > 0
}
