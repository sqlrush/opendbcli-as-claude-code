/*-------------------------------------------------------------------------
 *
 * types.go
 *	  DiagnoseMode represents the three diagnosis modes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/agent/types.go
 *
 *-------------------------------------------------------------------------
 */
package agent

// DiagnoseMode represents the three diagnosis modes.
type DiagnoseMode string

const (
	ModePlaybook DiagnoseMode = "playbook"
	ModeAssist   DiagnoseMode = "assist"
	ModeAuto     DiagnoseMode = "auto"
)

const DefaultMaxRounds = 20

func (m DiagnoseMode) MaxRounds() int {
	switch m {
	case ModePlaybook:
		return 1
	default:
		return DefaultMaxRounds
	}
}

func (m DiagnoseMode) IsValid() bool {
	switch m {
	case ModePlaybook, ModeAssist, ModeAuto:
		return true
	default:
		return false
	}
}

type RoundInfo struct {
	Round   int
	Summary string
}

type OnRoundFunc func(info RoundInfo)
type OnStreamFunc func(delta string)
