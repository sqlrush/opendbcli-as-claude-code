/*-------------------------------------------------------------------------
 *
 * perfsnap.go
 *	  PGPerfSnapshot is a point-in-time PostgreSQL performance snapshot.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/perfsnap.go
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
)

// ── SQL ──

const pgPerfSnapDBStatSQL = `SELECT
  datname,
  xact_commit, xact_rollback,
  blks_read, blks_hit,
  tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted,
  deadlocks, temp_bytes, temp_files
FROM pg_stat_database
WHERE datname = current_database()`

const pgPerfSnapBGWriterSQL = `SELECT
  checkpoints_timed, checkpoints_req,
  buffers_checkpoint, buffers_clean, buffers_backend,
  maxwritten_clean, buffers_alloc
FROM pg_stat_bgwriter`

const pgPerfSnapTopSQLSQL = `SELECT
  queryid::text, calls, total_exec_time, mean_exec_time,
  rows, shared_blks_hit, shared_blks_read,
  LEFT(query, 120) AS query_text
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 10`

// ── Data types ──

// PGPerfSnapshot is a point-in-time PostgreSQL performance snapshot.
type PGPerfSnapshot struct {
	ID        int               `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	DBStat    *PGDBStat         `json:"db_stat"`
	BGWriter  *PGBGWriterStat   `json:"bgwriter"`
	TopSQL    []PGSQLStat       `json:"top_sql,omitempty"`
	Baseline  bool              `json:"baseline,omitempty"`
}

// PGDBStat holds pg_stat_database metrics.
type PGDBStat struct {
	DBName      string `json:"db_name"`
	Commits     int64  `json:"commits"`
	Rollbacks   int64  `json:"rollbacks"`
	BlksRead    int64  `json:"blks_read"`
	BlksHit     int64  `json:"blks_hit"`
	TupReturned int64  `json:"tup_returned"`
	TupFetched  int64  `json:"tup_fetched"`
	TupInserted int64  `json:"tup_inserted"`
	TupUpdated  int64  `json:"tup_updated"`
	TupDeleted  int64  `json:"tup_deleted"`
	Deadlocks   int64  `json:"deadlocks"`
	TempBytes   int64  `json:"temp_bytes"`
	TempFiles   int64  `json:"temp_files"`
}

// PGBGWriterStat holds pg_stat_bgwriter metrics.
type PGBGWriterStat struct {
	CheckpointsTimed int64 `json:"checkpoints_timed"`
	CheckpointsReq   int64 `json:"checkpoints_req"`
	BufCheckpoint    int64 `json:"buf_checkpoint"`
	BufClean         int64 `json:"buf_clean"`
	BufBackend       int64 `json:"buf_backend"`
	MaxWrittenClean  int64 `json:"max_written_clean"`
	BufAlloc         int64 `json:"buf_alloc"`
}

// PGSQLStat holds a top SQL entry from pg_stat_statements.
type PGSQLStat struct {
	QueryID      string  `json:"query_id"`
	Calls        int64   `json:"calls"`
	TotalTimeMs  float64 `json:"total_time_ms"`
	MeanTimeMs   float64 `json:"mean_time_ms"`
	Rows         int64   `json:"rows"`
	SharedHit    int64   `json:"shared_hit"`
	SharedRead   int64   `json:"shared_read"`
	QueryText    string  `json:"query_text"`
}

// PGPerfDiff describes differences between two snapshots.
type PGPerfDiff struct {
	From            time.Time      `json:"from"`
	To              time.Time      `json:"to"`
	TPSDelta        float64        `json:"tps_delta"`
	CacheHitOld     float64        `json:"cache_hit_old"`
	CacheHitNew     float64        `json:"cache_hit_new"`
	TempBytesOld    int64          `json:"temp_bytes_old"`
	TempBytesNew    int64          `json:"temp_bytes_new"`
	DeadlocksOld    int64          `json:"deadlocks_old"`
	DeadlocksNew    int64          `json:"deadlocks_new"`
	CheckpointsOld  int64          `json:"checkpoints_old"`
	CheckpointsNew  int64          `json:"checkpoints_new"`
	NewHotSQL       []PGSQLStat    `json:"new_hot_sql,omitempty"`
	SQLTimeChanges  []pgSQLChange  `json:"sql_time_changes,omitempty"`
}

type pgSQLChange struct {
	QueryID     string  `json:"query_id"`
	OldTotalMs  float64 `json:"old_total_ms"`
	NewTotalMs  float64 `json:"new_total_ms"`
	DeltaMs     float64 `json:"delta_ms"`
}

// ── Snapshot Store ──

const pgMaxSnapshots = 144

// PGSnapStore manages a ring buffer of PG snapshots persisted to disk.
type PGSnapStore struct {
	mu     sync.Mutex
	dir    string
	snaps  []PGPerfSnapshot
	nextID int
}

// NewPGSnapStore creates a store at the given directory.
func NewPGSnapStore(dir string) *PGSnapStore {
	s := &PGSnapStore{dir: dir}
	s.load()
	return s
}

func (s *PGSnapStore) Add(snap PGPerfSnapshot) PGPerfSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	snap.ID = s.nextID
	s.snaps = append(s.snaps, snap)
	if len(s.snaps) > pgMaxSnapshots {
		s.snaps = s.snaps[len(s.snaps)-pgMaxSnapshots:]
	}
	s.save()
	return snap
}

func (s *PGSnapStore) Latest(n int) []PGPerfSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n > len(s.snaps) {
		n = len(s.snaps)
	}
	result := make([]PGPerfSnapshot, n)
	for i := 0; i < n; i++ {
		result[i] = s.snaps[len(s.snaps)-1-i]
	}
	return result
}

func (s *PGSnapStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.snaps)
}

func (s *PGSnapStore) At(idx int) (PGPerfSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 {
		idx = len(s.snaps) + idx
	}
	if idx < 0 || idx >= len(s.snaps) {
		return PGPerfSnapshot{}, false
	}
	return s.snaps[idx], true
}

func (s *PGSnapStore) load() {
	path := filepath.Join(s.dir, "pg_perf_snapshots.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snaps []PGPerfSnapshot
	if json.Unmarshal(data, &snaps) == nil {
		if len(snaps) > pgMaxSnapshots {
			snaps = snaps[len(snaps)-pgMaxSnapshots:]
		}
		s.snaps = snaps
		for _, snap := range snaps {
			if snap.ID >= s.nextID {
				s.nextID = snap.ID
			}
		}
	}
}

func (s *PGSnapStore) save() {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s.snaps)
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, "pg_perf_snapshots.json")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// ── Skill ──

// PGPerfSnapSkill captures and compares PostgreSQL performance snapshots.
type PGPerfSnapSkill struct {
	driver db.Driver
	store  *PGSnapStore
}

// NewPGPerfSnapSkill creates a PGPerfSnapSkill with the given driver and data directory.
func NewPGPerfSnapSkill(driver db.Driver, dataDir string) *PGPerfSnapSkill {
	return &PGPerfSnapSkill{
		driver: driver,
		store:  NewPGSnapStore(dataDir),
	}
}

func (s *PGPerfSnapSkill) Name() string                       { return "perfsnap" }
func (s *PGPerfSnapSkill) Description() string                { return "Performance snapshot -- capture and diff (PostgreSQL)" }
func (s *PGPerfSnapSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *PGPerfSnapSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "perfsnap",
		Description: "Capture PostgreSQL performance snapshot and compare with previous",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "snap | compare | list | baseline",
				},
			},
		},
	}
}

func (s *PGPerfSnapSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "perfsnap",
		Aliases:        []string{"psnap"},
		Usage:          "/perfsnap [snap|compare|list|baseline]",
		ArgCompletions: []string{"snap", "compare", "list", "baseline"},
		Examples: []string{
			"/perfsnap",
			"/perfsnap snap",
			"/perfsnap compare",
			"/perfsnap list",
			"/perfsnap baseline",
		},
	}
}

func (s *PGPerfSnapSkill) Validate(params skill.Params) error {
	action := strings.Fields(params.StringOr("args", "snap"))
	if len(action) == 0 {
		return nil
	}
	switch action[0] {
	case "snap", "compare", "list", "baseline", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: snap, compare, list, baseline", action[0])
	}
}

func (s *PGPerfSnapSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
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
	case "baseline":
		return s.baseline()
	default:
		return &skill.Result{Type: skill.ResultError, Rendered: fmt.Sprintf("unknown action: %s", action)}, nil
	}
}

func (s *PGPerfSnapSkill) snap(ctx context.Context) (*skill.Result, error) {
	snap, err := s.capture(ctx)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	saved := s.store.Add(snap)

	var diffResult *PGPerfDiff
	if prev, ok := s.store.At(-2); ok {
		d := pgComputeDiff(prev, saved)
		diffResult = &d
	}

	rendered := pgRenderSnap(saved, diffResult)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &saved,
		Rendered: rendered,
		Summary:  fmt.Sprintf("Snapshot #%d captured", saved.ID),
	}, nil
}

func (s *PGPerfSnapSkill) compare() (*skill.Result, error) {
	if s.store.Count() < 2 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "Need at least 2 snapshots to compare (run /perfsnap snap first)",
		}, nil
	}

	snap1, _ := s.store.At(-2)
	snap2, _ := s.store.At(-1)
	d := pgComputeDiff(snap1, snap2)

	rendered := pgRenderDiff(d)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &d,
		Rendered: rendered,
		Summary:  fmt.Sprintf("Compare #%d vs #%d", snap1.ID, snap2.ID),
	}, nil
}

func (s *PGPerfSnapSkill) list() (*skill.Result, error) {
	snaps := s.store.Latest(20)
	if len(snaps) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No snapshots yet",
		}, nil
	}

	var rows [][]any
	for _, snap := range snaps {
		base := ""
		if snap.Baseline {
			base = "BASE"
		}
		tps := "-"
		if snap.DBStat != nil {
			tps = fmt.Sprintf("%d/%d", snap.DBStat.Commits, snap.DBStat.Rollbacks)
		}
		topSQL := "-"
		if len(snap.TopSQL) > 0 {
			topSQL = fmt.Sprintf("%s (%.0fms)", snap.TopSQL[0].QueryID, snap.TopSQL[0].TotalTimeMs)
		}
		rows = append(rows, []any{
			snap.ID,
			snap.Timestamp.Format("01-02 15:04:05"),
			tps,
			topSQL,
			base,
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"#", "Time", "Commit/Rollback", "Top SQL", "Base"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 20, TermWidth: 120})

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Performance snapshots (%d total)\n\n%s", len(snaps), table),
		Summary:  fmt.Sprintf("%d snapshots", len(snaps)),
	}, nil
}

func (s *PGPerfSnapSkill) baseline() (*skill.Result, error) {
	if s.store.Count() == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No snapshots yet, run /perfsnap snap first",
		}, nil
	}

	snap, _ := s.store.At(-1)
	snap.Baseline = true

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Snapshot #%d marked as baseline", snap.ID),
		Summary:  fmt.Sprintf("baseline set: #%d", snap.ID),
	}, nil
}

// ── Capture ──

func (s *PGPerfSnapSkill) capture(ctx context.Context) (PGPerfSnapshot, error) {
	snap := PGPerfSnapshot{Timestamp: time.Now()}

	// pg_stat_database
	dbResult, err := s.driver.Query(ctx, pgPerfSnapDBStatSQL)
	if err != nil {
		return snap, fmt.Errorf("querying pg_stat_database: %w", err)
	}
	if len(dbResult.Rows) > 0 {
		row := dbResult.Rows[0]
		if len(row) >= 13 {
			snap.DBStat = &PGDBStat{
				DBName:      asStrPS(row[0]),
				Commits:     toInt64PS(row[1]),
				Rollbacks:   toInt64PS(row[2]),
				BlksRead:    toInt64PS(row[3]),
				BlksHit:     toInt64PS(row[4]),
				TupReturned: toInt64PS(row[5]),
				TupFetched:  toInt64PS(row[6]),
				TupInserted: toInt64PS(row[7]),
				TupUpdated:  toInt64PS(row[8]),
				TupDeleted:  toInt64PS(row[9]),
				Deadlocks:   toInt64PS(row[10]),
				TempBytes:   toInt64PS(row[11]),
				TempFiles:   toInt64PS(row[12]),
			}
		}
	}

	// pg_stat_bgwriter
	bgResult, err := s.driver.Query(ctx, pgPerfSnapBGWriterSQL)
	if err == nil && len(bgResult.Rows) > 0 {
		row := bgResult.Rows[0]
		if len(row) >= 7 {
			snap.BGWriter = &PGBGWriterStat{
				CheckpointsTimed: toInt64PS(row[0]),
				CheckpointsReq:   toInt64PS(row[1]),
				BufCheckpoint:    toInt64PS(row[2]),
				BufClean:         toInt64PS(row[3]),
				BufBackend:       toInt64PS(row[4]),
				MaxWrittenClean:  toInt64PS(row[5]),
				BufAlloc:         toInt64PS(row[6]),
			}
		}
	}

	// pg_stat_statements (may not be installed)
	sqlResult, err := s.driver.Query(ctx, pgPerfSnapTopSQLSQL)
	if err == nil {
		for _, row := range sqlResult.Rows {
			if len(row) < 8 {
				continue
			}
			snap.TopSQL = append(snap.TopSQL, PGSQLStat{
				QueryID:     asStrPS(row[0]),
				Calls:       toInt64PS(row[1]),
				TotalTimeMs: toFloat64PS(row[2]),
				MeanTimeMs:  toFloat64PS(row[3]),
				Rows:        toInt64PS(row[4]),
				SharedHit:   toInt64PS(row[5]),
				SharedRead:  toInt64PS(row[6]),
				QueryText:   asStrPS(row[7]),
			})
		}
	}

	return snap, nil
}

// ── Diff ──

func pgComputeDiff(old, new_ PGPerfSnapshot) PGPerfDiff {
	d := PGPerfDiff{
		From: old.Timestamp,
		To:   new_.Timestamp,
	}

	elapsed := new_.Timestamp.Sub(old.Timestamp).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	if old.DBStat != nil && new_.DBStat != nil {
		commitsDelta := float64(new_.DBStat.Commits - old.DBStat.Commits)
		rollbacksDelta := float64(new_.DBStat.Rollbacks - old.DBStat.Rollbacks)
		d.TPSDelta = (commitsDelta + rollbacksDelta) / elapsed

		d.CacheHitOld = pgCacheHitPct(old.DBStat)
		d.CacheHitNew = pgCacheHitPct(new_.DBStat)

		d.TempBytesOld = old.DBStat.TempBytes
		d.TempBytesNew = new_.DBStat.TempBytes

		d.DeadlocksOld = old.DBStat.Deadlocks
		d.DeadlocksNew = new_.DBStat.Deadlocks
	}

	if old.BGWriter != nil && new_.BGWriter != nil {
		d.CheckpointsOld = old.BGWriter.CheckpointsTimed + old.BGWriter.CheckpointsReq
		d.CheckpointsNew = new_.BGWriter.CheckpointsTimed + new_.BGWriter.CheckpointsReq
	}

	// SQL changes
	oldSQL := make(map[string]PGSQLStat)
	for _, s := range old.TopSQL {
		oldSQL[s.QueryID] = s
	}
	for _, s := range new_.TopSQL {
		if prev, ok := oldSQL[s.QueryID]; ok {
			deltaMs := s.TotalTimeMs - prev.TotalTimeMs
			if deltaMs > 1000 || deltaMs < -1000 {
				d.SQLTimeChanges = append(d.SQLTimeChanges, pgSQLChange{
					QueryID:    s.QueryID,
					OldTotalMs: prev.TotalTimeMs,
					NewTotalMs: s.TotalTimeMs,
					DeltaMs:    deltaMs,
				})
			}
		} else {
			d.NewHotSQL = append(d.NewHotSQL, s)
		}
	}
	sort.Slice(d.SQLTimeChanges, func(i, j int) bool {
		return pgAbs(d.SQLTimeChanges[i].DeltaMs) > pgAbs(d.SQLTimeChanges[j].DeltaMs)
	})

	return d
}

func pgCacheHitPct(stat *PGDBStat) float64 {
	total := float64(stat.BlksHit + stat.BlksRead)
	if total <= 0 {
		return 0
	}
	return float64(stat.BlksHit) / total * 100
}

func pgAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ── Render ──

func pgRenderSnap(snap PGPerfSnapshot, diff *PGPerfDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Performance Snapshot #%d (%s)\n\n", snap.ID, snap.Timestamp.Format("15:04:05"))

	// DB stats
	if snap.DBStat != nil {
		b.WriteString("-- pg_stat_database --\n")
		b.WriteString(fmt.Sprintf("  Commits: %d  Rollbacks: %d  Deadlocks: %d\n",
			snap.DBStat.Commits, snap.DBStat.Rollbacks, snap.DBStat.Deadlocks))
		b.WriteString(fmt.Sprintf("  Cache Hit: %.1f%%  Temp Bytes: %s\n",
			pgCacheHitPct(snap.DBStat), pgFormatBytes(snap.DBStat.TempBytes)))
	}

	// BGWriter
	if snap.BGWriter != nil {
		b.WriteString("\n-- pg_stat_bgwriter --\n")
		b.WriteString(fmt.Sprintf("  Checkpoints: timed=%d req=%d\n",
			snap.BGWriter.CheckpointsTimed, snap.BGWriter.CheckpointsReq))
		b.WriteString(fmt.Sprintf("  Buffers: checkpoint=%d clean=%d backend=%d\n",
			snap.BGWriter.BufCheckpoint, snap.BGWriter.BufClean, snap.BGWriter.BufBackend))
	}

	// Top SQL
	if len(snap.TopSQL) > 0 {
		b.WriteString("\n-- Top SQL (pg_stat_statements) --\n")
		var rows [][]any
		for _, s := range snap.TopSQL {
			if len(rows) >= 10 {
				break
			}
			rows = append(rows, []any{
				s.QueryID,
				s.Calls,
				fmt.Sprintf("%.1f", s.TotalTimeMs),
				fmt.Sprintf("%.2f", s.MeanTimeMs),
				s.Rows,
				s.QueryText,
			})
		}
		qr := &db.QueryResult{
			Columns: []string{"QueryID", "Calls", "TotalMs", "MeanMs", "Rows", "Query"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	// Diff section
	if diff != nil && pgHasDiffContent(*diff) {
		b.WriteString("\n-- vs Previous Snapshot --\n")
		b.WriteString(pgRenderDiffBrief(*diff))
	}

	return b.String()
}

func pgRenderDiff(d PGPerfDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Performance Compare (%s -> %s)\n\n",
		d.From.Format("15:04:05"), d.To.Format("15:04:05"))

	if !pgHasDiffContent(d) {
		b.WriteString("  No significant changes\n")
		return b.String()
	}

	b.WriteString(pgRenderDiffBrief(d))
	return b.String()
}

func pgRenderDiffBrief(d PGPerfDiff) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("  TPS: %.1f/s\n", d.TPSDelta))
	b.WriteString(fmt.Sprintf("  Cache Hit: %.1f%% -> %.1f%%\n", d.CacheHitOld, d.CacheHitNew))

	tempDelta := d.TempBytesNew - d.TempBytesOld
	if tempDelta != 0 {
		b.WriteString(fmt.Sprintf("  Temp Bytes: %s -> %s (%+s)\n",
			pgFormatBytes(d.TempBytesOld), pgFormatBytes(d.TempBytesNew), pgFormatBytes(tempDelta)))
	}

	deadlockDelta := d.DeadlocksNew - d.DeadlocksOld
	if deadlockDelta > 0 {
		b.WriteString(fmt.Sprintf("  Deadlocks: %d -> %d (+%d)\n",
			d.DeadlocksOld, d.DeadlocksNew, deadlockDelta))
	}

	checkpointDelta := d.CheckpointsNew - d.CheckpointsOld
	if checkpointDelta > 0 {
		b.WriteString(fmt.Sprintf("  Checkpoints: %d -> %d (+%d)\n",
			d.CheckpointsOld, d.CheckpointsNew, checkpointDelta))
	}

	if len(d.SQLTimeChanges) > 0 {
		b.WriteString("\n  SQL execution time changes:\n")
		var rows [][]any
		for _, c := range d.SQLTimeChanges {
			arrow := "+"
			if c.DeltaMs < 0 {
				arrow = ""
			}
			rows = append(rows, []any{
				c.QueryID,
				fmt.Sprintf("%.0fms", c.OldTotalMs),
				fmt.Sprintf("%.0fms", c.NewTotalMs),
				fmt.Sprintf("%s%.0fms", arrow, c.DeltaMs),
			})
		}
		qr := &db.QueryResult{
			Columns: []string{"QueryID", "Before", "After", "Delta"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	if len(d.NewHotSQL) > 0 {
		b.WriteString("\n  New hot SQL:\n")
		var rows [][]any
		for _, s := range d.NewHotSQL {
			rows = append(rows, []any{
				s.QueryID,
				s.Calls,
				fmt.Sprintf("%.1f", s.TotalTimeMs),
				s.QueryText,
			})
		}
		qr := &db.QueryResult{
			Columns: []string{"QueryID", "Calls", "TotalMs", "Query"},
			Rows:    rows,
		}
		b.WriteString(format.FormatTableOpts(qr, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	return b.String()
}

func pgHasDiffContent(d PGPerfDiff) bool {
	return d.TPSDelta != 0 ||
		d.CacheHitOld != d.CacheHitNew ||
		d.TempBytesOld != d.TempBytesNew ||
		d.DeadlocksOld != d.DeadlocksNew ||
		len(d.SQLTimeChanges) > 0 ||
		len(d.NewHotSQL) > 0
}

func pgFormatBytes(b int64) string {
	abs := b
	prefix := ""
	if abs < 0 {
		abs = -abs
		prefix = "-"
	}
	switch {
	case abs >= 1024*1024*1024:
		return fmt.Sprintf("%s%.1fGB", prefix, float64(abs)/1024/1024/1024)
	case abs >= 1024*1024:
		return fmt.Sprintf("%s%.1fMB", prefix, float64(abs)/1024/1024)
	case abs >= 1024:
		return fmt.Sprintf("%s%.1fKB", prefix, float64(abs)/1024)
	default:
		return fmt.Sprintf("%s%dB", prefix, abs)
	}
}

// ── helpers ──

func asStrPS(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func toInt64PS(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return int64(f)
	default:
		return 0
	}
}

func toFloat64PS(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}
