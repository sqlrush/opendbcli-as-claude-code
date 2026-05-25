/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  Source resolution for /wdranalyze: either generate a fresh WDR via
 *	  dbe_perf.generate_wdr_report() or load an existing file.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/collector.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// Collector fetches raw WDR content based on Options. Pure I/O — no parsing.
type Collector struct {
	driver db.Driver // may be nil for file-only mode
}

func NewCollector(driver db.Driver) *Collector {
	return &Collector{driver: driver}
}

// Fetch returns the raw WDR text/HTML and the resolved snapshot range. For
// file mode, snapshot IDs are -1.
func (c *Collector) Fetch(ctx context.Context, opts Options) (string, int64, int64, error) {
	switch opts.Mode {
	case "file":
		return c.fetchFromFile(opts.FilePath)
	case "snapshot":
		return c.fetchFromSnapshots(ctx, opts.SnapshotA, opts.SnapshotB)
	case "latest":
		a, b, err := c.resolveLatestPair(ctx)
		if err != nil {
			return "", 0, 0, err
		}
		return c.fetchFromSnapshots(ctx, a, b)
	case "timerange":
		a, b, err := c.resolveTimeWindow(ctx, opts.Window)
		if err != nil {
			return "", 0, 0, err
		}
		return c.fetchFromSnapshots(ctx, a, b)
	default:
		return "", 0, 0, fmt.Errorf("unknown collector mode: %q", opts.Mode)
	}
}

func (c *Collector) fetchFromFile(path string) (string, int64, int64, error) {
	if path == "" {
		return "", 0, 0, fmt.Errorf("file mode requires --path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return "", 0, 0, fmt.Errorf("file %s is empty", path)
	}
	return string(data), -1, -1, nil
}

// fetchFromSnapshots calls generate_wdr_report to produce a fresh report.
// GaussDB/openGauss builds differ here: some expose pg_catalog.generate_wdr_report
// with bigint/cstring arguments, while older builds expose dbe_perf.generate_wdr_report
// with a numeric report level. Try the concrete signatures in a controlled order
// before surfacing an error to the user.
func (c *Collector) fetchFromSnapshots(ctx context.Context, snapA, snapB int64) (string, int64, int64, error) {
	if c.driver == nil {
		return "", 0, 0, fmt.Errorf("snapshot mode requires DB connection")
	}
	if snapA <= 0 || snapB <= 0 {
		return "", 0, 0, fmt.Errorf("invalid snapshot IDs: %d → %d", snapA, snapB)
	}
	if snapA >= snapB {
		// Auto-fix swapped order
		snapA, snapB = snapB, snapA
	}

	var lastErr error
	// Try summary first for lower payload, then detail-all.
	for _, detail := range []string{"summary", "all"} {
		for _, query := range wdrReportQueries(snapA, snapB, detail) {
			cctx, cancel := contextWithTimeout(ctx, 90*time.Second)
			res, err := c.driver.Query(cctx, query)
			cancel()
			if err != nil {
				lastErr = fmt.Errorf("generate_wdr_report(%d, %d, %s): %w", snapA, snapB, detail, err)
				continue
			}
			if res == nil || len(res.Rows) == 0 {
				continue
			}
			var sb strings.Builder
			for _, row := range res.Rows {
				if len(row) > 0 {
					switch v := row[0].(type) {
					case string:
						sb.WriteString(v)
					case []byte:
						sb.Write(v)
					}
					sb.WriteString("\n")
				}
			}
			if sb.Len() > 0 {
				return sb.String(), snapA, snapB, nil
			}
		}
	}
	if lastErr != nil {
		return "", snapA, snapB, lastErr
	}
	return "", snapA, snapB, fmt.Errorf("generate_wdr_report returned no content (snap %d → %d)", snapA, snapB)
}

func wdrReportQueries(snapA, snapB int64, detail string) []string {
	return []string{
		fmt.Sprintf(`SELECT pg_catalog.generate_wdr_report(%d::bigint, %d::bigint, '%s', 'cluster', '')`, snapA, snapB, detail),
		fmt.Sprintf(`SELECT generate_wdr_report(%d::bigint, %d::bigint, '%s', 'cluster', '')`, snapA, snapB, detail),
		fmt.Sprintf(`SELECT dbe_perf.generate_wdr_report(%d, %d, %d, '%s', 'cluster')`, snapA, snapB, defaultReportLevelMin, detail),
	}
}

// resolveLatestPair finds the two most recent snapshots so we can diff them.
// Uses snapshot.snapshot view if present (og standard), falls back to
// pg_class probe if missing.
func (c *Collector) resolveLatestPair(ctx context.Context) (int64, int64, error) {
	res, err := c.driver.Query(ctx,
		`SELECT snapshot_id FROM snapshot.snapshot ORDER BY snapshot_id DESC LIMIT 2`)
	if err != nil {
		return 0, 0, fmt.Errorf(
			"cannot list snapshots: %w (hint: ensure enable_wdr_snapshot=on and at least 2 snapshots have been taken)", err)
	}
	if res == nil || len(res.Rows) < 2 {
		return 0, 0, fmt.Errorf(
			"need >=2 WDR snapshots; current count: %d (run: SELECT create_wdr_snapshot(); twice to generate)", len(res.Rows))
	}
	end := toInt64(res.Rows[0][0])
	start := toInt64(res.Rows[1][0])
	return start, end, nil
}

// resolveTimeWindow finds the snapshot pair that brackets the requested
// time window. Returns the smallest snapshot range that fully contains
// [now-window, now].
func (c *Collector) resolveTimeWindow(ctx context.Context, window time.Duration) (int64, int64, error) {
	if window <= 0 {
		return 0, 0, fmt.Errorf("window must be > 0")
	}
	res, err := c.driver.Query(ctx,
		`SELECT snapshot_id, start_ts FROM snapshot.snapshot WHERE start_ts >= NOW() - INTERVAL '`+window.String()+
			`' ORDER BY snapshot_id ASC`)
	if err != nil {
		return 0, 0, fmt.Errorf("query snapshots by window: %w", err)
	}
	if res == nil || len(res.Rows) < 2 {
		return 0, 0, fmt.Errorf(
			"no snapshot pair found within window %s (need at least 2 snapshots)", window)
	}
	start := toInt64(res.Rows[0][0])
	end := toInt64(res.Rows[len(res.Rows)-1][0])
	return start, end, nil
}

// defaultReportLevelMin matches og's lowest detail level (1). Generate gives
// a comprehensive report; we'll filter sections in our renderer.
const defaultReportLevelMin = 1

func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// isRetryableErr returns true for errors that suggest "try a different
// detail level" (e.g. permission denied, level not supported).
func isRetryableErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, hint := range []string{"permission", "denied", "not supported", "invalid level"} {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float64:
		return int64(x)
	case string:
		i := parseInt(x)
		return i
	case []byte:
		return parseInt(string(x))
	}
	return 0
}
