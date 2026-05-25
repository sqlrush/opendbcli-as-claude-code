/*-------------------------------------------------------------------------
 *
 * credential.go
 *	  Provider resolves credentials (typically passwords) for named
 *	  connections. Implementations may prompt the user, read from
 *	  encrypted storage, or delegate to an external secret manager.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/credential.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import "fmt"

// Provider resolves credentials (typically passwords) for named connections.
// Implementations may prompt the user, read from encrypted storage,
// or delegate to an external secret manager.
type Provider interface {
	// Resolve returns the password for the given connection name.
	// For auth modes that don't need a password (os, wallet, kerberos),
	// Resolve returns an empty string and nil error.
	Resolve(connName string) (string, error)
}

// NewProvider creates a credential Provider of the given type.
// Supported types: "prompt", "save", "wallet", "os", "ldap", "kerberos", "token".
// "encrypted" is kept as an alias for "save".
func NewProvider(providerType string) (Provider, error) {
	switch providerType {
	case "prompt":
		return NewPromptProvider(), nil
	case "save", "encrypted":
		return nil, fmt.Errorf("save provider requires credentials directory; use NewEncryptedProvider(dir)")
	case "wallet":
		return NewWalletProvider(), nil
	case "os":
		return NewOSAuthProvider(), nil
	case "ldap":
		return nil, fmt.Errorf("LDAP provider requires configuration; use NewLDAPProvider(server, baseDN)")
	case "kerberos":
		return NewKerberosProvider(), nil
	case "token":
		return nil, fmt.Errorf("token provider requires configuration; use NewTokenProvider(tokenURL, scope, clientID, clientSecret)")
	default:
		return nil, fmt.Errorf("unknown credential provider type: %q", providerType)
	}
}
