/*-------------------------------------------------------------------------
 *
 * driver_test.go
 *	  Test cases for driver.go (mock package):
 *	  TestMockDriver_DefaultQuery, TestMockDriver_CustomQuery,
 *	  TestMockDriver_QueryError.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/db/mock/driver_test.go
 *
 *-------------------------------------------------------------------------
 */
package mock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// Compile-time check that Driver satisfies db.Driver.
var _ db.Driver = (*Driver)(nil)

func TestMockDriver_DefaultQuery(t *testing.T) {
	d := NewMockDriver()
	result, err := d.Query(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Columns) != 0 {
		t.Errorf("expected 0 columns, got %d", len(result.Columns))
	}
	if len(result.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result.Rows))
	}
}

func TestMockDriver_CustomQuery(t *testing.T) {
	d := NewMockDriver()
	d.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns:  []string{"id", "name"},
			Rows:     [][]any{{1, "alice"}, {2, "bob"}},
			Duration: 50 * time.Millisecond,
		}, nil
	}

	result, err := d.Query(context.Background(), "SELECT id, name FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(result.Columns))
	}
	if len(result.Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Columns[0] != "id" || result.Columns[1] != "name" {
		t.Errorf("unexpected columns: %v", result.Columns)
	}
}

func TestMockDriver_QueryError(t *testing.T) {
	d := NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("query failed")
	}

	_, err := d.Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "query failed" {
		t.Errorf("expected %q, got %q", "query failed", err.Error())
	}
}

func TestMockDriver_DefaultExec(t *testing.T) {
	d := NewMockDriver()
	result, err := d.Exec(context.Background(), "DELETE FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.RowsAffected != 0 {
		t.Errorf("expected 0 rows affected, got %d", result.RowsAffected)
	}
}

func TestMockDriver_CustomExec(t *testing.T) {
	d := NewMockDriver()
	d.ExecFunc = func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
		return &db.ExecResult{
			RowsAffected: 5,
			Duration:     10 * time.Millisecond,
		}, nil
	}

	result, err := d.Exec(context.Background(), "DELETE FROM users WHERE active = false")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowsAffected != 5 {
		t.Errorf("expected 5 rows affected, got %d", result.RowsAffected)
	}
}

func TestMockDriver_ServerInfo(t *testing.T) {
	d := NewMockDriver()
	d.ServerInfoVal = db.ServerInfo{
		Version:      "19.3.0.0.0",
		InstanceName: "ORCL",
		Hostname:     "db-host-01",
		Platform:     "Linux x86 64-bit",
		StartupTime:  time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC),
	}

	info := d.ServerInfo()
	if info.Version != "19.3.0.0.0" {
		t.Errorf("expected version %q, got %q", "19.3.0.0.0", info.Version)
	}
	if info.InstanceName != "ORCL" {
		t.Errorf("expected instance %q, got %q", "ORCL", info.InstanceName)
	}
	if info.Hostname != "db-host-01" {
		t.Errorf("expected hostname %q, got %q", "db-host-01", info.Hostname)
	}
	if info.Platform != "Linux x86 64-bit" {
		t.Errorf("expected platform %q, got %q", "Linux x86 64-bit", info.Platform)
	}
}

func TestMockDriver_DefaultPing(t *testing.T) {
	d := NewMockDriver()
	err := d.Ping(context.Background())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMockDriver_CustomPing(t *testing.T) {
	d := NewMockDriver()
	d.PingFunc = func(_ context.Context) error {
		return errors.New("connection lost")
	}

	err := d.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "connection lost" {
		t.Errorf("expected %q, got %q", "connection lost", err.Error())
	}
}

func TestMockDriver_DefaultClose(t *testing.T) {
	d := NewMockDriver()
	err := d.Close()
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMockDriver_CustomClose(t *testing.T) {
	d := NewMockDriver()
	d.CloseFunc = func() error {
		return errors.New("close failed")
	}

	err := d.Close()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMockDriver_DefaultBeginTx(t *testing.T) {
	d := NewMockDriver()
	tx, err := d.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx == nil {
		t.Fatal("expected non-nil tx")
	}
}

func TestMockDriver_DefaultServerInfo_ZeroValue(t *testing.T) {
	d := NewMockDriver()
	info := d.ServerInfo()
	if info.Version != "" {
		t.Errorf("expected empty version, got %q", info.Version)
	}
	if info.InstanceName != "" {
		t.Errorf("expected empty instance, got %q", info.InstanceName)
	}
}
