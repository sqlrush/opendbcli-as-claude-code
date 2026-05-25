/*-------------------------------------------------------------------------
 *
 * health.go
 *	  EvaluateHealth evaluates the snapshot and sets Health and Alerts.
 *	  prevTPS is the TPS from the previous frame (for drop detection).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/monitor/dbtop/health.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "fmt"

const (
	anWarn        = 20
	anCrit        = 50
	cacheHitWarn  = 95.0
	etWarn        = 300.0
	etCrit        = 600.0
	tpsDropWarn   = 50.0
	tpsDropCrit   = 80.0
)

// EvaluateHealth evaluates the snapshot and sets Health and Alerts.
// prevTPS is the TPS from the previous frame (for drop detection).
func EvaluateHealth(s *Snapshot, prevTPS float64) {
	s.Health = Healthy
	s.Alerts = nil

	promote := func(level HealthLevel, msg string) {
		if level > s.Health {
			s.Health = level
		}
		s.Alerts = append(s.Alerts, msg)
	}

	// Active sessions check.
	switch {
	case s.ActiveCount > anCrit:
		promote(Critical, fmt.Sprintf("AN=%d (>%d)", s.ActiveCount, anCrit))
	case s.ActiveCount > anWarn:
		promote(Warning, fmt.Sprintf("AN=%d (>%d)", s.ActiveCount, anWarn))
	}

	// Cache hit ratio check.
	if s.CacheHitPct > 0 && s.CacheHitPct < cacheHitWarn {
		promote(Warning, fmt.Sprintf("CacheHit=%.1f%% (<%.0f%%)", s.CacheHitPct, cacheHitWarn))
	}

	// TPS drop detection.
	if prevTPS > 0 && s.TPS < prevTPS {
		dropPct := (prevTPS - s.TPS) / prevTPS * 100
		switch {
		case dropPct > tpsDropCrit:
			promote(Critical, fmt.Sprintf("TPS drop %.0f%%", dropPct))
		case dropPct > tpsDropWarn:
			promote(Warning, fmt.Sprintf("TPS drop %.0f%%", dropPct))
		}
	}

	// Long-running session check.
	for _, sess := range s.Sessions {
		switch {
		case sess.ElapsedSec > etCrit:
			promote(Critical, fmt.Sprintf("PID %d E/T=%.0fs", sess.PID, sess.ElapsedSec))
		case sess.ElapsedSec > etWarn:
			promote(Warning, fmt.Sprintf("PID %d E/T=%.0fs", sess.PID, sess.ElapsedSec))
		}
	}
}
