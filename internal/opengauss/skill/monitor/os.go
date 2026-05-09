/*-------------------------------------------------------------------------
 *
 * os.go
 *	  OSSkill shows OS-level metrics for the OpenGauss host.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/os.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// OSSkill shows OS-level metrics for the OpenGauss host.
//
// Data source priority:
//  1. DB-side OG views (gs_os_run_info, gs_total_memory_detail) — these live
//     on the DB host and work regardless of where opendb is running. This
//     is the right answer for the common case of a remote DB.
//  2. Local /proc fallback — only when opendb is co-located with the DB
//     (loopback connection) AND DB-side queries failed.
//  3. Clear message if neither works — no more silent "only available on
//     Linux" when the caller runs on macOS talking to a remote Linux OG.
type OSSkill struct {
	driver   db.Driver
	connHost string
}

// NewOSSkill creates an OSSkill. connHost tells us whether opendb is
// running on the DB host (loopback) or talking over the network.
func NewOSSkill(driver db.Driver, connHost string) *OSSkill {
	return &OSSkill{driver: driver, connHost: connHost}
}

func (s *OSSkill) Name() string                       { return "os" }
func (s *OSSkill) Description() string                { return "OS 资源概览（远端 DB 宿主）" }
func (s *OSSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *OSSkill) Validate(_ skill.Params) error      { return nil }

func (s *OSSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/os", Examples: []string{"/os"}}
}

func (s *OSSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "os",
		Description: "Show OS-level metrics for the OG host: load, memory, CPU. Uses gs_os_run_info when available, falls back to local /proc on loopback.",
	}
}

func (s *OSSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	sections := []format.PanelSection{}

	// Try OG-native views first — they work regardless of client OS.
	if lines, err := collectOGHostMetrics(ctx, s.driver); err == nil && len(lines) > 0 {
		sections = append(sections, format.PanelSection{
			Header: fmt.Sprintf("OG Host Metrics (via %s)", s.hostLabel()),
			Lines:  lines,
		})
	}

	// Fall back to /proc only if opendb is on the DB host.
	if len(sections) == 0 && osIsLoopback(s.connHost) && runtime.GOOS == "linux" {
		sections = append(sections, collectProcSections()...)
	}

	if len(sections) == 0 {
		return &skill.Result{
			Type: skill.ResultText,
			Rendered: fmt.Sprintf(
				"/os 无法采集 OS 指标：\n"+
					"  · 当前连接 host = %s（%s）\n"+
					"  · 客户端 OS = %s\n"+
					"  · OG 端 pv_os_run_info() 视图查询失败或不可用\n"+
					"\n建议：\n"+
					"  · 把 opendb 部署到 DB 宿主机上（loopback 连接）\n"+
					"  · 或在 DB 上授予 MONADMIN/SYSADMIN 权限访问 pv_os_run_info()",
				s.hostLabel(), locOrRemote(s.connHost), runtime.GOOS),
			Summary: "/os unavailable (remote client, DB-side view inaccessible)",
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

func (s *OSSkill) hostLabel() string {
	if s.connHost == "" {
		return "unknown"
	}
	return s.connHost
}

// collectOGHostMetrics reads OG's server-side OS view. gs_os_run_info
// returns name/value pairs (run_time, cpus, loadavg, free_mem, total_mem).
// Returns nil, nil when OG does not expose the view in this build.
func collectOGHostMetrics(ctx context.Context, driver db.Driver) ([]string, error) {
	if driver == nil {
		return nil, fmt.Errorf("driver nil")
	}
	// pg_sys_memory_info() / gs_os_run_info() are OG-specific. Try both.
	// Keep the query defensive — if the function is absent the Query errors
	// out and we return err so the caller falls back.
	// OG's actual function is pv_os_run_info() (PV = "per-view" prefix used
	// by OG's monitoring functions). Not gs_os_run_info despite the naming
	// convention of other gs_* views. Must call as a set-returning function.
	res, err := driver.Query(ctx, `SELECT name, value FROM pv_os_run_info()`)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Rows) == 0 {
		return nil, fmt.Errorf("gs_os_run_info returned 0 rows")
	}
	lines := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-20s : %v", row[0], row[1]))
	}
	return lines, nil
}

// collectProcSections returns the original /proc-based panels. Only called
// when opendb runs on the DB host.
func collectProcSections() []format.PanelSection {
	var sections []format.PanelSection

	if loadLines, err := readOSLoadAvg(); err == nil {
		sections = append(sections, format.PanelSection{Header: "Load Average", Lines: loadLines})
	}
	if memLines, err := readOSMemInfo(); err == nil {
		sections = append(sections, format.PanelSection{Header: "Memory", Lines: memLines})
	}
	if cpuLines, err := readOSCPUInfo(); err == nil {
		sections = append(sections, format.PanelSection{Header: "CPU", Lines: cpuLines})
	}
	return sections
}

// osIsLoopback reports whether the connection host is the local machine.
// Matches the behavior trace.IsLocal uses so /os and /trace agree.
func osIsLoopback(host string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.String() == host {
			return true
		}
	}
	return false
}

func locOrRemote(host string) string {
	if osIsLoopback(host) {
		return "local"
	}
	return "remote"
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
