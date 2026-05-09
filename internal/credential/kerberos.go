/*-------------------------------------------------------------------------
 *
 * kerberos.go
 *	  KerberosProvider handles Kerberos (GSSAPI) authentication. It
 *	  relies on the system's Kerberos ticket cache (kinit). The actual
 *	  AUTH TYPE=KERBEROS option is set via ConnectionConfig.Options by
 *	  the connection manager.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/kerberos.go
 *
 *-------------------------------------------------------------------------
 */
package credential

// KerberosProvider handles Kerberos (GSSAPI) authentication.
// It relies on the system's Kerberos ticket cache (kinit).
// The actual AUTH TYPE=KERBEROS option is set via ConnectionConfig.Options
// by the connection manager.
//
// Note: Full Kerberos integration requires jcmturner/gokrb5/v8 library.
// This implementation assumes the user has already obtained a ticket via kinit.
type KerberosProvider struct{}

// NewKerberosProvider creates a KerberosProvider.
func NewKerberosProvider() *KerberosProvider {
	return &KerberosProvider{}
}

// Resolve returns empty credentials — Kerberos authentication uses
// the system's ticket cache. The user must run kinit beforehand.
func (p *KerberosProvider) Resolve(_ string) (string, error) {
	return "", nil
}
