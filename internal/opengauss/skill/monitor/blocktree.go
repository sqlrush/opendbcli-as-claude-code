/*-------------------------------------------------------------------------
 *
 * blocktree.go
 *	  BlockTreeSkill shows blocking chain using pg_locks self-join.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/blocktree.go
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

// blocktreeSQL uses pg_locks self-join instead of pg_blocking_pids()
// which is not available in OpenGauss.
// Columns: blocked_pid(0), blocked_user(1), blocked_query(2),
//          blocker_pid(3), blocker_user(4), blocker_query(5),
//          wait_type(6), wait_event(7).
//
// CRITICAL: kl.granted MUST be required. Without it, when N sessions wait
// on the same transactionid (1 holder + N-1 waiters, e.g., 30 concurrent
// UPDATE WHERE id=1), the join produces N×(N-1) rows where every waiter
// is reported as "blocker" of every other waiter, forming cycles in the
// blocking graph that crash ogRenderNode (OOM, 30+ GB RSS observed).
const blocktreeSQL = `SELECT
  blocked.pid AS blocked_pid,
  blocked.usename AS blocked_user,
  LEFT(blocked.query, 80) AS blocked_query,
  blocker.pid AS blocker_pid,
  blocker.usename AS blocker_user,
  LEFT(blocker.query, 80) AS blocker_query,
  CASE WHEN blocked.waiting THEN 'Lock' ELSE NULL END AS wait_type,
  CASE WHEN blocked.waiting THEN 'lock_wait' WHEN blocked.enqueue != '' THEN blocked.enqueue ELSE NULL END AS wait_event
FROM pg_locks bl
JOIN pg_stat_activity blocked ON blocked.pid = bl.pid
JOIN pg_locks kl ON kl.transactionid = bl.transactionid AND kl.pid != bl.pid AND kl.granted
JOIN pg_stat_activity blocker ON blocker.pid = kl.pid
WHERE NOT bl.granted`

// blockNode represents one session in the blocking tree.
type blockNode struct {
	ID        string
	User      string
	SQL       string
	WaitTime  string
	WaitEvent string
	Children  []*blockNode
}

// BlockTreeSkill shows blocking chain using pg_locks self-join.
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
	return skill.ToolDef{Name: "blocktree", Description: "Show blocking chain tree using pg_locks self-join"}
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

	roots, totalVictims := ogBuildTree(result.Rows)
	rendered := renderOGBlockTree(roots, totalVictims)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d chain(s), %d blocked session(s)", len(roots), totalVictims),
	}, nil
}

// ogBuildTree builds a tree from flat blocker-blocked pairs.
func ogBuildTree(rows [][]interface{}) ([]*blockNode, int) {
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
			nodeByID[blockerPID] = &blockNode{
				ID:   blockerPID,
				User: rowStr(row, 4),
				SQL:  rowStr(row, 5),
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
		waitEvt := rowStr(row, 7)
		if waitEvt != "" {
			blocked.WaitEvent = waitEvt
		}

		// Add blocked as child of blocker (avoid duplicates).
		blocker := nodeByID[blockerPID]
		if !ogHasChild(blocker, blockedPID) {
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

func ogHasChild(parent *blockNode, childID string) bool {
	for _, c := range parent.Children {
		if c.ID == childID {
			return true
		}
	}
	return false
}

func renderOGBlockTree(roots []*blockNode, totalVictims int) string {
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
		ogRenderNode(&b, root, "", true, true, map[string]bool{})
	}

	if len(roots) > 0 {
		b.WriteString(fmt.Sprintf("\n提示: /kill %s 终止根阻塞者", roots[0].ID))
	}

	return b.String()
}

// ogRenderNode recursively renders a tree node with box-drawing connectors.
// visited is required to break cycles — defensive guard if upstream graph
// builds a cyclic structure (would otherwise OOM via unbounded recursion).
func ogRenderNode(b *strings.Builder, n *blockNode, prefix string, isLast bool, isRoot bool, visited map[string]bool) {
	if visited[n.ID] {
		fmt.Fprintf(b, "%s↻ PID %s (cycle detected, skipping)\n", prefix, n.ID)
		return
	}
	visited[n.ID] = true

	connector := "├─"
	if isLast {
		connector = "└─"
	}
	if isRoot {
		connector = "🔒"
	}

	if isRoot {
		fmt.Fprintf(b, "%s%s PID %s (%s)", prefix, connector, n.ID, ogSafeUser(n.User))
		if n.SQL != "" {
			fmt.Fprintf(b, " — %s", ogTrunc(n.SQL, 60))
		}
	} else {
		fmt.Fprintf(b, "%s%s PID %s (%s)", prefix, connector, n.ID, ogSafeUser(n.User))
		if n.WaitEvent != "" {
			fmt.Fprintf(b, " — %s", n.WaitEvent)
		}
		if n.SQL != "" {
			fmt.Fprintf(b, " — %s", ogTrunc(n.SQL, 50))
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
		ogRenderNode(b, child, childPrefix, i == len(n.Children)-1, false, visited)
	}
}

func ogSafeUser(u string) string {
	if u == "" {
		return "-"
	}
	return u
}

func ogTrunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
