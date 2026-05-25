/*-------------------------------------------------------------------------
 *
 * driver_test.go
 *	  Test cases for driver.go (driver package):
 *	  TestNewDriver_InvalidConfig_EmptyHost,
 *	  TestNewDriver_InvalidConfig_ZeroPort,
 *	  TestNewDriver_InvalidConfig_NegativePort.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/driver/driver_test.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db"
)

func TestNewDriver_InvalidConfig_EmptyHost(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host: "",
		Port: 1521,
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for empty host")
	}
}

func TestNewDriver_InvalidConfig_ZeroPort(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host: "localhost",
		Port: 0,
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for zero port")
	}
}

func TestNewDriver_InvalidConfig_NegativePort(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host: "localhost",
		Port: -1,
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for negative port")
	}
}

func TestNewDriver_InvalidConfig_PortTooHigh(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host: "localhost",
		Port: 70000,
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for port > 65535")
	}
}

func TestNewDriver_InvalidConfig_NoServiceOrDatabase(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host: "localhost",
		Port: 1521,
		User: "admin",
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for missing service and database")
	}
}

func TestNewDriver_InvalidConfig_NoUser(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:    "localhost",
		Port:    1521,
		Service: "orcl",
		User:    "",
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for empty user")
	}
}

func TestBuildDSN_WithService(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:     "10.0.1.100",
		Port:     1521,
		Service:  "orcl",
		User:     "dbadmin",
		Password: "secret",
	}
	dsn := buildDSN(cfg)
	want := "oracle://dbadmin:secret@10.0.1.100:1521/orcl"
	if dsn != want {
		t.Errorf("buildDSN() = %q, want %q", dsn, want)
	}
}

func TestBuildDSN_WithSID(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:     "db.example.com",
		Port:     1522,
		Database: "MYDB",
		User:     "sys",
		Password: "p@ss",
	}
	dsn := buildDSN(cfg)
	want := "oracle://sys:p@ss@db.example.com:1522/MYDB"
	if dsn != want {
		t.Errorf("buildDSN() = %q, want %q", dsn, want)
	}
}

func TestBuildDSN_ServicePrecedence(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:     "host1",
		Port:     1521,
		Service:  "myservice",
		Database: "mysid",
		User:     "user1",
		Password: "pass1",
	}
	dsn := buildDSN(cfg)
	want := "oracle://user1:pass1@host1:1521/myservice"
	if dsn != want {
		t.Errorf("buildDSN() should prefer Service over Database, got %q, want %q", dsn, want)
	}
}

func TestBuildDSN_EmptyPassword(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:    "host1",
		Port:    1521,
		Service: "orcl",
		User:    "admin",
	}
	dsn := buildDSN(cfg)
	want := "oracle://admin:@host1:1521/orcl"
	if dsn != want {
		t.Errorf("buildDSN() = %q, want %q", dsn, want)
	}
}

func TestValidateConfig_Valid(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:    "localhost",
		Port:    1521,
		Service: "orcl",
		User:    "admin",
	}
	if err := validateConfig(cfg); err != nil {
		t.Errorf("expected valid config, got error: %v", err)
	}
}

func TestValidateConfig_ValidWithDatabase(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:     "localhost",
		Port:     1521,
		Database: "MYDB",
		User:     "admin",
	}
	if err := validateConfig(cfg); err != nil {
		t.Errorf("expected valid config with Database, got error: %v", err)
	}
}

// Compile-time check that OracleDriver implements db.Driver.
var _ db.Driver = (*OracleDriver)(nil)

// Compile-time check that OracleDriver implements db.Inspector.
var _ db.Inspector = (*OracleDriver)(nil)
