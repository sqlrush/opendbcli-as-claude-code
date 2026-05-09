/*-------------------------------------------------------------------------
 *
 * blocktree_test.go
 *	  Test cases for blocktree.go (monitor package):
 *	  TestParseBlockNodes, TestBuildBlockTree,
 *	  TestBuildBlockTree_MultipleRoots.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/blocktree_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"strings"
	"testing"
)

func TestParseBlockNodes(t *testing.T) {
	rows := [][]interface{}{
		// SID, Serial, Username, BlockingSession, SQLID, Event, WaitClass, SecondsInWait, Status, Program, SQLText
		{142, 1001, "SCOTT", nil, "abc123def4567", "SQL*Net message from client", "Idle", 0, "ACTIVE", "sqlplus.exe", "UPDATE orders SET status='X'"},
		{200, 2001, "HR", 142, "xyz789abc0123", "enq: TX - row lock contention", "Application", 15, "ACTIVE", "app.jar", "UPDATE orders SET status='Y'"},
		{201, 2002, "HR", 142, "xyz789abc0124", "enq: TX - row lock contention", "Application", 10, "ACTIVE", "app.jar", "UPDATE orders SET status='Z'"},
		{300, 3001, "SYSTEM", 200, "qqq111222333a", "enq: TX - row lock contention", "Application", 5, "ACTIVE", "batch.sh", "SELECT * FROM orders"},
	}

	nodes := parseBlockNodes(rows)
	if len(nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}

	// SID 142 has no blocking_session → root.
	if nodes[0].SID != 142 {
		t.Errorf("first node SID = %d, want 142", nodes[0].SID)
	}
	if nodes[0].BlockingSession != 0 {
		t.Errorf("root blocking_session = %d, want 0", nodes[0].BlockingSession)
	}

	// SID 200 blocked by 142.
	if nodes[1].BlockingSession != 142 {
		t.Errorf("SID 200 blocking_session = %d, want 142", nodes[1].BlockingSession)
	}
}

func TestBuildBlockTree(t *testing.T) {
	nodes := []*blockNode{
		{SID: 142, BlockingSession: 0, Username: "SCOTT", SQLID: "abc123"},
		{SID: 200, BlockingSession: 142, Username: "HR", Event: "enq: TX"},
		{SID: 201, BlockingSession: 142, Username: "HR", Event: "enq: TX"},
		{SID: 300, BlockingSession: 200, Username: "SYSTEM", Event: "enq: TX"},
	}

	roots, totalVictims := buildBlockTree(nodes)

	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	if totalVictims != 3 {
		t.Errorf("totalVictims = %d, want 3", totalVictims)
	}

	root := roots[0]
	if root.SID != 142 {
		t.Errorf("root SID = %d, want 142", root.SID)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root should have 2 children, got %d", len(root.Children))
	}

	// SID 200 should have child SID 300.
	var sid200 *blockNode
	for _, c := range root.Children {
		if c.SID == 200 {
			sid200 = c
			break
		}
	}
	if sid200 == nil {
		t.Fatal("SID 200 not found as child of root")
	}
	if len(sid200.Children) != 1 || sid200.Children[0].SID != 300 {
		t.Error("SID 200 should have child SID 300")
	}
}

func TestBuildBlockTree_MultipleRoots(t *testing.T) {
	nodes := []*blockNode{
		{SID: 100, BlockingSession: 0, Username: "A"},
		{SID: 200, BlockingSession: 0, Username: "B"},
		{SID: 101, BlockingSession: 100, Username: "C"},
		{SID: 201, BlockingSession: 200, Username: "D"},
	}

	roots, totalVictims := buildBlockTree(nodes)

	if len(roots) != 2 {
		t.Errorf("expected 2 roots, got %d", len(roots))
	}
	if totalVictims != 2 {
		t.Errorf("totalVictims = %d, want 2", totalVictims)
	}
}

func TestRenderBlockTree(t *testing.T) {
	root := &blockNode{
		SID: 142, Username: "SCOTT", SQLID: "abc123def4567",
		Status: "ACTIVE", SQLText: "UPDATE orders SET status='X'",
		Children: []*blockNode{
			{
				SID: 200, Username: "HR", SQLID: "xyz789", BlockingSession: 142,
				Event: "enq: TX - row lock contention", SecondsInWait: 15,
				Children: []*blockNode{
					{
						SID: 300, Username: "SYS", BlockingSession: 200,
						Event: "enq: TX - row lock contention", SecondsInWait: 5,
					},
				},
			},
			{
				SID: 201, Username: "HR", BlockingSession: 142,
				Event: "enq: TX - row lock contention", SecondsInWait: 10,
			},
		},
	}

	output := renderBlockTree([]*blockNode{root}, 3)

	// Should contain tree structure elements.
	if !strings.Contains(output, "SID:142") {
		t.Error("should contain root SID")
	}
	if !strings.Contains(output, "SID:200") {
		t.Error("should contain child SID 200")
	}
	if !strings.Contains(output, "SID:300") {
		t.Error("should contain grandchild SID 300")
	}
	if !strings.Contains(output, "├─") || !strings.Contains(output, "└─") {
		t.Error("should contain tree connectors")
	}
	if !strings.Contains(output, "enq: TX") {
		t.Error("should contain wait event")
	}
	if !strings.Contains(output, "15s") {
		t.Error("should contain wait time")
	}
	if !strings.Contains(output, "3 个被阻塞会话") {
		t.Error("should show total victim count")
	}
}

func TestRenderBlockTree_Empty(t *testing.T) {
	output := renderBlockTree(nil, 0)
	if !strings.Contains(output, "0 条") {
		t.Errorf("empty tree should say 0, got %q", output)
	}
}

func TestBtTrunc(t *testing.T) {
	if btTrunc("short", 10) != "short" {
		t.Error("short string should not be truncated")
	}
	result := btTrunc("this is a long string", 10)
	if len([]rune(result)) != 10 {
		t.Errorf("truncated length = %d, want 10", len([]rune(result)))
	}
	if !strings.HasSuffix(result, "…") {
		t.Error("truncated string should end with …")
	}
}
