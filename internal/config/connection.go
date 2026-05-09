/*-------------------------------------------------------------------------
 *
 * connection.go
 *	  Credential describes how to obtain the password for a connection.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/config/connection.go
 *
 *-------------------------------------------------------------------------
 */
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Credential describes how to obtain the password for a connection.
type Credential struct {
	Provider   string `yaml:"provider"`
	Value      string `yaml:"value,omitempty"`
	VaultPath  string `yaml:"vault_path,omitempty"`
	WalletDir  string `yaml:"wallet_dir,omitempty"`   // Oracle Wallet TNS_ADMIN directory
	LDAPServer string `yaml:"ldap_server,omitempty"`  // LDAP server URL
	LDAPBaseDN string `yaml:"ldap_base_dn,omitempty"` // LDAP search base DN
	TokenURL     string `yaml:"token_url,omitempty"`      // OAuth2 token endpoint
	TokenScope   string `yaml:"token_scope,omitempty"`    // OAuth2 scope
	ClientID     string `yaml:"client_id,omitempty"`      // OAuth2 client ID
	ClientSecret string `yaml:"client_secret,omitempty"`  // OAuth2 client secret
}

// Connection represents a single database connection entry.
type Connection struct {
	Name       string     `yaml:"name"`
	DBType     string     `yaml:"db_type,omitempty"` // oracle|mysql|postgres|opengauss|gaussdb (default: oracle)
	Host       string     `yaml:"host"`
	Port       int        `yaml:"port"`
	Service    string     `yaml:"service"`
	SID        string     `yaml:"sid,omitempty"`      // Oracle: alternative to service name
	Database   string     `yaml:"database,omitempty"` // MySQL/PG/OG: database name
	User       string     `yaml:"user"`
	AuthMode   string     `yaml:"auth_mode,omitempty"` // prompt|save|wallet|os|ldap|kerberos|token
	Privilege  string     `yaml:"privilege,omitempty"` // sysdba|sysoper (empty = normal)
	Credential Credential `yaml:"credential"`
}

// ConnectionGroup represents a group of related connections loaded from a
// single YAML file.
type ConnectionGroup struct {
	Group       string       `yaml:"group"`
	Tags        []string     `yaml:"tags"`
	Connections []Connection `yaml:"connections"`
}

// LoadConnectionGroups scans the given directory for YAML files and parses
// each one into a ConnectionGroup. Non-YAML files are silently ignored.
// Returns an empty slice (not nil) when the directory contains no YAML files.
func LoadConnectionGroups(dir string) ([]ConnectionGroup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading connections directory %q: %w", dir, err)
	}

	groups := make([]ConnectionGroup, 0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading connection file %q: %w", entry.Name(), err)
		}

		var group ConnectionGroup
		if err := yaml.Unmarshal(data, &group); err != nil {
			return nil, fmt.Errorf("parsing connection file %q: %w", entry.Name(), err)
		}

		groups = append(groups, group)
	}

	return groups, nil
}
