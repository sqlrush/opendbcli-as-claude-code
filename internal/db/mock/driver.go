/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  Driver is a mock db.Driver for unit testing. Callers set the
 *	  function fields to control behavior. If a function field is nil,
 *	  the method returns a sensible default.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/db/mock/driver.go
 *
 *-------------------------------------------------------------------------
 */
package mock

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// Driver is a mock db.Driver for unit testing.
// Callers set the function fields to control behavior.
// If a function field is nil, the method returns a sensible default.
type Driver struct {
	QueryFunc     func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
	ExecFunc      func(ctx context.Context, sql string, args ...any) (*db.ExecResult, error)
	BeginTxFunc   func(ctx context.Context, opts *db.TxOptions) (db.Tx, error)
	PingFunc      func(ctx context.Context) error
	CloseFunc     func() error
	ServerInfoVal db.ServerInfo
}

// NewMockDriver returns a new Driver with default (no-op) implementations.
func NewMockDriver() *Driver {
	return &Driver{}
}

// Query delegates to QueryFunc if set, otherwise returns an empty QueryResult.
func (d *Driver) Query(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	if d.QueryFunc != nil {
		return d.QueryFunc(ctx, sql, args...)
	}
	return &db.QueryResult{}, nil
}

// Exec delegates to ExecFunc if set, otherwise returns an empty ExecResult.
func (d *Driver) Exec(ctx context.Context, sql string, args ...any) (*db.ExecResult, error) {
	if d.ExecFunc != nil {
		return d.ExecFunc(ctx, sql, args...)
	}
	return &db.ExecResult{}, nil
}

// BeginTx delegates to BeginTxFunc if set, otherwise returns a mock transaction.
func (d *Driver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	if d.BeginTxFunc != nil {
		return d.BeginTxFunc(ctx, opts)
	}
	return &mockTx{}, nil
}

// Ping delegates to PingFunc if set, otherwise returns nil.
func (d *Driver) Ping(ctx context.Context) error {
	if d.PingFunc != nil {
		return d.PingFunc(ctx)
	}
	return nil
}

// Close delegates to CloseFunc if set, otherwise returns nil.
func (d *Driver) Close() error {
	if d.CloseFunc != nil {
		return d.CloseFunc()
	}
	return nil
}

// ServerInfo returns the ServerInfoVal field.
func (d *Driver) ServerInfo() db.ServerInfo {
	return d.ServerInfoVal
}

// mockTx is a no-op implementation of db.Tx for the mock driver.
type mockTx struct {
	QueryFunc  func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
	ExecFunc   func(ctx context.Context, sql string, args ...any) (*db.ExecResult, error)
	CommitFunc func() error
}

func (t *mockTx) Query(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	if t.QueryFunc != nil {
		return t.QueryFunc(ctx, sql, args...)
	}
	return &db.QueryResult{}, nil
}

func (t *mockTx) Exec(ctx context.Context, sql string, args ...any) (*db.ExecResult, error) {
	if t.ExecFunc != nil {
		return t.ExecFunc(ctx, sql, args...)
	}
	return &db.ExecResult{}, nil
}

func (t *mockTx) Commit() error {
	if t.CommitFunc != nil {
		return t.CommitFunc()
	}
	return nil
}

func (t *mockTx) Rollback() error {
	return nil
}
