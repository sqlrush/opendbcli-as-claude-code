/*-------------------------------------------------------------------------
 *
 * token_test.go
 *	  Test cases for token.go (cluster package): TestGenerateToken,
 *	  TestTokenExpiry, TestSaveAndLoadToken.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/token_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import (
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if len(token.Value) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("token length = %d, want 32", len(token.Value))
	}
	if !token.IsValid() {
		t.Error("freshly generated token should be valid")
	}
}

func TestTokenExpiry(t *testing.T) {
	token := Token{
		Value:     "test",
		CreatedAt: time.Now().Add(-25 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	if token.IsValid() {
		t.Error("expired token should not be valid")
	}
}

func TestSaveAndLoadToken(t *testing.T) {
	tmpDir := t.TempDir()

	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if err := SaveToken(tmpDir, token); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	loaded, err := LoadToken(tmpDir)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}

	if loaded.Value != token.Value {
		t.Errorf("value = %q, want %q", loaded.Value, token.Value)
	}
	if !loaded.IsValid() {
		t.Error("loaded token should be valid")
	}
}

func TestTokenUniqueness(t *testing.T) {
	t1, _ := GenerateToken()
	t2, _ := GenerateToken()
	if t1.Value == t2.Value {
		t.Error("two generated tokens should not be equal")
	}
}
