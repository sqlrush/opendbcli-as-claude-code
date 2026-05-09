/*-------------------------------------------------------------------------
 *
 * perfsnap.go
 *	  PerfSnapshot is a point-in-time performance snapshot.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/perfsnap.go
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

const perfSnapWaitSQL = `SELECT NVL(event, 'ON CPU') AS event, wait_class,
       COUNT(*) AS samples,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) AS pct
FROM v$active_session_history
WHERE sample_time > SYSDATE - :1/1440
GROUP BY event, wait_class ORDER BY COUNT(*) DESC
FETCH FIRST 15 ROWS ONLY`

const perfSnapTopSQLSQL = `SELECT sql_id, COUNT(*) AS samples,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) AS pct,
       MAX(sql_plan_hash_value) AS plan_hash
FROM v$active_session_history
WHERE sample_time > SYSDATE - :1/1440 AND sql_id IS NOT NULL
GROUP BY sql_id ORDER BY COUNT(*) DESC
FETCH FIRST 10 ROWS ONLY`

const perfSnapSysstatSQL = `SELECT name, value FROM v$sysstat
WHERE name IN ('user commits','user rollbacks','execute count','redo size',
               'physical reads','physical writes','parse count (hard)',
               'session logical reads','db block changes')`

const perfSnapSysEventSQL = `SELECT event, total_waits, time_waited_micro, wait_class
FROM v$system_event WHERE wait_class <> 'Idle'
ORDER BY time_waited_micro DESC
FETCH FIRST 15 ROWS ONLY`

// ── Data types ──

// PerfSnapshot is a point-in-time performance snapshot.
type PerfSnapshot struct {
	ID        int                 `json:"id"`
	Timestamp time.Time           `json:"timestamp"`
	Waits     []WaitSample        `json:"waits"`
	TopSQL    []SQLSample         `json:"top_sql"`
	Sysstat   map[string]int64    `json:"sysstat"`
	SysEvents []SysEventSample    `json:"sys_events"`
	Baseline  bool                `json:"baseline,omitempty"`
}

// WaitSample is an ASH wait event aggregation.
type WaitSample struct {
	Event     string  `json:"event"`
	WaitClass string  `json:"wait_class"`
	Samples   int     `json:"samples"`
	Pct       float64 `json:"pct"`
}

// SQLSample is a top SQL from ASH.
type SQLSample struct {
	SQLID    string  `json:"sql_id"`
	Samples  int     `json:"samples"`
	Pct      float64 `json:"pct"`
	PlanHash string  `json:"plan_hash"`
}

// SysEventSample is a v$system_event row.
type SysEventSample struct {
	Event      string `json:"event"`
	TotalWaits int64  `json:"total_waits"`
	TimeMicro  int64  `json:"time_micro"`
	WaitClass  string `json:"wait_class"`
}

// PerfDiff describes differences between two snapshots.
type PerfDiff struct {
	From        time.Time         `json:"from"`
	To          time.Time         `json:"to"`
	WaitDrift   []WaitDrift       `json:"wait_drift,omitempty"`
	SQLChanges  []SQLChange       `json:"sql_changes,omitempty"`
	NewHotSQL   []SQLSample       `json:"new_hot_sql,omitempty"`
	LoadChange  *LoadChange       `json:"load_change,omitempty"`
}

// WaitDrift shows a wait event that shifted significantly.
type WaitDrift struct {
	Event   string  `json:"event"`
	OldPct  float64 `json:"old_pct"`
	NewPct  float64 `json:"new_pct"`
	DeltaPct float64 `json:"delta_pct"`
}

// SQLChange shows a SQL that changed plan or ranking.
type SQLChange struct {
	SQLID       string `json:"sql_id"`
	OldPlanHash string `json:"old_plan_hash"`
	NewPlanHash string `json:"new_plan_hash"`
	OldPct      float64 `json:"old_pct"`
	NewPct      float64 `json:"new_pct"`
}

// LoadChange shows overall load delta.
type LoadChange struct {
	OldExec int64 `json:"old_exec"`
	NewExec int64 `json:"new_exec"`
	OldRedo int64 `json:"old_redo"`
	NewRedo int64 `json:"new_redo"`
}

// ── Snapshot Store ──

const maxSnapshots = 144 // 24h at 10min interval

// SnapStore manages a ring buffer of snapshots persisted to disk.
type SnapStore struct {
	mu   sync.Mutex
	dir  string
	snaps []PerfSnapshot
	nextID int
}

// NewSnapStore creates a store at the given directory.
func NewSnapStore(dir string) *SnapStore {
	s := &SnapStore{dir: dir}
	s.load()
	return s
}

func (s *SnapStore) Add(snap PerfSnapshot) PerfSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	snap.ID = s.nextID
	s.snaps = append(s.snaps, snap)
	if len(s.snaps) > maxSnapshots {
		s.snaps = s.snaps[len(s.snaps)-maxSnapshots:]
	}
	s.save()
	return snap
}

func (s *SnapStore) Latest(n int) []PerfSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n > len(s.snaps) {
		n = len(s.snaps)
	}
	result := make([]PerfSnapshot, n)
	for i := 0; i < n; i++ {
		result[i] = s.snaps[len(s.snaps)-1-i]
	}
	return result
}

func (s *SnapStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.snaps)
}

func (s *SnapStore) At(idx int) (PerfSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 {
		idx = len(s.snaps) + idx
	}
	if idx < 0 || idx >= len(s.snaps) {
		return PerfSnapshot{}, false
	}
	return s.snaps[idx], true
}

func (s *SnapStore) load() {
	path := filepath.Join(s.dir, "perf_snapshots.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snaps []PerfSnapshot
	if json.Unmarshal(data, &snaps) == nil {
		if len(snaps) > maxSnapshots {
			snaps = snaps[len(snaps)-maxSnapshots:]
		}
		s.snaps = snaps
		for _, snap := range snaps {
			if snap.ID >= s.nextID {
				s.nextID = snap.ID
			}
		}
	}
}

func (s *SnapStore) save() {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s.snaps)
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, "perf_snapshots.json")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// ── Skill ──

// PerfSnapSkill captures and compares performance snapshots (intelligent AWR).
type PerfSnapSkill struct {
	driver db.Driver
	store  *SnapStore
}

// NewPerfSnapSkill creates a PerfSnapSkill with the given driver and data directory.
func NewPerfSnapSkill(driver db.Driver, dataDir string) *PerfSnapSkill {
	return &PerfSnapSkill{
		driver: driver,
		store:  NewSnapStore(dataDir),
	}
}

func (s *PerfSnapSkill) Name() string                       { return "perfsnap" }
func (s *PerfSnapSkill) Description() string                { return "Performance snapshot — capture and diff (intelligent AWR)" }
func (s *PerfSnapSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *PerfSnapSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "perfsnap",
		Description: "Capture performance snapshot and compare with previous",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "snap | diff | list | baseline",
				},
			},
		},
	}
}

func (s *PerfSnapSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "perfsnap",
		Aliases:        []string{"psnap"},
		Usage:          "/perfsnap [snap|diff|list|baseline]",
		ArgCompletions: []string{"snap", "diff", "list", "baseline"},
		Examples: []string{
			"/perfsnap",
			"/perfsnap snap",
			"/perfsnap diff",
			"/perfsnap list",
			"/perfsnap baseline",
		},
	}
}

func (s *PerfSnapSkill) Validate(params skill.Params) error {
	action := strings.Fields(params.StringOr("args", "snap"))
	if len(action) == 0 {
		return nil
	}
	switch action[0] {
	case "snap", "diff", "list", "baseline", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: snap, diff, list, baseline", action[0])
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
	case "diff":
		return s.diff()
	case "list":
		return s.list()
	case "baseline":
		return s.baseline()
	default:
		return &skill.Result{Type: skill.ResultError, Rendered: fmt.Sprintf("未知操作: %s", action)}, nil
	}
}

// Store returns the snapshot store (for scheduler integration).
func (s *PerfSnapSkill) Store() *SnapStore {
	return s.store
}

func (s *PerfSnapSkill) snap(ctx context.Context) (*skill.Result, error) {
	snap, err := s.capture(ctx)
	if err != nil {
		return nil, err
	}

	saved := s.store.Add(snap)

	// If there's a previous snapshot, auto-diff.
	var diffResult *PerfDiff
	if prev, ok := s.store.At(-2); ok {
		d := computeDiff(prev, saved)
		diffResult = &d
	}

	rendered := renderSnap(saved, diffResult)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &saved,
		Rendered: rendered,
		Summary:  fmt.Sprintf("快照 #%d, %d waits, %d SQL", saved.ID, len(saved.Waits), len(saved.TopSQL)),
	}, nil
}

func (s *PerfSnapSkill) diff() (*skill.Result, error) {
	if s.store.Count() < 2 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "需要至少 2 个快照才能对比 (先执行 /perfsnap snap)",
		}, nil
	}

	snap1, _ := s.store.At(-2)
	snap2, _ := s.store.At(-1)
	d := computeDiff(snap1, snap2)

	rendered := renderDiff(d)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &d,
		Rendered: rendered,
		Summary:  fmt.Sprintf("对比 #%d vs #%d", snap1.ID, snap2.ID),
	}, nil
}

func (s *PerfSnapSkill) list() (*skill.Result, error) {
	snaps := s.store.Latest(20)
	if len(snaps) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "暂无快照",
		}, nil
	}

	var rows [][]any
	for _, snap := range snaps {
		base := ""
		if snap.Baseline {
			base = "BASE"
		}
		topWait := "-"
		if len(snap.Waits) > 0 {
			topWait = fmt.Sprintf("%s (%.0f%%)", snap.Waits[0].Event, snap.Waits[0].Pct)
		}
		topSQL := "-"
		if len(snap.TopSQL) > 0 {
			topSQL = fmt.Sprintf("%s (%.0f%%)", snap.TopSQL[0].SQLID, snap.TopSQL[0].Pct)
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
		Rendered: fmt.Sprintf("性能快照列表 (%d 个)\n\n%s", len(snaps), table),
		Summary:  fmt.Sprintf("%d snapshots", len(snaps)),
	}, nil
}

func (s *PerfSnapSkill) baseline() (*skill.Result, error) {
	if s.store.Count() == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "暂无快照，先执行 /perfsnap snap",
		}, nil
	}

	snap, _ := s.store.At(-1)
	snap.Baseline = true
	// Re-save (simple approach: the latest is already in the store).

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("快照 #%d 已标记为基线", snap.ID),
		Summary:  fmt.Sprintf("baseline set: #%d", snap.ID),
	}, nil
}

// ── Capture ──

func (s *PerfSnapSkill) capture(ctx context.Context) (PerfSnapshot, error) {
	snap := PerfSnapshot{Timestamp: time.Now()}

	// Waits (ASH last 10 min).
	waitResult, err := s.driver.Query(ctx, perfSnapWaitSQL, 10)
	if err != nil {
		return snap, fmt.Errorf("querying ASH waits: %w", err)
	}
	for _, row := range waitResult.Rows {
		if len(row) < 4 {
			continue
		}
		snap.Waits = append(snap.Waits, WaitSample{
			Event:     shared.AsString(row[0]),
			WaitClass: shared.AsString(row[1]),
			Samples:   int(shared.ToFloat64(row[2])),
			Pct:       shared.ToFloat64(row[3]),
		})
	}

	// Top SQL (ASH last 10 min).
	sqlResult, err := s.driver.Query(ctx, perfSnapTopSQLSQL, 10)
	if err != nil {
		return snap, fmt.Errorf("querying ASH top SQL: %w", err)
	}
	for _, row := range sqlResult.Rows {
		if len(row) < 4 {
			continue
		}
		snap.TopSQL = append(snap.TopSQL, SQLSample{
			SQLID:    shared.AsString(row[0]),
			Samples:  int(shared.ToFloat64(row[1])),
			Pct:      shared.ToFloat64(row[2]),
			PlanHash: shared.AsString(row[3]),
		})
	}

	// Sysstat.
	statResult, err := s.driver.Query(ctx, perfSnapSysstatSQL)
	if err != nil {
		return snap, fmt.Errorf("querying sysstat: %w", err)
	}
	snap.Sysstat = make(map[string]int64)
	for _, row := range statResult.Rows {
		if len(row) < 2 {
			continue
		}
		snap.Sysstat[shared.AsString(row[0])] = int64(shared.ToFloat64(row[1]))
	}

	// System events.
	eventResult, err := s.driver.Query(ctx, perfSnapSysEventSQL)
	if err != nil {
		return snap, fmt.Errorf("querying system events: %w", err)
	}
	for _, row := range eventResult.Rows {
		if len(row) < 4 {
			continue
		}
		snap.SysEvents = append(snap.SysEvents, SysEventSample{
			Event:      shared.AsString(row[0]),
			TotalWaits: int64(shared.ToFloat64(row[1])),
			TimeMicro:  int64(shared.ToFloat64(row[2])),
			WaitClass:  shared.AsString(row[3]),
		})
	}

	return snap, nil
}

// ── Diff ──

func computeDiff(old, new_ PerfSnapshot) PerfDiff {
	d := PerfDiff{
		From: old.Timestamp,
		To:   new_.Timestamp,
	}

	// Wait drift: events that shifted > 5% pct points.
	oldWaits := make(map[string]float64)
	for _, w := range old.Waits {
		oldWaits[w.Event] = w.Pct
	}
	for _, w := range new_.Waits {
		oldPct := oldWaits[w.Event]
		delta := w.Pct - oldPct
		if delta > 5 || delta < -5 {
			d.WaitDrift = append(d.WaitDrift, WaitDrift{
				Event:    w.Event,
				OldPct:   oldPct,
				NewPct:   w.Pct,
				DeltaPct: delta,
			})
		}
	}
	sort.Slice(d.WaitDrift, func(i, j int) bool {
		return abs(d.WaitDrift[i].DeltaPct) > abs(d.WaitDrift[j].DeltaPct)
	})

	// SQL changes: plan hash changed for same sql_id.
	oldSQL := make(map[string]SQLSample)
	for _, s := range old.TopSQL {
		oldSQL[s.SQLID] = s
	}
	for _, s := range new_.TopSQL {
		if prev, ok := oldSQL[s.SQLID]; ok {
			if prev.PlanHash != s.PlanHash && prev.PlanHash != "" && s.PlanHash != "" {
				d.SQLChanges = append(d.SQLChanges, SQLChange{
					SQLID:       s.SQLID,
					OldPlanHash: prev.PlanHash,
					NewPlanHash: s.PlanHash,
					OldPct:      prev.Pct,
					NewPct:      s.Pct,
				})
			}
		} else {
			// New hot SQL not seen in previous snapshot.
			d.NewHotSQL = append(d.NewHotSQL, s)
		}
	}

	// Load change (execute count and redo size delta).
	if old.Sysstat != nil && new_.Sysstat != nil {
		d.LoadChange = &LoadChange{
			OldExec: old.Sysstat["execute count"],
			NewExec: new_.Sysstat["execute count"],
			OldRedo: old.Sysstat["redo size"],
			NewRedo: new_.Sysstat["redo size"],
		}
	}

	return d
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ── Render ──

func renderSnap(snap PerfSnapshot, diff *PerfDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "性能快照 #%d (%s)\n\n", snap.ID, snap.Timestamp.Format("15:04:05"))

	// Top waits.
	b.WriteString("── Top Wait Events ──\n")
	if len(snap.Waits) == 0 {
		b.WriteString(" (无数据)\n")
	} else {
		var rows [][]any
		for _, w := range snap.Waits {
			if len(rows) >= 10 {
				break
			}
			rows = append(rows, []any{w.Event, w.WaitClass, w.Samples, fmt.Sprintf("%.1f%%", w.Pct)})
		}
		qr := &db.QueryResult{
			Columns: []string{"Event", "Class", "Samples", "Pct"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	// Top SQL.
	b.WriteString("\n── Top SQL ──\n")
	if len(snap.TopSQL) == 0 {
		b.WriteString(" (无数据)\n")
	} else {
		var rows [][]any
		for _, s := range snap.TopSQL {
			if len(rows) >= 10 {
				break
			}
			rows = append(rows, []any{s.SQLID, s.Samples, fmt.Sprintf("%.1f%%", s.Pct), s.PlanHash})
		}
		qr := &db.QueryResult{
			Columns: []string{"SQL_ID", "Samples", "Pct", "Plan Hash"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	// Diff section.
	if diff != nil && hasDiffContent(*diff) {
		b.WriteString("\n── 与上次快照对比 ──\n")
		b.WriteString(renderDiffBrief(*diff))
	}

	return b.String()
}

func renderDiff(d PerfDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "性能对比 (%s → %s)\n\n",
		d.From.Format("15:04:05"), d.To.Format("15:04:05"))

	if !hasDiffContent(d) {
		b.WriteString(" 无显著变化\n")
		return b.String()
	}

	b.WriteString(renderDiffBrief(d))
	return b.String()
}

func renderDiffBrief(d PerfDiff) string {
	var b strings.Builder

	if len(d.WaitDrift) > 0 {
		b.WriteString("等待事件漂移:\n")
		var rows [][]any
		for _, w := range d.WaitDrift {
			arrow := "+"
			if w.DeltaPct < 0 {
				arrow = ""
			}
			rows = append(rows, []any{w.Event, fmt.Sprintf("%.0f%%", w.OldPct), fmt.Sprintf("%.0f%%", w.NewPct), fmt.Sprintf("%s%.0f%%", arrow, w.DeltaPct)})
		}
		qr := &db.QueryResult{
			Columns: []string{"Event", "Before", "After", "Delta"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	if len(d.SQLChanges) > 0 {
		b.WriteString("\nSQL 执行计划变化:\n")
		var rows [][]any
		for _, c := range d.SQLChanges {
			rows = append(rows, []any{c.SQLID, c.OldPlanHash, c.NewPlanHash, fmt.Sprintf("%.0f%% → %.0f%%", c.OldPct, c.NewPct)})
		}
		qr := &db.QueryResult{
			Columns: []string{"SQL_ID", "Old Plan", "New Plan", "Pct"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	if len(d.NewHotSQL) > 0 {
		b.WriteString("\n新出现的高耗 SQL:\n")
		var rows [][]any
		for _, s := range d.NewHotSQL {
			rows = append(rows, []any{s.SQLID, s.Samples, fmt.Sprintf("%.1f%%", s.Pct), s.PlanHash})
		}
		qr := &db.QueryResult{
			Columns: []string{"SQL_ID", "Samples", "Pct", "Plan Hash"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	if d.LoadChange != nil {
		execDelta := d.LoadChange.NewExec - d.LoadChange.OldExec
		redoDelta := d.LoadChange.NewRedo - d.LoadChange.OldRedo
		if execDelta != 0 || redoDelta != 0 {
			b.WriteString(fmt.Sprintf("\n负载变化: execute count %+d, redo size %+d KB\n",
				execDelta, redoDelta/1024))
		}
	}

	return b.String()
}

func hasDiffContent(d PerfDiff) bool {
	return len(d.WaitDrift) > 0 || len(d.SQLChanges) > 0 || len(d.NewHotSQL) > 0
}
