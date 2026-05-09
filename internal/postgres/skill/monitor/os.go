/*-------------------------------------------------------------------------
 *
 * os.go
 *	  OSSkill shows OS-level metrics (Linux: /proc/loadavg,
 *	  /proc/meminfo, /proc/stat).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/os.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// OSSkill shows OS-level metrics (Linux: /proc/loadavg, /proc/meminfo, /proc/stat).
type OSSkill struct {
	driver db.Driver
}

// NewOSSkill creates an OSSkill.
func NewOSSkill(driver db.Driver) *OSSkill {
	return &OSSkill{driver: driver}
}

func (s *OSSkill) Name() string                       { return "os" }
func (s *OSSkill) Description() string                { return "OS 资源概览" }
func (s *OSSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *OSSkill) Validate(_ skill.Params) error      { return nil }

func (s *OSSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/os", Examples: []string{"/os"}}
}

func (s *OSSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "os",
		Description: "Show OS-level metrics: load average, memory, CPU (Linux only)",
	}
}

// P11: PG data directory size query for key subdirectories.
const pgDataDirSQL = `SELECT
  current_setting('data_directory') AS data_dir,
  pg_size_pretty(pg_database_size(current_database())) AS db_size,
  (SELECT pg_size_pretty(SUM(size) ::bigint) FROM pg_ls_waldir()) AS wal_size`

func (s *OSSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	if runtime.GOOS != "linux" {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("OS metrics only available on Linux (current: %s)", runtime.GOOS),
			Summary:  "unsupported OS",
		}, nil
	}

	sections := []format.PanelSection{}

	// Load average
	if loadLines, err := readOSLoadAvg(); err == nil {
		sections = append(sections, format.PanelSection{
			Header: "Load Average",
			Lines:  loadLines,
		})
	}

	// Memory
	if memLines, err := readOSMemInfo(); err == nil {
		sections = append(sections, format.PanelSection{
			Header: "Memory",
			Lines:  memLines,
		})
	}

	// CPU
	if cpuLines, err := readOSCPUInfo(); err == nil {
		sections = append(sections, format.PanelSection{
			Header: "CPU",
			Lines:  cpuLines,
		})
	}

	// P11: PG data directory sizes
	if pgLines := s.readPGDataDirSizes(ctx); len(pgLines) > 0 {
		sections = append(sections, format.PanelSection{
			Header: "PG Data Directory",
			Lines:  pgLines,
		})
	}

	if len(sections) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无法读取 OS 指标",
			Summary:  "failed to read OS metrics",
		}, nil
	}

	rendered := format.Panel("OS Metrics", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "OS metrics",
	}, nil
}

func (s *OSSkill) readPGDataDirSizes(ctx context.Context) []string {
	result, err := s.driver.Query(ctx, pgDataDirSQL)
	if err != nil || len(result.Rows) == 0 {
		return nil
	}
	row := result.Rows[0]
	lines := []string{
		fmt.Sprintf("Data Directory  : %v", row[0]),
		fmt.Sprintf("Database Size   : %v", row[1]),
		fmt.Sprintf("WAL Size        : %v", row[2]),
	}
	return lines
}

func readOSLoadAvg() ([]string, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, fmt.Errorf("unexpected loadavg format")
	}
	return []string{
		fmt.Sprintf("1 min / 5 min / 15 min : %s / %s / %s", fields[0], fields[1], fields[2]),
	}, nil
}

func readOSMemInfo() ([]string, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info := make(map[string]float64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)
		var val float64
		fmt.Sscanf(valStr, "%f", &val)
		info[key] = val
	}

	totalKB := info["MemTotal"]
	freeKB := info["MemFree"]
	availKB := info["MemAvailable"]
	buffersKB := info["Buffers"]
	cachedKB := info["Cached"]
	swapTotalKB := info["SwapTotal"]
	swapFreeKB := info["SwapFree"]

	totalMB := totalKB / 1024
	usedMB := (totalKB - freeKB - buffersKB - cachedKB) / 1024
	availMB := availKB / 1024
	usedPct := safePct(totalKB-availKB, totalKB)
	swapUsedMB := (swapTotalKB - swapFreeKB) / 1024
	swapTotalMB := swapTotalKB / 1024

	lines := []string{
		fmt.Sprintf("Total           : %.0f MB", totalMB),
		fmt.Sprintf("Used            : %.0f MB (%.1f%%)", usedMB, usedPct),
		fmt.Sprintf("Available       : %.0f MB", availMB),
		fmt.Sprintf("                  %s", format.ProgressBar(usedPct, 30)),
	}

	if swapTotalKB > 0 {
		swapPct := safePct(swapTotalKB-swapFreeKB, swapTotalKB)
		lines = append(lines, fmt.Sprintf("Swap            : %.0f / %.0f MB (%.1f%%)", swapUsedMB, swapTotalMB, swapPct))
	}

	return lines, nil
}

func readOSCPUInfo() ([]string, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			return []string{
				fmt.Sprintf("Cores           : %d", runtime.NumCPU()),
				fmt.Sprintf("User / System   : %s / %s (jiffies)", fields[1], fields[3]),
				fmt.Sprintf("Idle            : %s (jiffies)", fields[4]),
			}, nil
		}
	}
	return nil, fmt.Errorf("cpu line not found in /proc/stat")
}
