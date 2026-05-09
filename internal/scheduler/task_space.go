/*-------------------------------------------------------------------------
 *
 * task_space.go
 *	  SpaceRecord is a tablespace usage snapshot for trend analysis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/task_space.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SpaceRecord is a tablespace usage snapshot for trend analysis.
type SpaceRecord struct {
	Timestamp time.Time              `json:"timestamp"`
	Spaces    map[string]SpaceUsage  `json:"spaces"`
}

// SpaceUsage holds a single tablespace usage reading.
type SpaceUsage struct {
	UsedMB  float64 `json:"used_mb"`
	TotalMB float64 `json:"total_mb"`
	UsedPct float64 `json:"used_pct"`
}

// SpaceAlert is generated when a tablespace exceeds a threshold.
type SpaceAlert struct {
	Tablespace string  `json:"tablespace"`
	UsedPct    float64 `json:"used_pct"`
	Severity   string  `json:"severity"` // "WARN" | "CRITICAL"
	GrowthRate float64 `json:"growth_rate_mb_per_hour,omitempty"`
	PredictFull string `json:"predict_full,omitempty"`
}

const maxSpaceRecords = 144 // 24h at 10min interval

// SpaceTracker tracks tablespace usage over time for trend analysis.
type SpaceTracker struct {
	mu      sync.Mutex
	records []SpaceRecord
	path    string
}

// NewSpaceTracker creates a tracker persisted to the given directory.
func NewSpaceTracker(dir string) *SpaceTracker {
	t := &SpaceTracker{
		path: filepath.Join(dir, "space_snapshots.json"),
	}
	t.load()
	return t
}

// Add records a new space snapshot and persists to disk.
func (t *SpaceTracker) Add(record SpaceRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.records = append(t.records, record)
	if len(t.records) > maxSpaceRecords {
		t.records = t.records[len(t.records)-maxSpaceRecords:]
	}
	t.save()
}

// Analyze checks for threshold violations and growth anomalies.
// warnPct is the WARN threshold (default 85), critPct is CRITICAL (default 95).
func (t *SpaceTracker) Analyze(latest SpaceRecord, warnPct, critPct float64) []SpaceAlert {
	t.mu.Lock()
	records := make([]SpaceRecord, len(t.records))
	copy(records, t.records)
	t.mu.Unlock()

	var alerts []SpaceAlert
	for name, usage := range latest.Spaces {
		// Threshold check.
		if usage.UsedPct >= critPct {
			alert := SpaceAlert{
				Tablespace: name,
				UsedPct:    usage.UsedPct,
				Severity:   "CRITICAL",
			}
			addGrowthInfo(&alert, name, records)
			alerts = append(alerts, alert)
		} else if usage.UsedPct >= warnPct {
			alert := SpaceAlert{
				Tablespace: name,
				UsedPct:    usage.UsedPct,
				Severity:   "WARN",
			}
			addGrowthInfo(&alert, name, records)
			alerts = append(alerts, alert)
		}
	}

	return alerts
}

// addGrowthInfo calculates growth rate and prediction using linear regression.
func addGrowthInfo(alert *SpaceAlert, name string, records []SpaceRecord) {
	if len(records) < 3 {
		return
	}

	// Collect data points for linear regression.
	var xs, ys []float64
	base := records[0].Timestamp
	for _, r := range records {
		if usage, ok := r.Spaces[name]; ok {
			hours := r.Timestamp.Sub(base).Hours()
			xs = append(xs, hours)
			ys = append(ys, usage.UsedMB)
		}
	}
	if len(xs) < 3 {
		return
	}

	slope := linearRegressionSlope(xs, ys) // MB per hour
	if slope <= 0 {
		return
	}

	alert.GrowthRate = math.Round(slope*100) / 100

	// Predict when 100% full.
	last := records[len(records)-1]
	if usage, ok := last.Spaces[name]; ok && usage.TotalMB > usage.UsedMB {
		remaining := usage.TotalMB - usage.UsedMB
		hoursToFull := remaining / slope
		if hoursToFull < 168 { // only predict within 7 days
			fullTime := last.Timestamp.Add(time.Duration(hoursToFull * float64(time.Hour)))
			alert.PredictFull = fullTime.Format("01-02 15:04")
		}
	}
}

// linearRegressionSlope computes the slope of a simple linear regression.
func linearRegressionSlope(xs, ys []float64) float64 {
	n := float64(len(xs))
	if n < 2 {
		return 0
	}

	var sumX, sumY, sumXY, sumX2 float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
		sumXY += xs[i] * ys[i]
		sumX2 += xs[i] * xs[i]
	}

	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		return 0
	}

	return (n*sumXY - sumX*sumY) / denom
}

func (t *SpaceTracker) load() {
	data, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var records []SpaceRecord
	if json.Unmarshal(data, &records) == nil {
		if len(records) > maxSpaceRecords {
			records = records[len(records)-maxSpaceRecords:]
		}
		t.records = records
	}
}

func (t *SpaceTracker) save() {
	dir := filepath.Dir(t.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(t.records)
	if err != nil {
		return
	}
	tmp := t.path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		os.Rename(tmp, t.path)
	}
}

// FormatSpaceAlerts returns a human-readable alert string for REPL display.
func FormatSpaceAlerts(alerts []SpaceAlert) string {
	if len(alerts) == 0 {
		return ""
	}
	var lines []string
	for _, a := range alerts {
		line := fmt.Sprintf("%s %s at %.0f%%", a.Severity, a.Tablespace, a.UsedPct)
		if a.GrowthRate > 0 {
			line += fmt.Sprintf(" (增长 %.1f MB/h", a.GrowthRate)
			if a.PredictFull != "" {
				line += fmt.Sprintf(", 预计 %s 满", a.PredictFull)
			}
			line += ")"
		}
		lines = append(lines, line)
	}
	return fmt.Sprintf("空间告警: %s", joinLines(lines))
}

func joinLines(lines []string) string {
	if len(lines) == 1 {
		return lines[0]
	}
	result := lines[0]
	for _, l := range lines[1:] {
		result += "; " + l
	}
	return result
}
