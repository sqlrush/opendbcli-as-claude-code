/*-------------------------------------------------------------------------
 *
 * lazydriver.go
 *	  LazyDriver implements db.Driver by delegating to the Manager's
 *	  current active connection. All methods return a NotConnectedError
 *	  if no connection is established.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/connection/lazydriver.go
 *
 *-------------------------------------------------------------------------
 */
package connection

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// LazyDriver implements db.Driver by delegating to the Manager's current
// active connection. All methods return a NotConnectedError if no connection
// is established.
type LazyDriver struct {
	manager *Manager
}

// NewLazyDriver creates a LazyDriver backed by the given Manager.
func NewLazyDriver(manager *Manager) *LazyDriver {
	return &LazyDriver{manager: manager}
}

func (d *LazyDriver) Query(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	driver, err := d.manager.Current()
	if err != nil {
		return nil, err
	}
	return driver.Query(ctx, sql, args...)
}

func (d *LazyDriver) Exec(ctx context.Context, sql string, args ...any) (*db.ExecResult, error) {
	driver, err := d.manager.Current()
	if err != nil {
		return nil, err
	}
	return driver.Exec(ctx, sql, args...)
}

func (d *LazyDriver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	driver, err := d.manager.Current()
	if err != nil {
		return nil, err
	}
	return driver.BeginTx(ctx, opts)
}

func (d *LazyDriver) Ping(ctx context.Context) error {
	driver, err := d.manager.Current()
	if err != nil {
		return err
	}
	return driver.Ping(ctx)
}

func (d *LazyDriver) Close() error {
	driver, err := d.manager.Current()
	if err != nil {
		return nil
	}
	return driver.Close()
}

func (d *LazyDriver) ServerInfo() db.ServerInfo {
	driver, err := d.manager.Current()
	if err != nil {
		return db.ServerInfo{}
	}
	return driver.ServerInfo()
}
