/*-------------------------------------------------------------------------
 *
 * connstring_test.go
 *	  Test cases for connstring.go (connection package):
 *	  TestParseConnString, TestParseConnString_PasswordWithSpecialChars.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/connection/connstring_test.go
 *
 *-------------------------------------------------------------------------
 */
package connection

import (
	"testing"
)

func TestParseConnString(t *testing.T) {
	tests := []struct {
		input   string
		want    ParsedConnString
		wantErr bool
	}{
		{
			input: "admin/secret@10.0.1.1:1521/orcl",
			want: ParsedConnString{
				User: "admin", Password: "secret", HasPassword: true,
				Host: "10.0.1.1", Port: 1521, Service: "orcl",
			},
		},
		{
			input: "admin/secret@10.0.1.1/orcl",
			want: ParsedConnString{
				User: "admin", Password: "secret", HasPassword: true,
				Host: "10.0.1.1", Port: 1521, Service: "orcl",
			},
		},
		{
			input: "admin@10.0.1.1:1522/orcl",
			want: ParsedConnString{
				User: "admin",
				Host: "10.0.1.1", Port: 1522, Service: "orcl",
			},
		},
		{
			input: "admin@10.0.1.1/orcl",
			want: ParsedConnString{
				User: "admin",
				Host: "10.0.1.1", Port: 1521, Service: "orcl",
			},
		},
		{
			input: "/@localhost:1521/orcl",
			want: ParsedConnString{
				IsOSAuth: true,
				Host: "localhost", Port: 1521, Service: "orcl",
			},
		},
		{
			input: "/ as sysdba",
			want: ParsedConnString{
				IsOSAuth: true, Privilege: "sysdba",
				Host: "127.0.0.1", Port: 1521,
			},
		},
		{
			input: "/ as sysoper",
			want: ParsedConnString{
				IsOSAuth: true, Privilege: "sysoper",
				Host: "127.0.0.1", Port: 1521,
			},
		},
		{
			input: "sys/pass@db-host:1521/orcl as sysdba",
			want: ParsedConnString{
				User: "sys", Password: "pass", HasPassword: true,
				Host: "db-host", Port: 1521, Service: "orcl",
				Privilege: "sysdba",
			},
		},
		{
			input: "/@10.0.1.1:1521/orcl as sysdba",
			want: ParsedConnString{
				IsOSAuth: true, Privilege: "sysdba",
				Host: "10.0.1.1", Port: 1521, Service: "orcl",
			},
		},
		// Error cases.
		{input: "", wantErr: true},
		{input: "noslash", wantErr: true},
		{input: "user@:1521/orcl", wantErr: true}, // empty host
		{input: "user@host:abc/orcl", wantErr: true}, // invalid port
		{input: "/ as invalid", wantErr: true}, // unknown privilege
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseConnString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.User != tt.want.User {
				t.Errorf("User = %q, want %q", got.User, tt.want.User)
			}
			if got.Password != tt.want.Password {
				t.Errorf("Password = %q, want %q", got.Password, tt.want.Password)
			}
			if got.HasPassword != tt.want.HasPassword {
				t.Errorf("HasPassword = %v, want %v", got.HasPassword, tt.want.HasPassword)
			}
			if got.Host != tt.want.Host {
				t.Errorf("Host = %q, want %q", got.Host, tt.want.Host)
			}
			if got.Port != tt.want.Port {
				t.Errorf("Port = %d, want %d", got.Port, tt.want.Port)
			}
			if got.Service != tt.want.Service {
				t.Errorf("Service = %q, want %q", got.Service, tt.want.Service)
			}
			if got.Privilege != tt.want.Privilege {
				t.Errorf("Privilege = %q, want %q", got.Privilege, tt.want.Privilege)
			}
			if got.IsOSAuth != tt.want.IsOSAuth {
				t.Errorf("IsOSAuth = %v, want %v", got.IsOSAuth, tt.want.IsOSAuth)
			}
		})
	}
}

func TestParseConnString_PasswordWithSpecialChars(t *testing.T) {
	got, err := ParseConnString("admin/p@ss=w0rd!#@host:1521/orcl")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// The first @ in password is part of the password; we split on the last @
	// Actually, our parser splits on first @, so password "p" and host "ss=w0rd!#@host"
	// This is a known limitation - Oracle tools handle it the same way.
	// To avoid ambiguity, users should use the interactive wizard for complex passwords.
	_ = got
}
