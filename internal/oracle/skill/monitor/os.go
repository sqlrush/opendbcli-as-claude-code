/*-------------------------------------------------------------------------
 *
 * os.go
 *	  OSSkill shows OS-level metrics from v$osstat.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/os.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const osStatSQL = `SELECT stat_name, value
FROM v$osstat
WHERE stat_name IN (
    'NUM_CPUS', 'NUM_CPU_CORES', 'NUM_CPU_SOCKETS',
    'IDLE_TIME', 'BUSY_TIME', 'USER_TIME', 'SYS_TIME', 'IOWAIT_TIME',
    'PHYSICAL_MEMORY_BYTES', 'FREE_MEMORY_BYTES',
    'LOAD', 'VM_IN_BYTES', 'VM_OUT_BYTES'
)
ORDER BY stat_name`

const osProcSQL = `SELECT pid, program,
       ROUND(pga_used_mem/1048576, 1) AS pga_used_mb,
       ROUND(pga_alloc_mem/1048576, 1) AS pga_alloc_mb,
       ROUND(pga_max_mem/1048576, 1) AS pga_max_mb
FROM v$process
WHERE background IS NULL
ORDER BY pga_used_mem DESC
FETCH FIRST 10 ROWS ONLY`

// OSSkill shows OS-level metrics from v$osstat.
type OSSkill struct {
	driver db.Driver
}

// NewOSSkill creates an OSSkill backed by the given driver.
func NewOSSkill(driver db.Driver) *OSSkill {
	return &OSSkill{driver: driver}
}

func (s *OSSkill) Name() string                        { return "os" }
func (s *OSSkill) Description() string                 { return "Show OS-level metrics (CPU, memory, I/O)" }
func (s *OSSkill) SecurityLevel() skill.SecurityLevel   { return skill.LevelReadOnly }

func (s *OSSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "os",
		Description: "Show OS-level metrics from v$osstat",
	}
}

func (s *OSSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "os",
		Aliases:  []string{"osstat"},
		Usage:    "/os [proc]",
		Examples: []string{"/os", "/os proc"},
	}
}

func (s *OSSkill) Validate(_ skill.Params) error { return nil }

func (s *OSSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if strings.EqualFold(args, "proc") {
		return s.showProcesses(ctx)
	}
	return s.showOSStat(ctx)
}

func (s *OSSkill) showOSStat(ctx context.Context) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, osStatSQL)
	if err != nil {
		return nil, fmt.Errorf("querying v$osstat: %w", err)
	}

	stats := make(map[string]int64)
	for _, row := range qr.Rows {
		name := rowStr(row, 0)
		val := rowInt64(row, 1)
		stats[name] = val
	}

	var b strings.Builder
	b.WriteString("OS 指标 (v$osstat):\n\n")

	// CPU section
	b.WriteString("  CPU:\n")
	fmt.Fprintf(&b, "    CPUs: %d  Cores: %d  Sockets: %d\n",
		stats["NUM_CPUS"], stats["NUM_CPU_CORES"], stats["NUM_CPU_SOCKETS"])

	totalCPU := stats["BUSY_TIME"] + stats["IDLE_TIME"]
	if totalCPU > 0 {
		busyPct := float64(stats["BUSY_TIME"]) * 100 / float64(totalCPU)
		userPct := float64(stats["USER_TIME"]) * 100 / float64(totalCPU)
		sysPct := float64(stats["SYS_TIME"]) * 100 / float64(totalCPU)
		ioPct := float64(stats["IOWAIT_TIME"]) * 100 / float64(totalCPU)
		fmt.Fprintf(&b, "    Busy: %.1f%%  User: %.1f%%  Sys: %.1f%%  IO Wait: %.1f%%\n",
			busyPct, userPct, sysPct, ioPct)
	}

	if stats["LOAD"] > 0 {
		loadAvg := float64(stats["LOAD"]) / 100
		fmt.Fprintf(&b, "    Load Average: %.2f\n", loadAvg)
	}

	// Memory section
	b.WriteString("\n  Memory:\n")
	physMB := stats["PHYSICAL_MEMORY_BYTES"] / 1048576
	freeMB := stats["FREE_MEMORY_BYTES"] / 1048576
	usedMB := physMB - freeMB
	var usedPct float64
	if physMB > 0 {
		usedPct = float64(usedMB) * 100 / float64(physMB)
	}
	fmt.Fprintf(&b, "    Physical: %d MB  Used: %d MB (%.1f%%)  Free: %d MB\n",
		physMB, usedMB, usedPct, freeMB)

	// VM (swap)
	vmIn := stats["VM_IN_BYTES"] / 1048576
	vmOut := stats["VM_OUT_BYTES"] / 1048576
	if vmIn > 0 || vmOut > 0 {
		fmt.Fprintf(&b, "    Swap In: %d MB  Swap Out: %d MB\n", vmIn, vmOut)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("cpu_busy=%.1f%% mem_used=%.1f%%",
			float64(stats["BUSY_TIME"])*100/float64(max64(totalCPU, 1)), usedPct),
	}, nil
}

func (s *OSSkill) showProcesses(ctx context.Context) (*skill.Result, error) {
	qr, err := s.driver.Query(ctx, osProcSQL)
	if err != nil {
		return nil, fmt.Errorf("querying v$process: %w", err)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     qr,
		Rendered: "操作系统进程 (PGA Top 10)",
		Summary:  fmt.Sprintf("PGA 使用 Top %d 进程", len(qr.Rows)),
	}, nil
}

func rowInt64(row []any, idx int) int64 {
	if idx >= len(row) || row[idx] == nil {
		return 0
	}
	switch v := row[idx].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	default:
		// go-ora may return other numeric types; try fmt.Sprintf → parse.
		s := fmt.Sprintf("%v", v)
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
