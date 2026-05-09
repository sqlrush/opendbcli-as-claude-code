/*-------------------------------------------------------------------------
 *
 * blocktree.go
 *	  BlockTreeSkill shows the blocking chain tree (blocker -> blocked
 *	  hierarchy).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/blocktree.go
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

// blocktreeSQL queries all blocking relationships from InnoDB lock waits.
// Each row is one blocker-blocked pair with wait time.
const blocktreeSQL = `SELECT
  b.PROCESSLIST_ID AS blocker_pid,
  b.PROCESSLIST_USER AS blocker_user,
  LEFT(b.PROCESSLIST_INFO, 80) AS blocker_sql,
  r.PROCESSLIST_ID AS blocked_pid,
  r.PROCESSLIST_USER AS blocked_user,
  LEFT(r.PROCESSLIST_INFO, 80) AS blocked_sql,
  TIMESTAMPDIFF(SECOND, r.PROCESSLIST_TIME, NOW()) AS wait_seconds,
  wl.LOCK_TYPE AS wait_lock_type
FROM performance_schema.data_lock_waits w
JOIN performance_schema.threads r
  ON r.THREAD_ID = w.REQUESTING_THREAD_ID
JOIN performance_schema.threads b
  ON b.THREAD_ID = w.BLOCKING_THREAD_ID
LEFT JOIN performance_schema.data_locks wl
  ON wl.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
ORDER BY b.PROCESSLIST_ID, r.PROCESSLIST_ID`

// blockNode represents one session in the blocking tree.
type blockNode struct {
	ID       string
	User     string
	SQL      string
	WaitTime string
	LockType string
	Children []*blockNode
}

// BlockTreeSkill shows the blocking chain tree (blocker -> blocked hierarchy).
type BlockTreeSkill struct {
	driver db.Driver
}

// NewBlockTreeSkill creates a BlockTreeSkill backed by the given driver.
func NewBlockTreeSkill(driver db.Driver) *BlockTreeSkill {
	return &BlockTreeSkill{driver: driver}
}

func (s *BlockTreeSkill) Name() string                       { return "blocktree" }
func (s *BlockTreeSkill) Description() string                { return "锁等待阻塞链" }
func (s *BlockTreeSkill) Category() string                   { return "monitor" }
func (s *BlockTreeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *BlockTreeSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/blocktree", Examples: []string{"/blocktree"}}
}

func (s *BlockTreeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "blocktree",
		Description: "Show InnoDB lock wait blocking chain tree with blocker and blocked threads",
	}
}

func (s *BlockTreeSkill) Validate(_ skill.Params) error { return nil }

func (s *BlockTreeSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, blocktreeSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无锁等待阻塞链",
			Summary:  "no blocking chains",
		}, nil
	}

	roots, totalVictims := mysqlBuildTree(result.Rows)
	rendered := renderMySQLBlockTree(roots, totalVictims)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d chain(s), %d blocked session(s)", len(roots), totalVictims),
	}, nil
}

// mysqlBuildTree builds a tree from flat blocker-blocked pairs.
// Columns: blocker_pid(0), blocker_user(1), blocker_sql(2),
//          blocked_pid(3), blocked_user(4), blocked_sql(5),
//          wait_seconds(6), wait_lock_type(7).
func mysqlBuildTree(rows [][]interface{}) ([]*blockNode, int) {
	nodeByID := make(map[string]*blockNode)
	blockedSet := make(map[string]bool)

	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		blockerID := rowStr(row, 0)
		blockedID := rowStr(row, 3)

		// Ensure blocker node exists.
		if _, ok := nodeByID[blockerID]; !ok {
			nodeByID[blockerID] = &blockNode{
				ID:   blockerID,
				User: rowStr(row, 1),
				SQL:  rowStr(row, 2),
			}
		}

		// Ensure blocked node exists.
		if _, ok := nodeByID[blockedID]; !ok {
			nodeByID[blockedID] = &blockNode{
				ID:   blockedID,
				User: rowStr(row, 4),
				SQL:  rowStr(row, 5),
			}
		}

		blocked := nodeByID[blockedID]
		waitSec := rowInt(row, 6)
		if waitSec > 0 {
			blocked.WaitTime = fmt.Sprintf("%ds", waitSec)
		}
		blocked.LockType = rowStr(row, 7)

		// Add blocked as child of blocker (avoid duplicates).
		blocker := nodeByID[blockerID]
		if !hasChild(blocker, blockedID) {
			blocker.Children = append(blocker.Children, blocked)
		}
		blockedSet[blockedID] = true
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

func hasChild(parent *blockNode, childID string) bool {
	for _, c := range parent.Children {
		if c.ID == childID {
			return true
		}
	}
	return false
}

func renderMySQLBlockTree(roots []*blockNode, totalVictims int) string {
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
		btRenderNode(&b, root, "", true, true)
	}

	if len(roots) > 0 {
		b.WriteString(fmt.Sprintf("\n提示: /kill %s 终止根阻塞者", roots[0].ID))
	}

	return b.String()
}

// btRenderNode recursively renders a tree node with box-drawing connectors.
func btRenderNode(b *strings.Builder, n *blockNode, prefix string, isLast bool, isRoot bool) {
	connector := "├─"
	if isLast {
		connector = "└─"
	}
	if isRoot {
		connector = "🔒"
	}

	if isRoot {
		fmt.Fprintf(b, "%s%s ID %s (%s)", prefix, connector, n.ID, safeUser(n.User))
		if n.SQL != "" {
			fmt.Fprintf(b, " — %s", btTrunc(n.SQL, 60))
		}
	} else {
		fmt.Fprintf(b, "%s%s ID %s (%s)", prefix, connector, n.ID, safeUser(n.User))
		if n.WaitTime != "" {
			fmt.Fprintf(b, " — 等待 %s", n.WaitTime)
		}
		if n.SQL != "" {
			fmt.Fprintf(b, " — %s", btTrunc(n.SQL, 50))
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
		btRenderNode(b, child, childPrefix, i == len(n.Children)-1, false)
	}
}

func safeUser(u string) string {
	if u == "" {
		return "-"
	}
	return u
}

func btTrunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
