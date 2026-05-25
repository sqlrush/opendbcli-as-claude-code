/*-------------------------------------------------------------------------
 *
 * blocktree.go
 *	  BlockTreeSkill shows blocking chain using pg_blocking_pids().
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/blocktree.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// blocktreeSQL queries all sessions involved in blocking relationships.
// Returns both blockers and blocked sessions with wait info.
// Columns: blocked_pid(0), blocked_user(1), blocked_query(2),
//          blocker_pid(3), blocker_user(4), blocker_query(5),
//          wait_event_type(6), wait_event(7), wait_seconds(8).
// P04: Enhanced blocktree SQL with lock mode annotation.
const blocktreeSQL = `SELECT
  blocked.pid AS blocked_pid,
  blocked.usename AS blocked_user,
  LEFT(blocked.query, 80) AS blocked_query,
  blocker.pid AS blocker_pid,
  blocker.usename AS blocker_user,
  LEFT(blocker.query, 80) AS blocker_query,
  blocked.wait_event_type,
  blocked.wait_event,
  EXTRACT(EPOCH FROM (now() - blocked.state_change))::int AS wait_seconds,
  COALESCE((SELECT string_agg(DISTINCT l.mode, ', ')
    FROM pg_locks l WHERE l.pid = blocker.pid AND l.granted = true
      AND l.relation IS NOT NULL), '') AS blocker_lock_mode
FROM pg_stat_activity blocked
JOIN LATERAL unnest(pg_blocking_pids(blocked.pid)) AS bp(pid) ON true
JOIN pg_stat_activity blocker ON blocker.pid = bp.pid
WHERE blocked.backend_type = 'client backend'
  AND cardinality(pg_blocking_pids(blocked.pid)) > 0
ORDER BY blocker.pid, blocked.pid`

// blockNode represents one session in the blocking tree.
type blockNode struct {
	ID        string
	User      string
	SQL       string
	WaitTime  string
	WaitEvent string
	LockMode  string // P04: lock mode held by blocker
	Children  []*blockNode
}

// BlockTreeSkill shows blocking chain using pg_blocking_pids().
type BlockTreeSkill struct{ driver db.Driver }

// NewBlockTreeSkill creates a BlockTreeSkill backed by the given driver.
func NewBlockTreeSkill(driver db.Driver) *BlockTreeSkill {
	return &BlockTreeSkill{driver: driver}
}

func (s *BlockTreeSkill) Name() string                       { return "blocktree" }
func (s *BlockTreeSkill) Description() string                { return "锁等待阻塞链" }
func (s *BlockTreeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *BlockTreeSkill) Validate(_ skill.Params) error      { return nil }
func (s *BlockTreeSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/blocktree"} }
func (s *BlockTreeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "blocktree", Description: "Show blocking chain tree using pg_blocking_pids()"}
}

func (s *BlockTreeSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, blocktreeSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无阻塞链",
			Summary:  "no blocking chains",
		}, nil
	}

	roots, totalVictims := pgBuildTree(result.Rows)
	rendered := renderPGBlockTree(roots, totalVictims)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d chain(s), %d blocked session(s)", len(roots), totalVictims),
	}, nil
}

// pgBuildTree builds a tree from flat blocker-blocked pairs.
func pgBuildTree(rows [][]interface{}) ([]*blockNode, int) {
	nodeByID := make(map[string]*blockNode)
	blockedSet := make(map[string]bool)

	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		blockedPID := rowStr(row, 0)
		blockerPID := rowStr(row, 3)

		// Ensure blocker node exists.
		if _, ok := nodeByID[blockerPID]; !ok {
			lockMode := ""
			if len(row) > 10 {
				lockMode = rowStr(row, 10)
			}
			nodeByID[blockerPID] = &blockNode{
				ID:       blockerPID,
				User:     rowStr(row, 4),
				SQL:      rowStr(row, 5),
				LockMode: lockMode,
			}
		}

		// Ensure blocked node exists.
		if _, ok := nodeByID[blockedPID]; !ok {
			nodeByID[blockedPID] = &blockNode{
				ID:   blockedPID,
				User: rowStr(row, 1),
				SQL:  rowStr(row, 2),
			}
		}

		blocked := nodeByID[blockedPID]
		waitSec := rowInt(row, 8)
		if waitSec > 0 {
			blocked.WaitTime = formatWait(waitSec)
		}
		waitEvt := rowStr(row, 7)
		if waitEvt != "" {
			blocked.WaitEvent = waitEvt
		}

		// Add blocked as child of blocker (avoid duplicates).
		blocker := nodeByID[blockerPID]
		if !pgHasChild(blocker, blockedPID) {
			blocker.Children = append(blocker.Children, blocked)
		}
		blockedSet[blockedPID] = true
	}

	// Roots are nodes that are never blocked by anyone.
	var roots []*blockNode
	totalVictims := 0
	for id, node := range nodeByID {
		if !blockedSet[id] {
			roots = append(roots, node)
		} else {
			totalVictims++
		}
	}

	return roots, totalVictims
}

func pgHasChild(parent *blockNode, childID string) bool {
	for _, c := range parent.Children {
		if c.ID == childID {
			return true
		}
	}
	return false
}

func renderPGBlockTree(roots []*blockNode, totalVictims int) string {
	var b strings.Builder

	if len(roots) == 0 && totalVictims == 0 {
		return "当前无阻塞链"
	}
	if len(roots) == 0 && totalVictims > 0 {
		return fmt.Sprintf("当前无阻塞链 (检测到 %d 个等待会话, 但非阻塞关系, 可能是锁争用/资源等待)", totalVictims)
	}

	b.WriteString(fmt.Sprintf("阻塞链: %d 条, 共 %d 个被阻塞会话\n\n", len(roots), totalVictims))

	for i, root := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		pgRenderNode(&b, root, "", true, true)
	}

	if len(roots) > 0 {
		b.WriteString(fmt.Sprintf("\n提示: /kill %s 终止根阻塞者", roots[0].ID))
	}

	return b.String()
}

// pgRenderNode recursively renders a tree node with box-drawing connectors.
func pgRenderNode(b *strings.Builder, n *blockNode, prefix string, isLast bool, isRoot bool) {
	connector := "├─"
	if isLast {
		connector = "└─"
	}
	if isRoot {
		connector = "🔒"
	}

	if isRoot {
		fmt.Fprintf(b, "%s%s PID %s (%s)", prefix, connector, n.ID, pgSafeUser(n.User))
		if n.LockMode != "" {
			if strings.Contains(n.LockMode, "AccessExclusiveLock") {
				fmt.Fprintf(b, " ⚠️ %s", n.LockMode)
			} else {
				fmt.Fprintf(b, " [%s]", n.LockMode)
			}
		}
		if n.SQL != "" {
			fmt.Fprintf(b, " — %s", pgTrunc(n.SQL, 60))
		}
	} else {
		fmt.Fprintf(b, "%s%s PID %s (%s)", prefix, connector, n.ID, pgSafeUser(n.User))
		if n.WaitTime != "" {
			fmt.Fprintf(b, " — 等待 %s", n.WaitTime)
		}
		if n.SQL != "" {
			fmt.Fprintf(b, " — %s", pgTrunc(n.SQL, 50))
		}
	}
	b.WriteString("\n")

	childPrefix := prefix
	if !isRoot {
		if isLast {
			childPrefix += "   "
		} else {
			childPrefix += "│  "
		}
	}

	for i, child := range n.Children {
		pgRenderNode(b, child, childPrefix, i == len(n.Children)-1, false)
	}
}

func pgSafeUser(u string) string {
	if u == "" {
		return "-"
	}
	return u
}

func pgTrunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}

func formatWait(seconds int) string {
	if seconds >= 60 {
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}
