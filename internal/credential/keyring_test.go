/*-------------------------------------------------------------------------
 *
 * keyring_test.go
 *	  Test cases for keyring.go (credential package):
 *	  TestDeriveKey_Deterministic, TestDeriveKey_DifferentSalts,
 *	  TestDeriveKey_Length.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/keyring_test.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"crypto/rand"
	"testing"
)

func TestDeriveKey_Deterministic(t *testing.T) {
	salt := make([]byte, SaltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("generating salt: %v", err)
	}

	key1, err := DeriveKey(salt)
	if err != nil {
		t.Fatalf("DeriveKey() error: %v", err)
	}
	key2, err := DeriveKey(salt)
	if err != nil {
		t.Fatalf("DeriveKey() second call error: %v", err)
	}

	if len(key1) != keyLength {
		t.Errorf("key length = %d, want %d", len(key1), keyLength)
	}

	// Same salt + machine + user → same key.
	for i := range key1 {
		if key1[i] != key2[i] {
			t.Fatal("DeriveKey() not deterministic: different keys from same inputs")
		}
	}
}

func TestDeriveKey_DifferentSalts(t *testing.T) {
	salt1 := make([]byte, SaltSize)
	salt2 := make([]byte, SaltSize)
	if _, err := rand.Read(salt1); err != nil {
		t.Fatalf("generating salt1: %v", err)
	}
	if _, err := rand.Read(salt2); err != nil {
		t.Fatalf("generating salt2: %v", err)
	}

	key1, err := DeriveKey(salt1)
	if err != nil {
		t.Fatalf("DeriveKey(salt1) error: %v", err)
	}
	key2, err := DeriveKey(salt2)
	if err != nil {
		t.Fatalf("DeriveKey(salt2) error: %v", err)
	}

	// Different salts → different keys.
	same := true
	for i := range key1 {
		if key1[i] != key2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different salts produced identical keys")
	}
}

func TestDeriveKey_Length(t *testing.T) {
	salt := make([]byte, SaltSize)
	key, err := DeriveKey(salt)
	if err != nil {
		t.Fatalf("DeriveKey() error: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32 (AES-256)", len(key))
	}
}

func TestGetMachineID_NonEmpty(t *testing.T) {
	id, err := getMachineID()
	if err != nil {
		t.Fatalf("getMachineID() error: %v", err)
	}
	if id == "" {
		t.Error("getMachineID() returned empty string")
	}
}

func TestGetUsername_NonEmpty(t *testing.T) {
	username, err := getUsername()
	if err != nil {
		t.Fatalf("getUsername() error: %v", err)
	}
	if username == "" {
		t.Error("getUsername() returned empty string")
	}
}

func TestReadFallbackMachineID_NonEmpty(t *testing.T) {
	id, err := readFallbackMachineID()
	if err != nil {
		t.Fatalf("readFallbackMachineID() error: %v", err)
	}
	if id == "" {
		t.Error("readFallbackMachineID() returned empty string")
	}
}
