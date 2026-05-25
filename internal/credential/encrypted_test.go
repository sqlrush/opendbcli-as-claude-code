/*-------------------------------------------------------------------------
 *
 * encrypted_test.go
 *	  Test cases for encrypted.go (credential package):
 *	  TestEncryptedProvider_StoreAndResolve,
 *	  TestEncryptedProvider_DifferentConnections,
 *	  TestEncryptedProvider_Overwrite.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/encrypted_test.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedProvider_StoreAndResolve(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	// Store a credential.
	if err := p.Store("test-db", "s3cret!"); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	// Resolve should return the same password.
	password, err := p.Resolve("test-db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "s3cret!" {
		t.Errorf("Resolve() = %q, want %q", password, "s3cret!")
	}
}

func TestEncryptedProvider_DifferentConnections(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	if err := p.Store("db-a", "pass-a"); err != nil {
		t.Fatalf("Store(db-a) error: %v", err)
	}
	if err := p.Store("db-b", "pass-b"); err != nil {
		t.Fatalf("Store(db-b) error: %v", err)
	}

	passA, err := p.Resolve("db-a")
	if err != nil {
		t.Fatalf("Resolve(db-a) error: %v", err)
	}
	passB, err := p.Resolve("db-b")
	if err != nil {
		t.Fatalf("Resolve(db-b) error: %v", err)
	}

	if passA != "pass-a" {
		t.Errorf("Resolve(db-a) = %q, want %q", passA, "pass-a")
	}
	if passB != "pass-b" {
		t.Errorf("Resolve(db-b) = %q, want %q", passB, "pass-b")
	}
}

func TestEncryptedProvider_Overwrite(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	if err := p.Store("db", "old"); err != nil {
		t.Fatalf("Store(old) error: %v", err)
	}
	if err := p.Store("db", "new"); err != nil {
		t.Fatalf("Store(new) error: %v", err)
	}

	password, err := p.Resolve("db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "new" {
		t.Errorf("Resolve() = %q, want %q (overwritten)", password, "new")
	}
}

func TestEncryptedProvider_ResolveNotFound(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	_, err := p.Resolve("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent credential")
	}
}

func TestEncryptedProvider_Delete(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	if err := p.Store("db", "pass"); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	if err := p.Delete("db"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	_, err := p.Resolve("db")
	if err == nil {
		t.Error("expected error after Delete")
	}
}

func TestEncryptedProvider_DeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	// Deleting a non-existent credential should not error.
	if err := p.Delete("nonexistent"); err != nil {
		t.Errorf("Delete(nonexistent) = %v, want nil", err)
	}
}

func TestEncryptedProvider_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	// Write garbage to the credential file.
	path := filepath.Join(dir, "corrupt.enc")
	if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
		t.Fatalf("writing corrupt file: %v", err)
	}

	_, err := p.Resolve("corrupt")
	if err == nil {
		t.Fatal("expected error for corrupted file")
	}
}

func TestEncryptedProvider_TamperedCiphertext(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	if err := p.Store("db", "secret"); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	// Tamper with the credential file.
	path := filepath.Join(dir, "db.enc")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	// Flip a byte in the ciphertext (after salt + nonce).
	if len(data) > SaltSize+nonceSize {
		data[SaltSize+nonceSize] ^= 0xff
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("writing tampered file: %v", err)
	}

	_, err = p.Resolve("db")
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestEncryptedProvider_EmptyPassword(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	if err := p.Store("db", ""); err != nil {
		t.Fatalf("Store('') error: %v", err)
	}

	password, err := p.Resolve("db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "" {
		t.Errorf("Resolve() = %q, want empty string", password)
	}
}

func TestEncryptedProvider_UnicodePassword(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	pass := "密码是中文🔐"
	if err := p.Store("db", pass); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	password, err := p.Resolve("db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != pass {
		t.Errorf("Resolve() = %q, want %q", password, pass)
	}
}

func TestEncryptedProvider_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	if err := p.Store("db", "pass"); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	path := filepath.Join(dir, "db.enc")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestEncryptedProvider_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)

	badNames := []string{"", ".", "..", "../etc/passwd", "foo/bar", "foo\\bar"}
	for _, name := range badNames {
		if err := p.Store(name, "pass"); err == nil {
			t.Errorf("Store(%q) should error for path traversal", name)
		}
		if _, err := p.Resolve(name); err == nil {
			t.Errorf("Resolve(%q) should error for path traversal", name)
		}
		if err := p.Delete(name); err == nil {
			t.Errorf("Delete(%q) should error for path traversal", name)
		}
	}
}

func TestEncryptedProvider_StoreCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "creds")
	p := NewEncryptedProvider(dir)

	if err := p.Store("db", "pass"); err != nil {
		t.Fatalf("Store() should create directory, got error: %v", err)
	}

	password, err := p.Resolve("db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "pass" {
		t.Errorf("Resolve() = %q, want %q", password, "pass")
	}
}
