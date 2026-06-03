/*-------------------------------------------------------------------------
 *
 * hostcheck.go
 *	  hostcheck — Provides IsLocal for the trace package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/hostcheck.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/osutil"
)

var processPatterns = map[string][]string{
	"mysql":     {"mysqld"},
	"postgres":  {"postgres", "postmaster"},
	"oracle":    {"ora_pmon"},
	"opengauss": {"gaussdb"},
}

func IsLocal(ctx context.Context, dbType string, connHost string) (int, error) {
	if !isLoopback(connHost) {
		return 0, fmt.Errorf("trace 功能需要 OpenDB 部署在数据库宿主机上 (当前连接: %s)", connHost)
	}
	pid, err := findDBProcess(dbType)
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func isLoopback(host string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.String() == host {
			return true
		}
	}
	return false
}

func findDBProcess(dbType string) (int, error) {
	patterns, ok := processPatterns[dbType]
	if !ok {
		return 0, fmt.Errorf("unsupported db type for trace: %s", dbType)
	}

	ctx := context.Background()
	out, err := osutil.Run(ctx, "ps", "aux")
	if err != nil {
		return 0, fmt.Errorf("执行 ps 失败: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		for _, pat := range patterns {
			if strings.Contains(line, pat) && !strings.Contains(line, "grep") {
				return parsePIDFromPsLine(line)
			}
		}
	}
	return 0, fmt.Errorf("未找到 %s 进程，请确认数据库正在运行", dbType)
}

func parsePIDFromPsLine(line string) (int, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected ps output: %s", line)
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("cannot parse PID from %q: %w", fields[1], err)
	}
	return pid, nil
}
