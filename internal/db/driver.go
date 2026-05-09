/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  Database driver implementation for the db package — adapts the
 *	  underlying SQL driver to the db.Driver contract.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/db/driver.go
 *
 *-------------------------------------------------------------------------
 */
package db

import "context"

type Driver interface {
	Close() error
	Query(ctx context.Context, sql string, args ...any) (*QueryResult, error)
	Exec(ctx context.Context, sql string, args ...any) (*ExecResult, error)
	BeginTx(ctx context.Context, opts *TxOptions) (Tx, error)
	Ping(ctx context.Context) error
	ServerInfo() ServerInfo
}

type Tx interface {
	Query(ctx context.Context, sql string, args ...any) (*QueryResult, error)
	Exec(ctx context.Context, sql string, args ...any) (*ExecResult, error)
	Commit() error
	Rollback() error
}

type Inspector interface {
	Databases(ctx context.Context) ([]string, error)
	Schemas(ctx context.Context) ([]string, error)
	Tables(ctx context.Context, schema string) ([]TableInfo, error)
	Columns(ctx context.Context, schema, table string) ([]ColumnInfo, error)
}
