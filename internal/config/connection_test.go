/*-------------------------------------------------------------------------
 *
 * connection_test.go
 *	  Test cases for connection.go (config package):
 *	  TestLoadConnectionGroups, TestLoadConnectionGroups_EmptyDir,
 *	  TestLoadConnectionGroups_InvalidYAML.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/config/connection_test.go
 *
 *-------------------------------------------------------------------------
 */
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConnectionGroups(t *testing.T) {
	dir := t.TempDir()

	prodYAML := `
group: production
tags:
  - prod
  - critical
connections:
  - name: prod-primary
    host: 10.0.1.100
    port: 1521
    service: orcl
    user: dbadmin
    credential:
      provider: vault
      vault_path: secret/data/prod-db
  - name: prod-standby
    host: 10.0.1.101
    port: 1521
    service: orcl
    user: dbadmin
    credential:
      provider: vault
      vault_path: secret/data/prod-db
`
	devYAML := `
group: development
tags:
  - dev
connections:
  - name: dev-local
    host: localhost
    port: 1521
    service: xepdb1
    user: devuser
    credential:
      provider: plaintext
      value: devpass123
`

	if err := os.WriteFile(filepath.Join(dir, "prod.yaml"), []byte(prodYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev.yml"), []byte(devYAML), 0644); err != nil {
		t.Fatal(err)
	}
	// Write a non-YAML file that should be ignored
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}

	groups, err := LoadConnectionGroups(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}

	// Find the production group (order depends on directory listing)
	var prodGroup, devGroup *ConnectionGroup
	for i := range groups {
		switch groups[i].Group {
		case "production":
			prodGroup = &groups[i]
		case "development":
			devGroup = &groups[i]
		}
	}

	if prodGroup == nil {
		t.Fatal("production group not found")
	}
	if len(prodGroup.Tags) != 2 {
		t.Errorf("prod tags = %d, want 2", len(prodGroup.Tags))
	}
	if len(prodGroup.Connections) != 2 {
		t.Errorf("prod connections = %d, want 2", len(prodGroup.Connections))
	}
	if prodGroup.Connections[0].Name != "prod-primary" {
		t.Errorf("first connection name = %q, want %q", prodGroup.Connections[0].Name, "prod-primary")
	}
	if prodGroup.Connections[0].Host != "10.0.1.100" {
		t.Errorf("host = %q, want %q", prodGroup.Connections[0].Host, "10.0.1.100")
	}
	if prodGroup.Connections[0].Port != 1521 {
		t.Errorf("port = %d, want 1521", prodGroup.Connections[0].Port)
	}
	if prodGroup.Connections[0].Credential.Provider != "vault" {
		t.Errorf("credential provider = %q, want %q", prodGroup.Connections[0].Credential.Provider, "vault")
	}
	if prodGroup.Connections[0].Credential.VaultPath != "secret/data/prod-db" {
		t.Errorf("vault path = %q, want %q", prodGroup.Connections[0].Credential.VaultPath, "secret/data/prod-db")
	}

	if devGroup == nil {
		t.Fatal("development group not found")
	}
	if len(devGroup.Connections) != 1 {
		t.Errorf("dev connections = %d, want 1", len(devGroup.Connections))
	}
	if devGroup.Connections[0].Credential.Provider != "plaintext" {
		t.Errorf("credential provider = %q, want %q", devGroup.Connections[0].Credential.Provider, "plaintext")
	}
	if devGroup.Connections[0].Credential.Value != "devpass123" {
		t.Errorf("credential value = %q, want %q", devGroup.Connections[0].Credential.Value, "devpass123")
	}
}

func TestLoadConnectionGroups_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	groups, err := LoadConnectionGroups(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if groups == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(groups) != 0 {
		t.Errorf("got %d groups, want 0", len(groups))
	}
}

func TestLoadConnectionGroups_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(`{{{not yaml`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConnectionGroups(dir)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoadConnectionGroups_NonexistentDir(t *testing.T) {
	_, err := LoadConnectionGroups("/nonexistent/directory")
	if err == nil {
		t.Error("expected error for nonexistent directory, got nil")
	}
}
