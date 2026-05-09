/*-------------------------------------------------------------------------
 *
 * classify_hang.go
 *	  Anomaly classifier (hang) for Oracle Sentinel — turns raw
 *	  probe deltas into typed events the engine routes to the right
 *	  diagnosis path.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/classify_hang.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// classify_hang.go — Database hang detection via 4-step elimination.

// classifyDatabaseHang uses a 4-step elimination approach to detect a true
// database hang. This is the lowest priority classification — it only triggers
// when no other specific root cause matches.
//
// Step 1: Pre-screen — active_sessions high + TPS low
// Step 2: Wait event elimination — if any single wait class > 60%, not hang
// Step 3: Positive scoring (multiple weak signals)
// Step 4: score >= 0.5 → DatabaseHang
func classifyDatabaseHang(r BurstReport) Classification {
	// ── Step 1: Pre-screen ──
	// Hang requires: active sessions elevated AND throughput collapsed.
	activeMetric, hasActive := r.Metrics[string(MetricActive)]
	if !hasActive || activeMetric.Max < 10 {
		return Classification{}
	}

	// TPS should be low (near zero or collapsed)
	tpsMetric, hasTPS := r.Metrics[string(MetricCommitRate)]
	if hasTPS && tpsMetric.Avg > 50 {
		// TPS still healthy — not a hang
		return Classification{}
	}

	// ── Step 2: Wait event elimination ──
	// If any single wait class dominates (>60%), it's a specific problem, not hang.
	eliminationClasses := []string{
		"Concurrency", // → Latch storm
		"Application", // → Lock contention
		"User I/O",    // → IO subsystem
		"System I/O",  // → IO subsystem
		"Commit",      // → Redo bottleneck
	}
	for _, class := range eliminationClasses {
		if waitClassTotal(r.WaitProfile, class) > 60 {
			return Classification{}
		}
	}

	// ── Step 3: Positive scoring ──
	score := 0.0
	evidence := []string{}

	// Signal 1: Wait events scattered (no single class >40%)
	scattered := true
	for _, class := range []string{"Concurrency", "Application", "User I/O", "System I/O", "Commit", "Network"} {
		if waitClassTotal(r.WaitProfile, class) > 40 {
			scattered = false
			break
		}
	}
	if scattered {
		score += 0.2
		evidence = append(evidence, "等待事件分散, 无单类超过40%")
	}

	// Signal 2: Instance status not OPEN
	if inst, ok := r.Metrics[string(MetricInstanceStatus)]; ok && inst.Max != 1.0 {
		score += 0.3
		evidence = append(evidence, "实例状态非 OPEN")
	}

	// Signal 3: Sessions > 95% of limit
	for _, rl := range r.ResourceLimits {
		if rl.ResourceName == "sessions" && rl.LimitValue > 0 {
			usedPct := rl.CurrentUtilization / rl.LimitValue * 100
			if usedPct > 95 {
				score += 0.15
				evidence = append(evidence,
					formatEvidence("会话数 %.0f/%.0f (%.1f%%), 接近耗尽",
						rl.CurrentUtilization, rl.LimitValue, usedPct))
			}
			break
		}
	}

	// Signal 4: Archive-related wait present
	archPct := waitEventPct(r.WaitProfile, "log file switch (archiving needed)")
	if archPct > 0 {
		score += 0.15
		evidence = append(evidence,
			formatEvidence("存在归档等待 \"log file switch (archiving needed)\" %.1f%%", archPct))
	}

	// Signal 5: Active/baseline ratio > 10x
	if r.BaselineActive > 0 && r.PeakActive > 0 {
		ratio := float64(r.PeakActive) / r.BaselineActive
		if ratio > 10 {
			score += 0.2
			evidence = append(evidence,
				formatEvidence("活跃会话/基线比 %.1fx (峰值 %d, 基线 %.1f)",
					ratio, r.PeakActive, r.BaselineActive))
		}
	}

	// ── Step 4: Judgment ──
	if score < 0.5 {
		return Classification{}
	}

	if score > 1.0 {
		score = 1.0
	}

	return Classification{
		Cause:      CauseDatabaseHang,
		Confidence: score,
		Evidence:   evidence,
	}
}
