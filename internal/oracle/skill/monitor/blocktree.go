/*-------------------------------------------------------------------------
 *
 * blocktree.go
 *	  BlockTreeSkill shows hierarchical blocking chains.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/blocktree.go
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

// blockTreeSQL queries all sessions involved in blocking relationships.
// Includes the blocker and all blocked sessions with their wait info.
const blockTreeSQL = `SELECT
    s.sid,
    s.serial#,
    s.username,
    s.blocking_session,
    s.sql_id,
    s.event,
    s.wait_class,
    s.seconds_in_wait,
    s.status,
    SUBSTR(s.program, 1, 30) AS program,
    SUBSTR(q.sql_text, 1, 80) AS sql_text
FROM v$session s
LEFT JOIN v$sql q ON s.sql_id = q.sql_id AND q.child_number = 0
WHERE s.blocking_session IS NOT NULL
   OR s.sid IN (SELECT blocking_session FROM v$session WHERE blocking_session IS NOT NULL)
ORDER BY s.blocking_session NULLS FIRST, s.seconds_in_wait DESC`

// blockNode represents one session in the blocking tree.
type blockNode struct {
	SID            int
	Serial         int
	Username       string
	SQLID          string
	Event          string
	WaitClass      string
	SecondsInWait  int
	Status         string
	Program        string
	SQLText        string
	BlockingSession int // 0 = root blocker
	Children       []*blockNode
}

// BlockTreeSkill shows hierarchical blocking chains.
type BlockTreeSkill struct {
	driver db.Driver
}

// NewBlockTreeSkill creates a BlockTreeSkill backed by the given driver.
func NewBlockTreeSkill(driver db.Driver) *BlockTreeSkill {
	return &BlockTreeSkill{driver: driver}
}

func (s *BlockTreeSkill) Name() string        { return "blocktree" }
func (s *BlockTreeSkill) Description() string  { return "Show hierarchical blocking chains" }
func (s *BlockTreeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *BlockTreeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "blocktree",
		Description: "Show hierarchical blocking chains (blocker → victims)",
	}
}

func (s *BlockTreeSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "blocktree",
		Usage:   "/blocktree",
		Aliases: []string{"bt"},
	}
}

func (s *BlockTreeSkill) Validate(_ skill.Params) error { return nil }

func (s *BlockTreeSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, blockTreeSQL)
	if err != nil {
		return nil, fmt.Errorf("querying blocking tree: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无阻塞链.",
			Summary:  "无阻塞",
		}, nil
	}

	// Parse rows into nodes.
	nodes := parseBlockNodes(result.Rows)

	// Build tree.
	roots, totalVictims := buildBlockTree(nodes)

	// Render.
	rendered := renderBlockTree(roots, totalVictims)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d chain(s), %d blocked session(s)", len(roots), totalVictims),
	}, nil
}

func parseBlockNodes(rows [][]interface{}) []*blockNode {
	nodes := make([]*blockNode, 0, len(rows))
	for _, row := range rows {
		if len(row) < 11 {
			continue
		}
		n := &blockNode{
			SID:             rowInt(row, 0),
			Serial:          rowInt(row, 1),
			Username:        rowStr(row, 2),
			BlockingSession: rowInt(row, 3),
			SQLID:           rowStr(row, 4),
			Event:           rowStr(row, 5),
			WaitClass:       rowStr(row, 6),
			SecondsInWait:   rowInt(row, 7),
			Status:          rowStr(row, 8),
			Program:         rowStr(row, 9),
			SQLText:         rowStr(row, 10),
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// buildBlockTree organizes flat nodes into a tree structure.
// Returns root blockers and total victim count.
func buildBlockTree(nodes []*blockNode) ([]*blockNode, int) {
	byID := make(map[int]*blockNode, len(nodes))
	for _, n := range nodes {
		byID[n.SID] = n
	}

	var roots []*blockNode
	totalVictims := 0

	for _, n := range nodes {
		if n.BlockingSession == 0 {
			// Root blocker (not blocked by anyone).
			roots = append(roots, n)
		} else if parent, ok := byID[n.BlockingSession]; ok {
			parent.Children = append(parent.Children, n)
			totalVictims++
		} else {
			// Parent not in result set (e.g., already disconnected).
			// Show as root with note.
			roots = append(roots, n)
			totalVictims++
		}
	}

	return roots, totalVictims
}

func renderBlockTree(roots []*blockNode, totalVictims int) string {
	var b strings.Builder

	if len(roots) == 0 && totalVictims == 0 {
		return "当前无阻塞链 — 0 条"
	}
	if len(roots) == 0 && totalVictims > 0 {
		return fmt.Sprintf("当前无阻塞链 (检测到 %d 个等待会话, 但非阻塞关系, 可能是序列争用/资源等待)", totalVictims)
	}

	b.WriteString(fmt.Sprintf("阻塞链: %d 条, 共 %d 个被阻塞会话\n\n", len(roots), totalVictims))

	for i, root := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		renderNode(&b, root, "", true, true)
	}

	return b.String()
}

// renderNode recursively renders a tree node with box-drawing connectors.
func renderNode(b *strings.Builder, n *blockNode, prefix string, isLast bool, isRoot bool) {
	// Connector characters.
	connector := "├─"
	if isLast {
		connector = "└─"
	}
	if isRoot {
		connector = "🔒"
	}

	// Session info line.
	waitInfo := ""
	if n.Event != "" && n.BlockingSession != 0 {
		waitInfo = fmt.Sprintf(" [等待: %s %ds]", btTrunc(n.Event, 35), n.SecondsInWait)
	} else if isRoot {
		waitInfo = fmt.Sprintf(" [%s]", n.Status)
		if strings.EqualFold(n.Status, "INACTIVE") {
			waitInfo += fmt.Sprintf(" (建议: /kill %d)", n.SID)
		}
	}

	sqlInfo := ""
	if n.SQLID != "" {
		sqlInfo = fmt.Sprintf(" SQL:%s", n.SQLID)
	}

	userInfo := n.Username
	if userInfo == "" {
		userInfo = "-"
	}

	b.WriteString(fmt.Sprintf("%s%s SID:%d %s%s%s",
		prefix, connector, n.SID, userInfo, sqlInfo, waitInfo))

	// SQL text preview on second line for root blockers.
	if isRoot && n.SQLText != "" {
		b.WriteString(fmt.Sprintf("\n%s   SQL: %s", prefix, btTrunc(n.SQLText, 70)))
	}
	b.WriteString("\n")

	// Render children.
	childPrefix := prefix
	if !isRoot {
		if isLast {
			childPrefix += "  "
		} else {
			childPrefix += "│ "
		}
	}

	for i, child := range n.Children {
		last := i == len(n.Children)-1
		renderNode(b, child, childPrefix, last, false)
	}
}

// ── helpers ──

func btTrunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
