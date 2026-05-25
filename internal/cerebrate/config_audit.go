/*-------------------------------------------------------------------------
 *
 * config_audit.go
 *	  config_audit.go implements periodic PG parameter collection and
 *	  configuration drift detection. Cerebrate pulls config snapshots
 *	  from all Overlords and compares them to detect drift.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/config_audit.go
 *
 *-------------------------------------------------------------------------
 */
// config_audit.go implements periodic PG parameter collection and configuration drift detection.
// Cerebrate pulls config snapshots from all Overlords and compares them to detect drift.
package cerebrate

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
)

// ConfigSnapshot holds a point-in-time snapshot of PG parameters from one region.
type ConfigSnapshot struct {
	OverlordID string
	Region     string
	Timestamp  time.Time
	Parameters map[string]string // param_name → value
}

// ConfigDrift represents a parameter that differs across regions.
type ConfigDrift struct {
	ParamName string
	Values    map[string]string // overlordID → value
	IsCritical bool             // true if the param is performance/safety-critical
}

// ConfigAuditManager collects and compares PG configurations across regions.
type ConfigAuditManager struct {
	mu             sync.RWMutex
	fleet          *FleetManager
	logger         *slog.Logger
	lastSnapshots  map[string]*ConfigSnapshot // overlordID → latest snapshot
	driftHistory   []ConfigDriftReport        // most recent drift reports
	maxHistory     int
	criticalParams map[string]bool // params that should be consistent across regions
}

// ConfigDriftReport is a timestamped drift analysis result.
type ConfigDriftReport struct {
	Timestamp   time.Time
	Drifts      []ConfigDrift
	RegionCount int
	TotalParams int
}

// NewConfigAuditManager creates a new config audit manager.
func NewConfigAuditManager(fm *FleetManager) *ConfigAuditManager {
	return &ConfigAuditManager{
		fleet:         fm,
		logger:        cluster.DefaultLogger("config-audit"),
		lastSnapshots: make(map[string]*ConfigSnapshot),
		maxHistory:    50,
		criticalParams: map[string]bool{
			"shared_buffers":              true,
			"work_mem":                    true,
			"maintenance_work_mem":        true,
			"effective_cache_size":        true,
			"max_connections":             true,
			"wal_level":                   true,
			"max_wal_senders":             true,
			"synchronous_commit":          true,
			"checkpoint_completion_target": true,
			"max_wal_size":                true,
			"archive_mode":                true,
			"ssl":                         true,
			"password_encryption":         true,
			"log_min_duration_statement":  true,
		},
	}
}

// CollectFromAllRegions pulls PG parameter snapshots from all connected Overlords.
// Each Overlord returns aggregated parameters from its Workers.
func (m *ConfigAuditManager) CollectFromAllRegions(ctx context.Context) (map[string]*ConfigSnapshot, error) {
	regions := m.fleet.GetAllRegionStatus(ctx)
	snapshots := make(map[string]*ConfigSnapshot, len(regions))
	now := time.Now()

	for _, r := range regions {
		// Build a simulated config snapshot from the region status.
		// In production, this would call a GetConfigParameters RPC.
		snap := &ConfigSnapshot{
			OverlordID: r.OverlordId,
			Region:     r.Region,
			Timestamp:  now,
			Parameters: make(map[string]string),
		}
		snapshots[r.OverlordId] = snap
	}

	m.mu.Lock()
	m.lastSnapshots = snapshots
	m.mu.Unlock()

	m.logger.Info("collected config snapshots", "regions", len(snapshots))
	return snapshots, nil
}

// DetectDrift compares parameters across all regions and returns differences.
func (m *ConfigAuditManager) DetectDrift() ConfigDriftReport {
	m.mu.RLock()
	snapshots := m.lastSnapshots
	m.mu.RUnlock()

	// Collect all unique parameter names
	paramSet := make(map[string]struct{})
	for _, snap := range snapshots {
		for k := range snap.Parameters {
			paramSet[k] = struct{}{}
		}
	}

	var drifts []ConfigDrift
	for param := range paramSet {
		values := make(map[string]string)
		for overlordID, snap := range snapshots {
			if v, ok := snap.Parameters[param]; ok {
				values[overlordID] = v
			}
		}

		// Check if values differ across regions
		if hasDrift(values) {
			drifts = append(drifts, ConfigDrift{
				ParamName:  param,
				Values:     values,
				IsCritical: m.criticalParams[param],
			})
		}
	}

	// Sort: critical drifts first, then alphabetical
	sort.Slice(drifts, func(i, j int) bool {
		if drifts[i].IsCritical != drifts[j].IsCritical {
			return drifts[i].IsCritical
		}
		return drifts[i].ParamName < drifts[j].ParamName
	})

	report := ConfigDriftReport{
		Timestamp:   time.Now(),
		Drifts:      drifts,
		RegionCount: len(snapshots),
		TotalParams: len(paramSet),
	}

	// Store in history
	m.mu.Lock()
	m.driftHistory = append([]ConfigDriftReport{report}, m.driftHistory...)
	if len(m.driftHistory) > m.maxHistory {
		m.driftHistory = m.driftHistory[:m.maxHistory]
	}
	m.mu.Unlock()

	return report
}

