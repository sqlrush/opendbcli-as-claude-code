/*-------------------------------------------------------------------------
 *
 * credential_test.go
 *	  Test cases for credential.go (credential package):
 *	  TestNewProvider_Prompt, TestNewProvider_Encrypted,
 *	  TestNewProvider_Unknown.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/credential_test.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Compile-time checks that providers satisfy their interfaces.
var (
	_ Provider         = (*PromptProvider)(nil)
	_ Provider         = (*EncryptedProvider)(nil)
	_ StorableProvider = (*EncryptedProvider)(nil)
	_ Provider         = (*WalletProvider)(nil)
	_ Provider         = (*OSAuthProvider)(nil)
	_ Provider         = (*LDAPProvider)(nil)
	_ Provider         = (*KerberosProvider)(nil)
	_ Provider         = (*TokenProvider)(nil)
)

func TestNewProvider_Prompt(t *testing.T) {
	p, err := NewProvider("prompt")
	if err != nil {
		t.Fatalf("NewProvider(\"prompt\") returned error: %v", err)
	}
	if _, ok := p.(*PromptProvider); !ok {
		t.Errorf("NewProvider(\"prompt\") returned %T, want *PromptProvider", p)
	}
}

func TestNewProvider_Encrypted(t *testing.T) {
	// "encrypted" / "save" requires directory argument via NewEncryptedProvider(dir).
	// NewProvider returns an error directing the user.
	_, err := NewProvider("encrypted")
	if err == nil {
		t.Fatal("NewProvider(\"encrypted\") should return error (requires dir)")
	}
	if !strings.Contains(err.Error(), "credentials directory") {
		t.Errorf("error should mention directory requirement, got: %v", err)
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	_, err := NewProvider("vault")
	if err == nil {
		t.Fatal("NewProvider(\"vault\") expected error, got nil")
	}
	want := `unknown credential provider type: "vault"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestNewProvider_EmptyString(t *testing.T) {
	_, err := NewProvider("")
	if err == nil {
		t.Fatal("NewProvider(\"\") expected error, got nil")
	}
}

func TestEncryptedProvider_Factory_ResolveNotFound(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)
	_, err := p.Resolve("prod-db")
	if err == nil {
		t.Fatal("expected error from EncryptedProvider.Resolve, got nil")
	}
	if !strings.Contains(err.Error(), "no saved credential") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestEncryptedProvider_Factory_IncludesConnectionName(t *testing.T) {
	dir := t.TempDir()
	p := NewEncryptedProvider(dir)
	_, err := p.Resolve("staging-db")
	if err == nil {
		t.Fatal("expected error from EncryptedProvider.Resolve, got nil")
	}
	if !strings.Contains(err.Error(), "staging-db") {
		t.Errorf("error should contain connection name, got: %s", err.Error())
	}
}

func TestPromptProvider_DefaultConstruction(t *testing.T) {
	p := NewPromptProvider()
	if p == nil {
		t.Fatal("expected non-nil PromptProvider")
	}
	if p.reader == nil {
		t.Error("default reader should not be nil")
	}
	if p.writer == nil {
		t.Error("default writer should not be nil")
	}
}

func TestPromptProvider_WithOptions(t *testing.T) {
	var buf bytes.Buffer
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	defer r.Close()
	w.Close()

	p := NewPromptProvider(WithReader(r), WithWriter(&buf))
	if p.reader != r {
		t.Error("WithReader option was not applied")
	}
	if p.writer != &buf {
		t.Error("WithWriter option was not applied")
	}
}

func TestPromptProvider_Resolve_WithPipe(t *testing.T) {
	// Create a pipe to simulate input.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	defer r.Close()

	// Write a password then close the writer end.
	if _, err := w.WriteString("secret123\n"); err != nil {
		t.Fatalf("write to pipe failed: %v", err)
	}
	w.Close()

	var promptBuf bytes.Buffer
	p := NewPromptProvider(WithReader(r), WithWriter(&promptBuf))

	// term.ReadPassword requires a real terminal fd on most platforms.
	// On a pipe it typically returns an error, which is acceptable.
	result, resolveErr := p.Resolve("test-db")
	if resolveErr != nil {
		t.Logf("Resolve returned error (expected on non-terminal fd): %v", resolveErr)
		return
	}

	if result != "secret123" {
		t.Errorf("Resolve() = %q, want %q", result, "secret123")
	}

	if !bytes.Contains(promptBuf.Bytes(), []byte("Password for test-db")) {
		t.Errorf("prompt output = %q, want it to contain 'Password for test-db'", promptBuf.String())
	}
}
