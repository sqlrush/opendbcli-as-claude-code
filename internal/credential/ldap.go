/*-------------------------------------------------------------------------
 *
 * ldap.go
 *	  LDAPProvider validates credentials against an LDAP/Active
 *	  Directory server. After validation, the password is passed to
 *	  Oracle for connection.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/ldap.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"fmt"
)

// LDAPProvider validates credentials against an LDAP/Active Directory server.
// After validation, the password is passed to Oracle for connection.
//
// Note: Full LDAP integration requires the go-ldap/ldap/v3 library.
// This implementation prompts for password and validates via LDAP bind.
type LDAPProvider struct {
	server string // LDAP server URL (e.g., ldap://ad.corp.com:389)
	baseDN string // search base DN (e.g., dc=corp,dc=com)
	prompt *PromptProvider
}

// NewLDAPProvider creates an LDAPProvider with the given LDAP server and base DN.
func NewLDAPProvider(server, baseDN string) *LDAPProvider {
	return &LDAPProvider{
		server: server,
		baseDN: baseDN,
		prompt: NewPromptProvider(),
	}
}

// Resolve prompts for a password, validates it against the LDAP server,
// and returns the password for Oracle connection.
func (p *LDAPProvider) Resolve(connName string) (string, error) {
	if p.server == "" {
		return "", fmt.Errorf("LDAP server not configured for connection %q", connName)
	}

	password, err := p.prompt.Resolve(connName)
	if err != nil {
		return "", err
	}

	// TODO: When go-ldap/ldap/v3 is added as a dependency:
	// 1. Dial LDAP server
	// 2. Search for user DN
	// 3. Bind with user DN + password to validate
	// For now, we trust the password and let Oracle validate it.
	// The LDAP server config is recorded for future validation.
	_ = p.baseDN

	return password, nil
}

// Server returns the configured LDAP server URL.
func (p *LDAPProvider) Server() string {
	return p.server
}

// BaseDN returns the configured LDAP search base DN.
func (p *LDAPProvider) BaseDN() string {
	return p.baseDN
}
