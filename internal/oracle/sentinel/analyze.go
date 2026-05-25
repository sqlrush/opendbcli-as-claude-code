/*-------------------------------------------------------------------------
 *
 * analyze.go
 *	  Analyze aggregates burst frames into a BurstReport.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/analyze.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"math"
	"sort"

	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
)

// Analyze aggregates burst frames into a BurstReport.
func Analyze(result BurstResult, baseline Baseline) BurstReport {
	report := BurstReport{
		TriggerEvent:   result.Trigger,
		PeakActive:     result.PeakActive,
		BaselineActive: baseline.AvgActive,
		RawFrameCount:  len(result.Frames),
		StartTime:      result.StartTime,
		EndTime:        result.EndTime,
	}
	if !result.EndTime.IsZero() && !result.StartTime.IsZero() {
		report.DurationSec = result.EndTime.Sub(result.StartTime).Seconds()
	}

	report.TopSQLs = AnalyzeSQLFrequency(result.Frames)
	report.BlockingChains = AnalyzeBlockingChains(result.Frames)
	report.WaitProfile = AnalyzeWaitDistribution(result.Frames)
	report.Metrics = AnalyzeMetrics(result.Frames)
	report.Classification = Classify(report)

	return report
}

// AnalyzeSQLFrequency counts how often each SQL_ID appears across frames.
func AnalyzeSQLFrequency(frames []BurstFrame) []SQLProfile {
	if len(frames) == 0 {
		return nil
	}

	type sqlAcc struct {
		sqlText       string
		planHash      int64
		event         string // dominant wait event
		waitClass     string
		eventCounts   map[string]int // event name -> count (to find dominant)
		totalElapsed  float64
		maxElapsed    float64
		frameSet      map[int]bool // frame indices where this SQL appeared
		maxConcurrent int
	}

	accum := make(map[string]*sqlAcc)

	for i, f := range frames {
		// Count concurrent sessions per SQL in this frame
		concurrency := make(map[string]int)
		for _, s := range f.Snapshot.Sessions {
			if s.SQLID == "" {
				continue
			}
			concurrency[s.SQLID]++

			acc, ok := accum[s.SQLID]
			if !ok {
				acc = &sqlAcc{
					frameSet:    make(map[int]bool),
					eventCounts: make(map[string]int),
				}
				accum[s.SQLID] = acc
			}
			acc.sqlText = s.SQLText
			acc.waitClass = s.WaitClass
			acc.eventCounts[s.Event]++
			acc.totalElapsed += s.ElapsedSec
			if s.ElapsedSec > acc.maxElapsed {
				acc.maxElapsed = s.ElapsedSec
			}
			acc.frameSet[i] = true
			if s.Burst != nil && s.Burst.PlanHashValue != 0 {
				acc.planHash = s.Burst.PlanHashValue
			}
		}
		for sqlID, cnt := range concurrency {
			if a, ok := accum[sqlID]; ok && cnt > a.maxConcurrent {
				a.maxConcurrent = cnt
			}
		}
	}

	totalFrames := len(frames)
	profiles := make([]SQLProfile, 0, len(accum))
	for sqlID, acc := range accum {
		// Find dominant event for this SQL.
		dominantEvent := acc.event
		maxCount := 0
		for ev, cnt := range acc.eventCounts {
			if cnt > maxCount {
				maxCount = cnt
				dominantEvent = ev
			}
		}

		occurrences := len(acc.frameSet)
		profiles = append(profiles, SQLProfile{
			SQLID:          sqlID,
			SQLText:        acc.sqlText,
			PlanHashValue:  acc.planHash,
			OccurrenceRate: float64(occurrences) / float64(totalFrames),
			AvgElapsedSec:  acc.totalElapsed / float64(occurrences),
			MaxElapsedSec:  acc.maxElapsed,
			Event:          dominantEvent,
			WaitClass:      acc.waitClass,
			MaxConcurrent:  acc.maxConcurrent,
		})
	}

	// Sort by max elapsed * concurrent (impact score).
	sort.Slice(profiles, func(i, j int) bool {
		si := profiles[i].MaxElapsedSec * float64(profiles[i].MaxConcurrent)
		sj := profiles[j].MaxElapsedSec * float64(profiles[j].MaxConcurrent)
		return si > sj
	})
	return profiles
}

// AnalyzeBlockingChains traces BlockingSID to find root blockers.
func AnalyzeBlockingChains(frames []BurstFrame) []BlockingChain {
	// Aggregate blocking relationships across all frames.
	type blockRel struct {
		blockerSQLID string
		blockerEvent string
		victims      map[int]bool
	}

	blockers := make(map[int]*blockRel) // root SID -> info

	for _, f := range frames {
		for _, s := range f.Snapshot.Sessions {
			if s.Burst == nil || s.Burst.BlockingSID == 0 {
				continue
			}
			rootSID := findRoot(f.Snapshot.Sessions, s.Burst.BlockingSID)
			rel, ok := blockers[rootSID]
			if !ok {
				rel = &blockRel{victims: make(map[int]bool)}
				blockers[rootSID] = rel
			}
			rel.victims[s.SID] = true
			// Try to fill root info from sessions
			for _, rs := range f.Snapshot.Sessions {
				if rs.SID == rootSID {
					rel.blockerSQLID = rs.SQLID
					rel.blockerEvent = rs.Event
					break
				}
			}
		}
	}

	chains := make([]BlockingChain, 0, len(blockers))
	for sid, rel := range blockers {
		chains = append(chains, BlockingChain{
			RootSID:     sid,
			RootSQLID:   rel.blockerSQLID,
			RootEvent:   rel.blockerEvent,
			VictimCount: len(rel.victims),
			MaxDepth:    1, // simplified: we only track 1-level blocking
		})
	}

	sort.Slice(chains, func(i, j int) bool {
		return chains[i].VictimCount > chains[j].VictimCount
	})
	return chains
}

// findRoot follows the BlockingSID chain to find the root blocker.
func findRoot(sessions []dbtop.SessionRow, blockingSID int) int {
	visited := make(map[int]bool)
	current := blockingSID
	for {
		if visited[current] {
			return current // cycle detected
		}
		visited[current] = true

		found := false
		for _, s := range sessions {
			if s.SID == current && s.Burst != nil && s.Burst.BlockingSID != 0 {
				current = s.Burst.BlockingSID
				found = true
				break
			}
		}
		if !found {
			return current
		}
	}
}

// AnalyzeWaitDistribution aggregates wait events by event name (not class).
func AnalyzeWaitDistribution(frames []BurstFrame) []WaitBucket {
	if len(frames) == 0 {
		return nil
	}

	type eventAcc struct {
		waitClass string
		totalMs   float64
	}
	totals := make(map[string]*eventAcc)
	for _, f := range frames {
		for _, ev := range f.Snapshot.Events {
			if ev.DTimeMs > 0 {
				acc, ok := totals[ev.Event]
				if !ok {
					acc = &eventAcc{waitClass: ev.WaitClass}
					totals[ev.Event] = acc
				}
				acc.totalMs += ev.DTimeMs
			}
		}
	}

	var grandTotal float64
	for _, acc := range totals {
		grandTotal += acc.totalMs
	}

	buckets := make([]WaitBucket, 0, len(totals))
	for event, acc := range totals {
		pct := 0.0
		if grandTotal > 0 {
			pct = acc.totalMs / grandTotal * 100
		}
		buckets = append(buckets, WaitBucket{
			Event:      event,
			WaitClass:  acc.waitClass,
			TotalMs:    acc.totalMs,
			Percentage: pct,
		})
	}

	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].TotalMs > buckets[j].TotalMs
	})
	return buckets
}

// AnalyzeMetrics computes summary stats for key metrics across frames.
func AnalyzeMetrics(frames []BurstFrame) map[string]MetricSummary {
	if len(frames) == 0 {
		return nil
	}

	type collector struct {
		values []float64
	}

	// Use MetricName constants as keys so renderBlockF can look them up.
	// Also include dbtop-specific composite metrics.
	keys := []string{
		string(MetricActive), string(MetricCPU), string(MetricIO),
		string(MetricRedoRate), string(MetricTotalSessions),
		string(MetricCommitRate), // TPS mapped to commit_rate
		"db%", "wtr%", "qps",
	}
	collectors := make(map[string]*collector, len(keys))
	for _, k := range keys {
		collectors[k] = &collector{values: make([]float64, 0, len(frames))}
	}

	for _, f := range frames {
		s := f.Snapshot
		collectors[string(MetricActive)].values = append(collectors[string(MetricActive)].values, float64(s.ActiveCount))
		collectors[string(MetricCPU)].values = append(collectors[string(MetricCPU)].values, float64(s.ActiveCPU))
		collectors[string(MetricIO)].values = append(collectors[string(MetricIO)].values, float64(s.ActiveIO))
		collectors[string(MetricRedoRate)].values = append(collectors[string(MetricRedoRate)].values, s.RedoKBs)
		collectors[string(MetricTotalSessions)].values = append(collectors[string(MetricTotalSessions)].values, float64(s.TotalSessions))
		collectors[string(MetricCommitRate)].values = append(collectors[string(MetricCommitRate)].values, s.TPS)
		collectors["db%"].values = append(collectors["db%"].values, s.DBPercent)
		collectors["wtr%"].values = append(collectors["wtr%"].values, s.WTRPercent)
		collectors["qps"].values = append(collectors["qps"].values, s.QPS)
	}

	result := make(map[string]MetricSummary, len(collectors))
	for name, c := range collectors {
		result[name] = summarize(c.values)
	}
	return result
}

// summarize computes avg/max/min and detects trend for a slice of values.
func summarize(vals []float64) MetricSummary {
	if len(vals) == 0 {
		return MetricSummary{}
	}

	sum := 0.0
	minV := vals[0]
	maxV := vals[0]
	for _, v := range vals {
		sum += v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	avg := sum / float64(len(vals))

	// Trend: compare first-half avg vs second-half avg
	mid := len(vals) / 2
	if mid == 0 {
		return MetricSummary{Avg: avg, Max: maxV, Min: minV, Trend: "stable"}
	}
	firstHalf := avgSlice(vals[:mid])
	secondHalf := avgSlice(vals[mid:])

	trend := detectTrend(firstHalf, secondHalf)
	return MetricSummary{Avg: avg, Max: maxV, Min: minV, Trend: trend}
}

// detectTrend determines the trend based on first vs second half comparison.
func detectTrend(first, second float64) string {
	if first == 0 && second == 0 {
		return "stable"
	}
	base := math.Abs(first)
	if base < 0.001 {
		base = 1.0
	}
	changePct := (second - first) / base * 100

	switch {
	case changePct > 50:
		return "spike"
	case changePct > 15:
		return "rising"
	case changePct < -50:
		return "drop"
	case changePct < -15:
		return "falling"
	default:
		return "stable"
	}
}

// avgSlice returns the average of a float64 slice.
func avgSlice(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
