/*-------------------------------------------------------------------------
 *
 * cmd.go
 *	  Package drone implements the Worker Agent (Drone/工蜂) daemon
 *	  mode. Autonomy Loop (sense→diagnose→act), Sentinel monitoring,
 *	  gRPC server, and local self-healing.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/cmd.go
 *
 *-------------------------------------------------------------------------
 */
// Package drone implements the Worker Agent (Drone/工蜂) daemon mode.
// Autonomy Loop (sense→diagnose→act), Sentinel monitoring, gRPC server, and local self-healing.
package drone

import (
	"fmt"
	"strings"
)

// Subcommand represents an agent subcommand (start/stop/status).
type Subcommand string

const (
	SubcmdStart  Subcommand = "start"
	SubcmdStop   Subcommand = "stop"
	SubcmdStatus Subcommand = "status"
)

// AgentArgs holds parsed agent command arguments.
type AgentArgs struct {
	Subcmd   Subcommand
	Role     string // "worker", "memory", "manager"
	Listen   string // gRPC listen address
	Overlord string // Overlord address (for worker role)
	DBType   string // database type
	DBConn   string // database connection string
	Web      string // Web UI listen address (for manager role)

	// LLM recording/replay for deterministic CI/CD testing.
	LLMRecord string // directory to write JSONL recordings (--llm-record)
	LLMReplay string // JSONL file to replay responses from (--llm-replay)
}

// ParseAgentArgs parses os.Args for "opendb agent <subcmd> [flags]".
// args should be os.Args[2:] (after "opendb" and "agent").
func ParseAgentArgs(args []string) (AgentArgs, error) {
	if len(args) == 0 {
		return AgentArgs{}, fmt.Errorf("usage: opendb agent <start|stop|status> [flags]")
	}

	subcmd := Subcommand(args[0])
	switch subcmd {
	case SubcmdStart, SubcmdStop, SubcmdStatus:
	default:
		return AgentArgs{}, fmt.Errorf("unknown agent subcommand: %s (use start, stop, or status)", args[0])
	}

	result := AgentArgs{
		Subcmd: subcmd,
		Role:   "worker",
		Listen: "0.0.0.0:9300",
	}

	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--role" && i+1 < len(args):
			i++
			result.Role = args[i]
		case strings.HasPrefix(args[i], "--role="):
			result.Role = strings.TrimPrefix(args[i], "--role=")
		case args[i] == "--listen" && i+1 < len(args):
			i++
			result.Listen = args[i]
		case strings.HasPrefix(args[i], "--listen="):
			result.Listen = strings.TrimPrefix(args[i], "--listen=")
		case args[i] == "--overlord" && i+1 < len(args):
			i++
			result.Overlord = args[i]
		case strings.HasPrefix(args[i], "--overlord="):
			result.Overlord = strings.TrimPrefix(args[i], "--overlord=")
		case args[i] == "--db-type" && i+1 < len(args):
			i++
			result.DBType = args[i]
		case strings.HasPrefix(args[i], "--db-type="):
			result.DBType = strings.TrimPrefix(args[i], "--db-type=")
		case args[i] == "--db-conn" && i+1 < len(args):
			i++
			result.DBConn = args[i]
		case strings.HasPrefix(args[i], "--db-conn="):
			result.DBConn = strings.TrimPrefix(args[i], "--db-conn=")
		case args[i] == "--web" && i+1 < len(args):
			i++
			result.Web = args[i]
		case strings.HasPrefix(args[i], "--web="):
			result.Web = strings.TrimPrefix(args[i], "--web=")
		case args[i] == "--llm-record" && i+1 < len(args):
			i++
			result.LLMRecord = args[i]
		case strings.HasPrefix(args[i], "--llm-record="):
			result.LLMRecord = strings.TrimPrefix(args[i], "--llm-record=")
		case args[i] == "--llm-replay" && i+1 < len(args):
			i++
			result.LLMReplay = args[i]
		case strings.HasPrefix(args[i], "--llm-replay="):
			result.LLMReplay = strings.TrimPrefix(args[i], "--llm-replay=")
		}
	}

	return result, nil
}
