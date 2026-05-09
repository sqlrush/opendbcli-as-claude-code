/*-------------------------------------------------------------------------
 *
 * providers_test.go
 *	  Test cases for providers.go (credential package):
 *	  TestWalletProvider_Resolve, TestOSAuthProvider_Resolve,
 *	  TestKerberosProvider_Resolve.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/providers_test.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Wallet Provider ---

func TestWalletProvider_Resolve(t *testing.T) {
	p := NewWalletProvider()
	password, err := p.Resolve("any-db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "" {
		t.Errorf("Resolve() = %q, want empty (wallet handles auth)", password)
	}
}

// --- OS Auth Provider ---

func TestOSAuthProvider_Resolve(t *testing.T) {
	p := NewOSAuthProvider()
	password, err := p.Resolve("local-sys")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "" {
		t.Errorf("Resolve() = %q, want empty (OS handles auth)", password)
	}
}

// --- Kerberos Provider ---

func TestKerberosProvider_Resolve(t *testing.T) {
	p := NewKerberosProvider()
	password, err := p.Resolve("krb-db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if password != "" {
		t.Errorf("Resolve() = %q, want empty (Kerberos handles auth)", password)
	}
}

// --- LDAP Provider ---

func TestLDAPProvider_NoServer(t *testing.T) {
	p := NewLDAPProvider("", "")
	_, err := p.Resolve("db")
	if err == nil {
		t.Fatal("expected error for empty LDAP server")
	}
}

func TestLDAPProvider_Accessors(t *testing.T) {
	p := NewLDAPProvider("ldap://ad.corp.com", "dc=corp,dc=com")
	if p.Server() != "ldap://ad.corp.com" {
		t.Errorf("Server() = %q", p.Server())
	}
	if p.BaseDN() != "dc=corp,dc=com" {
		t.Errorf("BaseDN() = %q", p.BaseDN())
	}
}

// --- Token Provider ---

func TestTokenProvider_NoURL(t *testing.T) {
	p := NewTokenProvider("", "", "", "")
	_, err := p.Resolve("db")
	if err == nil {
		t.Fatal("expected error for empty token URL")
	}
}

func TestTokenProvider_SuccessfulFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"access_token": "test-token-123",
			"expires_in":   3600,
			"token_type":   "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewTokenProvider(server.URL, "database", "", "")
	token, err := p.Resolve("cloud-db")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if token != "test-token-123" {
		t.Errorf("Resolve() = %q, want %q", token, "test-token-123")
	}
}

func TestTokenProvider_CachedToken(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := map[string]any{
			"access_token": "cached-token",
			"expires_in":   3600,
			"token_type":   "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewTokenProvider(server.URL, "", "", "")

	// First call fetches.
	_, err := p.Resolve("db")
	if err != nil {
		t.Fatalf("first Resolve() error: %v", err)
	}

	// Second call should use cache.
	_, err = p.Resolve("db")
	if err != nil {
		t.Fatalf("second Resolve() error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("server called %d times, want 1 (cached)", callCount)
	}
}

func TestTokenProvider_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"error":             "invalid_client",
			"error_description": "bad credentials",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewTokenProvider(server.URL, "", "", "")
	_, err := p.Resolve("db")
	if err == nil {
		t.Fatal("expected error for error response")
	}
}

func TestTokenProvider_Accessor(t *testing.T) {
	p := NewTokenProvider("https://auth.example.com/token", "db", "", "")
	if p.TokenURL() != "https://auth.example.com/token" {
		t.Errorf("TokenURL() = %q", p.TokenURL())
	}
}

// --- NewProvider factory ---

func TestNewProvider_Wallet(t *testing.T) {
	p, err := NewProvider("wallet")
	if err != nil {
		t.Fatalf("NewProvider(wallet) error: %v", err)
	}
	if _, ok := p.(*WalletProvider); !ok {
		t.Errorf("got %T, want *WalletProvider", p)
	}
}

func TestNewProvider_OS(t *testing.T) {
	p, err := NewProvider("os")
	if err != nil {
		t.Fatalf("NewProvider(os) error: %v", err)
	}
	if _, ok := p.(*OSAuthProvider); !ok {
		t.Errorf("got %T, want *OSAuthProvider", p)
	}
}

func TestNewProvider_Kerberos(t *testing.T) {
	p, err := NewProvider("kerberos")
	if err != nil {
		t.Fatalf("NewProvider(kerberos) error: %v", err)
	}
	if _, ok := p.(*KerberosProvider); !ok {
		t.Errorf("got %T, want *KerberosProvider", p)
	}
}

func TestNewProvider_SaveRequiresDir(t *testing.T) {
	_, err := NewProvider("save")
	if err == nil {
		t.Fatal("NewProvider(save) should error (requires dir)")
	}
}

func TestNewProvider_LDAPRequiresConfig(t *testing.T) {
	_, err := NewProvider("ldap")
	if err == nil {
		t.Fatal("NewProvider(ldap) should error (requires config)")
	}
}

func TestNewProvider_TokenRequiresConfig(t *testing.T) {
	_, err := NewProvider("token")
	if err == nil {
		t.Fatal("NewProvider(token) should error (requires config)")
	}
}
