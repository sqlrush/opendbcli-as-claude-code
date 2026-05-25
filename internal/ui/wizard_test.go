/*-------------------------------------------------------------------------
 *
 * wizard_test.go
 *	  Test cases for wizard.go (ui package): TestWizard_Run_FullInput,
 *	  TestWizard_Run_DefaultPort, TestWizard_Run_CustomPort.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/wizard_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"gopkg.in/yaml.v3"
)

func TestWizard_Run_FullInput(t *testing.T) {
	input := "prod-db1\n192.168.1.100\n1521\nORCL\nsystem\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	cfg, conn, err := w.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify config fields.
	if cfg.ConnectionsDir != filepath.Join(dir, "connections") {
		t.Errorf("ConnectionsDir = %q, want %q", cfg.ConnectionsDir, filepath.Join(dir, "connections"))
	}
	if cfg.Output.Format != "terminal" {
		t.Errorf("Output.Format = %q, want %q", cfg.Output.Format, "terminal")
	}
	if cfg.Output.MaxRows != 1000 {
		t.Errorf("Output.MaxRows = %d, want %d", cfg.Output.MaxRows, 1000)
	}
	if cfg.Security.ConfirmOnDangerous != true {
		t.Error("Security.ConfirmOnDangerous should be true")
	}

	// Verify connection fields.
	if conn.Name != "prod-db1" {
		t.Errorf("Name = %q, want %q", conn.Name, "prod-db1")
	}
	if conn.Host != "192.168.1.100" {
		t.Errorf("Host = %q, want %q", conn.Host, "192.168.1.100")
	}
	if conn.Port != 1521 {
		t.Errorf("Port = %d, want %d", conn.Port, 1521)
	}
	if conn.Service != "ORCL" {
		t.Errorf("Service = %q, want %q", conn.Service, "ORCL")
	}
	if conn.User != "system" {
		t.Errorf("User = %q, want %q", conn.User, "system")
	}
	if conn.Credential.Provider != "prompt" {
		t.Errorf("Credential.Provider = %q, want %q", conn.Credential.Provider, "prompt")
	}

	// Verify output contains welcome and success messages.
	output := buf.String()
	if !strings.Contains(output, "Welcome to OpenDB") {
		t.Error("output should contain welcome message")
	}
	if !strings.Contains(output, "Setup complete!") {
		t.Error("output should contain success message")
	}
}

func TestWizard_Run_DefaultPort(t *testing.T) {
	// Empty line for port should use default 1521.
	input := "dev-db\n10.0.0.5\n\nXE\nscott\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, conn, err := w.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conn.Port != 1521 {
		t.Errorf("Port = %d, want default %d", conn.Port, 1521)
	}
	if conn.Name != "dev-db" {
		t.Errorf("Name = %q, want %q", conn.Name, "dev-db")
	}
}

func TestWizard_Run_CustomPort(t *testing.T) {
	input := "staging\n10.0.0.10\n1522\nSTAGE\nadmin\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, conn, err := w.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conn.Port != 1522 {
		t.Errorf("Port = %d, want %d", conn.Port, 1522)
	}
}

func TestWizard_Run_FilesExistAndParse(t *testing.T) {
	input := "test-db\n127.0.0.1\n1521\nTESTDB\ntestuser\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, _, err := w.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify config.yaml exists and parses.
	configPath := filepath.Join(dir, "config.yaml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.yaml not found: %v", err)
	}

	var parsedCfg config.Config
	if err := yaml.Unmarshal(configData, &parsedCfg); err != nil {
		t.Fatalf("config.yaml parse error: %v", err)
	}
	if parsedCfg.ConnectionsDir != filepath.Join(dir, "connections") {
		t.Errorf("parsed ConnectionsDir = %q, want %q", parsedCfg.ConnectionsDir, filepath.Join(dir, "connections"))
	}

	// Verify default.yaml exists and parses.
	connPath := filepath.Join(dir, "connections", "default.yaml")
	connData, err := os.ReadFile(connPath)
	if err != nil {
		t.Fatalf("default.yaml not found: %v", err)
	}

	var parsedGroup config.ConnectionGroup
	if err := yaml.Unmarshal(connData, &parsedGroup); err != nil {
		t.Fatalf("default.yaml parse error: %v", err)
	}
	if parsedGroup.Group != "default" {
		t.Errorf("Group = %q, want %q", parsedGroup.Group, "default")
	}
	if len(parsedGroup.Connections) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(parsedGroup.Connections))
	}

	conn := parsedGroup.Connections[0]
	if conn.Name != "test-db" {
		t.Errorf("Name = %q, want %q", conn.Name, "test-db")
	}
	if conn.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want %q", conn.Host, "127.0.0.1")
	}
	if conn.User != "testuser" {
		t.Errorf("User = %q, want %q", conn.User, "testuser")
	}
}

func TestWizard_Run_EmptyName_Error(t *testing.T) {
	// Empty connection name should fail.
	input := "\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, _, err := w.Run()
	if err == nil {
		t.Fatal("expected error for empty connection name")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("error = %q, want it to mention 'cannot be empty'", err.Error())
	}
}

func TestWizard_Run_InvalidPort_Error(t *testing.T) {
	input := "mydb\n10.0.0.1\nabc\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, _, err := w.Run()
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	if !strings.Contains(err.Error(), "invalid port") {
		t.Errorf("error = %q, want it to mention 'invalid port'", err.Error())
	}
}

func TestWizard_Run_PortOutOfRange_Error(t *testing.T) {
	input := "mydb\n10.0.0.1\n99999\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, _, err := w.Run()
	if err == nil {
		t.Fatal("expected error for out-of-range port")
	}
	if !strings.Contains(err.Error(), "port must be between") {
		t.Errorf("error = %q, want it to mention 'port must be between'", err.Error())
	}
}

func TestWizard_Run_TruncatedInput_Error(t *testing.T) {
	// Only provide connection name, then EOF.
	input := "mydb\n"
	reader := strings.NewReader(input)
	var buf bytes.Buffer

	dir := t.TempDir()
	w := NewWizard(reader, &buf, dir)
	_, _, err := w.Run()
	if err == nil {
		t.Fatal("expected error for truncated input")
	}
}
