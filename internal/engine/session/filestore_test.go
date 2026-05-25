/*-------------------------------------------------------------------------
 *
 * filestore_test.go
 *	  Test cases for filestore.go (session package):
 *	  TestFileSessionStore_SaveAndLoad,
 *	  TestFileSessionStore_LoadNotFound,
 *	  TestFileSessionStore_ListByInstance.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/session/filestore_test.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

func TestFileSessionStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	ctx := context.Background()

	sess := NewSession("mysql-prod")
	sess.Messages = []provider.Message{
		{Role: "user", Content: "数据库变慢了"},
		{Role: "assistant", Content: "我来检查一下"},
	}
	sess.TurnCount = 3
	sess.TokensUsed = 1500

	// Save
	if err := store.Save(ctx, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, "mysql-prod", string(sess.ID)+".jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Load
	loaded, err := store.Load(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil")
	}
	if loaded.ID != sess.ID {
		t.Errorf("ID mismatch: got %s, want %s", loaded.ID, sess.ID)
	}
	if loaded.Instance != "mysql-prod" {
		t.Errorf("Instance: got %s, want mysql-prod", loaded.Instance)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("Messages: got %d, want 2", len(loaded.Messages))
	}
	if loaded.TurnCount != 3 {
		t.Errorf("TurnCount: got %d, want 3", loaded.TurnCount)
	}
}

func TestFileSessionStore_LoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	ctx := context.Background()

	sess, err := store.Load(ctx, "mysql-prod:nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestFileSessionStore_ListByInstance(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	ctx := context.Background()

	// Create 3 sessions
	for i := 0; i < 3; i++ {
		sess := NewSession("mysql-prod")
		sess.TurnCount = i + 1
		if err := store.Save(ctx, sess); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// List all
	sessions, err := store.ListByInstance(ctx, "mysql-prod", 10)
	if err != nil {
		t.Fatalf("ListByInstance: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("got %d sessions, want 3", len(sessions))
	}

	// List with limit
	sessions, err = store.ListByInstance(ctx, "mysql-prod", 2)
	if err != nil {
		t.Fatalf("ListByInstance: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(sessions))
	}
}

func TestFileSessionStore_ListByInstance_Empty(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	ctx := context.Background()

	sessions, err := store.ListByInstance(ctx, "nonexistent", 10)
	if err != nil {
		t.Fatalf("ListByInstance: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("got %d sessions, want 0", len(sessions))
	}
}

func TestFileSessionStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	ctx := context.Background()

	sess := NewSession("mysql-prod")

	// Save twice — second save should overwrite cleanly
	if err := store.Save(ctx, sess); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	sess.TurnCount = 5
	if err := store.Save(ctx, sess); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	loaded, err := store.Load(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.TurnCount != 5 {
		t.Errorf("TurnCount: got %d, want 5", loaded.TurnCount)
	}

	// No .tmp files left behind
	matches, _ := filepath.Glob(filepath.Join(dir, "mysql-prod", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover tmp files: %v", matches)
	}
}

func TestSessionID_Instance(t *testing.T) {
	tests := []struct {
		id       SessionID
		wantInst string
	}{
		{"mysql-prod:abc-123", "mysql-prod"},
		{"oracle:def-456", "oracle"},
		{"my:host:xyz", "my:host"},
		{"nocolon", "nocolon"},
	}
	for _, tt := range tests {
		got := tt.id.Instance()
		if got != tt.wantInst {
			t.Errorf("SessionID(%q).Instance() = %q, want %q", tt.id, got, tt.wantInst)
		}
	}
}