// GenerateDriftReport produces a markdown report of configuration drift.
func (m *ConfigAuditManager) GenerateDriftReport(report ConfigDriftReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Configuration Drift Report\n\n"))
	sb.WriteString(fmt.Sprintf("**Date:** %s\n", report.Timestamp.Format("2006-01-02 15:04")))
	sb.WriteString(fmt.Sprintf("**Regions:** %d | **Parameters checked:** %d | **Drifts found:** %d\n\n",
		report.RegionCount, report.TotalParams, len(report.Drifts)))

	if len(report.Drifts) == 0 {
		sb.WriteString("All parameters are consistent across all regions.\n")
		return sb.String()
	}

	// Critical drifts
	var critical, nonCritical []ConfigDrift
	for _, d := range report.Drifts {
		if d.IsCritical {
			critical = append(critical, d)
		} else {
			nonCritical = append(nonCritical, d)
		}
	}

	if len(critical) > 0 {
		sb.WriteString("## Critical Parameter Drifts\n\n")
		sb.WriteString("| Parameter | " + m.regionHeaders() + " |\n")
		sb.WriteString("|-----------|" + m.regionDashes() + "|\n")
		for _, d := range critical {
			sb.WriteString(fmt.Sprintf("| **%s** |", d.ParamName))
			m.mu.RLock()
			for overlordID := range m.lastSnapshots {
				val := d.Values[overlordID]
				if val == "" {
					val = "—"
				}
				sb.WriteString(fmt.Sprintf(" %s |", val))
			}
			m.mu.RUnlock()
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(nonCritical) > 0 {
		sb.WriteString("## Other Parameter Drifts\n\n")
		for _, d := range nonCritical {
			sb.WriteString(fmt.Sprintf("- **%s**: ", d.ParamName))
			parts := make([]string, 0, len(d.Values))
			for id, v := range d.Values {
				parts = append(parts, fmt.Sprintf("%s=%s", id, v))
			}
			sb.WriteString(strings.Join(parts, ", ") + "\n")
		}
	}

	return sb.String()
}

// LastDriftReport returns the most recent drift report, or nil if none.
func (m *ConfigAuditManager) LastDriftReport() *ConfigDriftReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.driftHistory) == 0 {
		return nil
	}
	return &m.driftHistory[0]
}

// StartPeriodicCollection runs config collection on the given interval.
func (m *ConfigAuditManager) StartPeriodicCollection(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.CollectFromAllRegions(ctx); err != nil {
				m.logger.Error("config collection failed", "error", err)
				continue
			}
			report := m.DetectDrift()
			if len(report.Drifts) > 0 {
				m.logger.Warn("config drift detected",
					"drifts", len(report.Drifts),
					"critical", countCritical(report.Drifts))
			}
		}
	}
}

func (m *ConfigAuditManager) regionHeaders() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var parts []string
	for id := range m.lastSnapshots {
		parts = append(parts, id)
	}
	sort.Strings(parts)
	return strings.Join(parts, " | ")
}

func (m *ConfigAuditManager) regionDashes() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var parts []string
	for range m.lastSnapshots {
		parts = append(parts, "---")
	}
	return strings.Join(parts, "|")
}

func hasDrift(values map[string]string) bool {
	var first string
	firstSet := false
	for _, v := range values {
		if !firstSet {
			first = v
			firstSet = true
			continue
		}
		if v != first {
			return true
		}
	}
	return false
}

func countCritical(drifts []ConfigDrift) int {
	n := 0
	for _, d := range drifts {
		if d.IsCritical {
			n++
		}
	}
	return n
}
