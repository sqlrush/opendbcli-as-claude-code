/*-------------------------------------------------------------------------
 *
 * driver_test.go
 *	  Test cases for driver.go (driver package): TestBuildDSN,
 *	  TestExtractGaussVersion.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/driver/driver_test.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
)

func TestBuildDSN(t *testing.T) {
	tests := []struct {
		name     string
		cfg      db.ConnectionConfig
		contains []string
		// notContains catches accidentally inheriting libpq's "dbname=" — gaussdb-go
		// requires "database=" instead, and silently ignoring "dbname" is exactly the
		// kind of misconfiguration that surfaces only at customer site.
		notContains []string
	}{
		{
			name: "minimal_uses_database_keyword_not_dbname",
			cfg: db.ConnectionConfig{
				Host:     "10.0.0.1",
				Port:     5432,
				User:     "u",
				Password: "p",
				Database: "mydb",
			},
			contains:    []string{"host=10.0.0.1", "port=5432", "user=u", "password=p", "database=mydb", "sslmode=disable", "application_name=opendb"},
			notContains: []string{"dbname="},
		},
		{
			name: "service_falls_back_to_database_when_database_empty",
			cfg: db.ConnectionConfig{
				Host: "h", Port: 1, User: "u", Password: "p",
				Service: "fromservice",
			},
			contains: []string{"database=fromservice"},
		},
		{
			name: "default_database_postgres_when_both_empty",
			cfg: db.ConnectionConfig{
				Host: "h", Port: 1, User: "u", Password: "p",
			},
			contains: []string{"database=postgres"},
		},
		{
			name: "options_sslmode_overrides_default",
			cfg: db.ConnectionConfig{
				Host: "h", Port: 1, User: "u", Password: "p",
				Options: map[string]string{"sslmode": "require"},
			},
			contains:    []string{"sslmode=require"},
			notContains: []string{"sslmode=disable"},
		},
		{
			name: "custom_application_name_replaces_default",
			cfg: db.ConnectionConfig{
				Host: "h", Port: 1, User: "u", Password: "p",
				Options: map[string]string{"application_name": "probe"},
			},
			contains:    []string{"application_name=probe"},
			notContains: []string{"application_name=opendb"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDSN(tt.cfg)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("DSN missing %q\n  got: %s", want, got)
				}
			}
			for _, banned := range tt.notContains {
				if strings.Contains(got, banned) {
					t.Errorf("DSN contains forbidden %q\n  got: %s", banned, got)
				}
			}
		})
	}
}

func TestExtractGaussVersion(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "opengauss_5_0_format",
			raw:  "(openGauss 5.0.0 build a07d57c3) compiled at 2023-03-29",
			want: "openGauss 5.0.0",
		},
		{
			name: "gaussdb_kernel_format",
			raw:  "(GaussDB Kernel V500R002C20 build foo) compiled",
			want: "GaussDB Kernel V500R002C20",
		},
		{
			name: "raw_when_no_marker",
			raw:  "Some unknown banner",
			want: "Some unknown banner",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGaussVersion(tt.raw)
			if got != tt.want {
				t.Errorf("extractGaussVersion(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
