/*-------------------------------------------------------------------------
 *
 * types.go
 *	  IsMaxTurnsExceeded returns true if the analysis text contains the
 *	  marker.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/agent/types.go
 *
 *-------------------------------------------------------------------------
 */
package agent

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

// MaxTurnsNote is appended when the agent loop exceeds maximum turns.
// Used by AutonomousStrategy to detect when to fall back to GuidedStrategy.
const MaxTurnsNote = "[Note: maximum turns exceeded]"

// IsMaxTurnsExceeded returns true if the analysis text contains the marker.
func IsMaxTurnsExceeded(text string) bool {
	return len(text) > 0 && containsMarker(text, MaxTurnsNote)
}

func containsMarker(s, marker string) bool {
	for i := 0; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}
