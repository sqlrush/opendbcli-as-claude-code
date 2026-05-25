/*-------------------------------------------------------------------------
 *
 * os_auth.go
 *	  OSAuthProvider handles OS-level authentication (e.g., / as
 *	  sysdba). The OS user's group membership (dba group) is used for
 *	  authentication. The actual AUTH TYPE=OS option is set via
 *	  ConnectionConfig.Options by the connection manager.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/os_auth.go
 *
 *-------------------------------------------------------------------------
 */
package credential

// OSAuthProvider handles OS-level authentication (e.g., / as sysdba).
// The OS user's group membership (dba group) is used for authentication.
// The actual AUTH TYPE=OS option is set via ConnectionConfig.Options
// by the connection manager.
type OSAuthProvider struct{}

// NewOSAuthProvider creates an OSAuthProvider.
func NewOSAuthProvider() *OSAuthProvider {
	return &OSAuthProvider{}
}

// Resolve returns empty credentials — OS authentication uses the
// operating system user's identity, no password needed.
func (p *OSAuthProvider) Resolve(_ string) (string, error) {
	return "", nil
}
