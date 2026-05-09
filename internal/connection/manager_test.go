/*-------------------------------------------------------------------------
 *
 * manager_test.go
 *	  Test cases for manager.go (connection package):
 *	  TestNewManager_NilConfig, TestNewManager_LoadsConfig,
 *	  TestNewManager_EmptyDir.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/connection/manager_test.go
 *
 *-------------------------------------------------------------------------
 */
package connection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	dberrors "github.com/sqlrush/opendb/internal/errors"
)

// testConfig creates a config pointing to a temporary connections directory
// with the given YAML files written into it.
func testConfig(t *testing.T, yamlFiles map[string]string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	connDir := filepath.Join(dir, "connections")
	if err := os.MkdirAll(connDir, 0o755); err != nil {
		t.Fatalf("creating connections dir: %v", err)
	}

	for name, content := range yamlFiles {
		path := filepath.Join(connDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return &config.Config{
		ConnectionsDir: connDir,
	}
}

// mockDriverFactory returns a DriverFactory that produces mock drivers.
func mockDriverFactory() DriverFactory {
	return func(cfg db.ConnectionConfig) (db.Driver, error) {
		d := mock.NewMockDriver()
		d.ServerInfoVal = db.ServerInfo{
			Version:      "19.3.0.0.0",
			InstanceName: "MOCK",
			Hostname:     cfg.Host,
		}
		return d, nil
	}
}

// failingDriverFactory returns a DriverFactory that always fails.
func failingDriverFactory(msg string) DriverFactory {
	return func(_ db.ConnectionConfig) (db.Driver, error) {
		return nil, errors.New(msg)
	}
}

// inlineProvider is a credential.Provider that returns a fixed password.
type inlineProvider struct {
	password string
}

func (p *inlineProvider) Resolve(_ string) (string, error) {
	return p.password, nil
}

const testYAML = `group: dev
tags: [development]
connections:
  - name: dev-db
    host: localhost
    port: 1521
    service: XEPDB1
    user: devuser
    credential:
      provider: inline
      value: devpass
  - name: staging-db
    host: staging.example.com
    port: 1521
    service: STGPDB1
    user: stguser
    credential:
      provider: inline
      value: stgpass
`

const testYAML2 = `group: prod
tags: [production]
connections:
  - name: prod-db
    host: prod.example.com
    port: 1521
    service: PRDPDB1
    user: prduser
    credential:
      provider: inline
      value: prdpass
`

func TestNewManager_NilConfig(t *testing.T) {
	_, err := NewManager(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewManager_LoadsConfig(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conns := mgr.ListConnections()
	if len(conns) != 2 {
		t.Errorf("expected 2 connections, got %d", len(conns))
	}
}

func TestNewManager_EmptyDir(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conns := mgr.ListConnections()
	if len(conns) != 0 {
		t.Errorf("expected 0 connections, got %d", len(conns))
	}
}

func TestNewManager_MissingDir(t *testing.T) {
	cfg := &config.Config{
		ConnectionsDir: "/nonexistent/path/connections",
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should start with empty groups when directory is missing.
	conns := mgr.ListConnections()
	if len(conns) != 0 {
		t.Errorf("expected 0 connections, got %d", len(conns))
	}
}

func TestConnect_UnknownConnection(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = mgr.Connect(context.Background(), "nonexistent-db")
	if err == nil {
		t.Fatal("expected error for unknown connection")
	}
	if got := err.Error(); got != `connection "nonexistent-db" not found` {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestConnect_Success(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = mgr.Connect(context.Background(), "dev-db")
	if err != nil {
		t.Fatalf("unexpected connect error: %v", err)
	}

	driver, err := mgr.Current()
	if err != nil {
		t.Fatalf("unexpected error from Current: %v", err)
	}
	if driver == nil {
		t.Fatal("expected non-nil driver")
	}

	info := mgr.CurrentInfo()
	if info == nil {
		t.Fatal("expected non-nil server info")
	}
	if info.Hostname != "localhost" {
		t.Errorf("expected hostname %q, got %q", "localhost", info.Hostname)
	}
}

func TestConnect_DriverFactoryError(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(failingDriverFactory("connection refused")),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = mgr.Connect(context.Background(), "dev-db")
	if err == nil {
		t.Fatal("expected error from failing driver factory")
	}
}

func TestConnect_ClosesExistingConnection(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})

	closeCalled := false
	factory := func(cfg db.ConnectionConfig) (db.Driver, error) {
		d := mock.NewMockDriver()
		d.CloseFunc = func() error {
			closeCalled = true
			return nil
		}
		d.ServerInfoVal = db.ServerInfo{Hostname: cfg.Host}
		return d, nil
	}

	mgr, err := NewManager(cfg,
		WithDriverFactory(factory),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Connect to first.
	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("first connect: %v", err)
	}

	// Connect to second should close the first.
	if err := mgr.Connect(context.Background(), "staging-db"); err != nil {
		t.Fatalf("second connect: %v", err)
	}

	if !closeCalled {
		t.Error("expected Close to be called on previous driver")
	}
}

func TestCurrent_NotConnected(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = mgr.Current()
	if err == nil {
		t.Fatal("expected NotConnectedError")
	}

	var notConnErr *dberrors.NotConnectedError
	if !errors.As(err, &notConnErr) {
		t.Errorf("expected NotConnectedError, got %T: %v", err, err)
	}
}

func TestCurrentInfo_NotConnected(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info := mgr.CurrentInfo()
	if info != nil {
		t.Errorf("expected nil server info when not connected, got %+v", info)
	}
}

func TestCurrentName_NotConnected(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	name := mgr.CurrentName()
	if name != "" {
		t.Errorf("expected empty name when not connected, got %q", name)
	}
}

func TestCurrentName_Connected(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if got := mgr.CurrentName(); got != "dev-db" {
		t.Errorf("expected %q, got %q", "dev-db", got)
	}
}

func TestDisconnect(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := mgr.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	_, err = mgr.Current()
	var notConnErr *dberrors.NotConnectedError
	if !errors.As(err, &notConnErr) {
		t.Errorf("expected NotConnectedError after disconnect, got %T: %v", err, err)
	}
}

func TestDisconnect_WhenNotConnected(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Disconnect when not connected should be a no-op.
	if err := mgr.Disconnect(); err != nil {
		t.Errorf("expected no error on disconnect when not connected, got %v", err)
	}
}

func TestListConnections(t *testing.T) {
	cfg := testConfig(t, map[string]string{
		"dev.yaml":  testYAML,
		"prod.yaml": testYAML2,
	})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conns := mgr.ListConnections()
	if len(conns) != 3 {
		t.Fatalf("expected 3 connections, got %d", len(conns))
	}

	names := make(map[string]bool)
	for _, c := range conns {
		names[c.Name] = true
	}
	for _, expected := range []string{"dev-db", "staging-db", "prod-db"} {
		if !names[expected] {
			t.Errorf("expected connection %q in list", expected)
		}
	}
}

func TestRecentConnections(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Initially empty.
	recent := mgr.RecentConnections()
	if len(recent) != 0 {
		t.Errorf("expected 0 recent, got %d", len(recent))
	}

	// Connect to dev-db.
	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("connect dev-db: %v", err)
	}

	recent = mgr.RecentConnections()
	if len(recent) != 1 || recent[0] != "dev-db" {
		t.Errorf("expected [dev-db], got %v", recent)
	}

	// Connect to staging-db.
	if err := mgr.Connect(context.Background(), "staging-db"); err != nil {
		t.Fatalf("connect staging-db: %v", err)
	}

	recent = mgr.RecentConnections()
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent, got %d", len(recent))
	}
	if recent[0] != "staging-db" {
		t.Errorf("expected most recent to be staging-db, got %s", recent[0])
	}
	if recent[1] != "dev-db" {
		t.Errorf("expected second recent to be dev-db, got %s", recent[1])
	}
}

func TestRecentConnections_NoDuplicates(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Connect twice to the same.
	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if err := mgr.Connect(context.Background(), "staging-db"); err != nil {
		t.Fatalf("second connect: %v", err)
	}
	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("third connect: %v", err)
	}

	recent := mgr.RecentConnections()
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent (no duplicates), got %d: %v", len(recent), recent)
	}
	if recent[0] != "dev-db" {
		t.Errorf("most recent should be dev-db, got %s", recent[0])
	}
}

func TestRecentConnections_ReturnsDefensiveCopy(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	recent := mgr.RecentConnections()
	recent[0] = "tampered"

	// Internal state should be unaffected.
	internal := mgr.RecentConnections()
	if internal[0] != "dev-db" {
		t.Errorf("expected internal state to be unaffected, got %s", internal[0])
	}
}

func TestReconnectIfReadOnly_ReadOnly(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Connect first.
	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Simulate reconnect for a read-only operation.
	err = mgr.ReconnectIfReadOnly(context.Background(), true)
	if err != nil {
		t.Fatalf("expected silent reconnect to succeed, got: %v", err)
	}
}

func TestReconnectIfReadOnly_Write(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "test"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Connect first.
	if err := mgr.Connect(context.Background(), "dev-db"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Reconnect for write should require confirmation.
	err = mgr.ReconnectIfReadOnly(context.Background(), false)
	if err == nil {
		t.Fatal("expected error for write operation reconnect")
	}
}

func TestReconnect_NoPreviousConnection(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = mgr.reconnect(context.Background())
	if err == nil {
		t.Fatal("expected error when no previous connection")
	}
}

func TestResolveCredential_InlineValue(t *testing.T) {
	cfg := testConfig(t, map[string]string{"dev.yaml": testYAML})
	mgr, err := NewManager(cfg,
		WithDriverFactory(mockDriverFactory()),
		WithProvider("inline", &inlineProvider{password: "ignored"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conn := config.Connection{
		Name: "test-conn",
		Credential: config.Credential{
			Value: "direct-password",
		},
	}

	password, err := mgr.resolveCredential(conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if password != "direct-password" {
		t.Errorf("expected %q, got %q", "direct-password", password)
	}
}

func TestResolveCredential_UnknownProvider(t *testing.T) {
	cfg := testConfig(t, map[string]string{})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conn := config.Connection{
		Name: "test-conn",
		Credential: config.Credential{
			Provider: "vault",
		},
	}

	_, err = mgr.resolveCredential(conn)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
