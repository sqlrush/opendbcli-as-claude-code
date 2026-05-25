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
 *	  internal/mysql/monitor/dbtop/health.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "fmt"

const (
	anWarn      = 20
	anCrit      = 50
	dbPctWarn   = 30.0
	dbPctCrit   = 60.0
	wtrWarn     = 30.0
	wtrCrit     = 60.0
	bpHitWarn   = 95.0 // Buffer Pool hit ratio warning below this
	etWarn      = 300.0
	etCrit      = 600.0
	tpsDropWarn = 50.0
	tpsDropCrit = 80.0
	evtPctWarn  = 30.0
	evtPctCrit  = 50.0
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

	// Active sessions check (always evaluated, not delta-dependent).
	switch {
	case s.ActiveCount > anCrit:
		promote(Critical, fmt.Sprintf("AN=%d (>%d)", s.ActiveCount, anCrit))
	case s.ActiveCount > anWarn:
		promote(Warning, fmt.Sprintf("AN=%d (>%d)", s.ActiveCount, anWarn))
	}

	// Buffer Pool utilization check.
	if s.BPMaxMB > 0 {
		bpPct := s.BPUsedMB / s.BPMaxMB * 100
		if bpPct < bpHitWarn {
			// Low buffer pool usage might indicate cold cache
		}
		if bpPct > 95 {
			promote(Warning, fmt.Sprintf("BP%%=%.1f (>95)", bpPct))
		}
	}

	if s.HasDelta {
		// DBPercent (Active / max_connections).
		switch {
		case s.DBPercent > dbPctCrit:
			promote(Critical, fmt.Sprintf("db%%=%.1f (>%.0f)", s.DBPercent, dbPctCrit))
		case s.DBPercent > dbPctWarn:
			promote(Warning, fmt.Sprintf("db%%=%.1f (>%.0f)", s.DBPercent, dbPctWarn))
		}

		// WTR% check.
		switch {
		case s.WTRPercent > wtrCrit:
			promote(Critical, fmt.Sprintf("WTR%%=%.1f (>%.0f)", s.WTRPercent, wtrCrit))
		case s.WTRPercent > wtrWarn:
			promote(Warning, fmt.Sprintf("WTR%%=%.1f (>%.0f)", s.WTRPercent, wtrWarn))
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
	}

	// Event PCT check.
	for _, e := range s.Events {
		switch {
		case e.PCT > evtPctCrit:
			promote(Critical, fmt.Sprintf("%s PCT=%.1f%%", e.Event, e.PCT))
		case e.PCT > evtPctWarn:
			promote(Warning, fmt.Sprintf("%s PCT=%.1f%%", e.Event, e.PCT))
		}
	}

	// Session elapsed time check.
	for _, sess := range s.Sessions {
		switch {
		case sess.ElapsedSec > etCrit:
			promote(Critical, fmt.Sprintf("SID %d E/T=%.0fs", sess.SID, sess.ElapsedSec))
		case sess.ElapsedSec > etWarn:
			promote(Warning, fmt.Sprintf("SID %d E/T=%.0fs", sess.SID, sess.ElapsedSec))
		}
	}
}
