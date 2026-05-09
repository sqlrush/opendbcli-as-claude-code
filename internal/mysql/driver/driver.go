/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  MySQLDriver implements db.Driver for MySQL databases.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/driver/driver.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/sqlrush/opendb/internal/db"
)

func init() {
	// Silence go-sql-driver/mysql logging — prevents stderr pollution
	// that corrupts the REPL display (e.g., "unexpected EOF" on disconnect).
	mysql.SetLogger(log.New(io.Discard, "", 0))
}

// MySQLDriver implements db.Driver for MySQL databases.
type MySQLDriver struct {
	conn       *sql.DB
	serverInfo db.ServerInfo
}

// NewDriver creates a new MySQLDriver, validates config, and connects.
func NewDriver(cfg db.ConnectionConfig) (*MySQLDriver, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid mysql config: %w", err)
	}

	dsn := buildDSN(cfg)
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open mysql connection: %w", err)
	}
	// Single connection — CLI tool needs session state persistence
	// (USE database, SET variables, temp tables, etc.)
	conn.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping mysql: %w", err)
	}

	info, err := fetchServerInfo(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to query server info: %w", err)
	}

	return &MySQLDriver{
		conn:       conn,
		serverInfo: info,
	}, nil
}

func validateConfig(cfg db.ConnectionConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("host is required")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", cfg.Port)
	}
	if cfg.User == "" {
		return fmt.Errorf("user is required")
	}
	return nil
}

// buildDSN builds a go-sql-driver/mysql DSN string.
// Format: user:password@tcp(host:port)/database?params
func buildDSN(cfg db.ConnectionConfig) string {
	database := cfg.Database
	if database == "" {
		database = cfg.Service // fallback: some configs may use Service field
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&timeout=15s&readTimeout=30s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, database)

	if charset, ok := cfg.Options["charset"]; ok {
		dsn += "&charset=" + charset
	} else {
		dsn += "&charset=utf8mb4"
	}

	if sslMode, ok := cfg.Options["ssl-mode"]; ok {
		dsn += "&tls=" + sslMode
	}

	return dsn
}

func (d *MySQLDriver) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := d.conn.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (d *MySQLDriver) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := d.conn.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{
		RowsAffected: rowsAffected,
		Duration:     time.Since(start),
	}, nil
}

func (d *MySQLDriver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	sqlOpts := &sql.TxOptions{}
	if opts != nil {
		sqlOpts.ReadOnly = opts.ReadOnly
	}
	tx, err := d.conn.BeginTx(ctx, sqlOpts)
	if err != nil {
		return nil, fmt.Errorf("mysql begin tx failed: %w", err)
	}
	return &mysqlTx{tx: tx}, nil
}

func (d *MySQLDriver) Ping(ctx context.Context) error {
	return d.conn.PingContext(ctx)
}

func (d *MySQLDriver) ServerInfo() db.ServerInfo {
	return d.serverInfo
}

func (d *MySQLDriver) Close() error {
	return d.conn.Close()
}

type mysqlTx struct {
	tx *sql.Tx
}

func (t *mysqlTx) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := t.tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql tx query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (t *mysqlTx) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := t.tx.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql tx exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{
		RowsAffected: rowsAffected,
		Duration:     time.Since(start),
	}, nil
}

func (t *mysqlTx) Commit() error  { return t.tx.Commit() }
func (t *mysqlTx) Rollback() error { return t.tx.Rollback() }

// scanRows converts sql.Rows to db.QueryResult.
func scanRows(rows *sql.Rows, start time.Time) (*db.QueryResult, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	var resultRows [][]any
	for rows.Next() {
		scanDest := make([]any, len(cols))
		scanPtrs := make([]any, len(cols))
		for i := range scanDest {
			scanPtrs[i] = &scanDest[i]
		}
		if err := rows.Scan(scanPtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		converted := make([]any, len(cols))
		for i, v := range scanDest {
			converted[i] = convertValue(v)
		}
		resultRows = append(resultRows, converted)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return &db.QueryResult{
		Columns:  cols,
		Rows:     resultRows,
		Duration: time.Since(start),
	}, nil
}

// convertValue normalizes MySQL driver types to Go primitives.
func convertValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []byte:
		return string(val)
	default:
		return val
	}
}

// fetchServerInfo queries MySQL instance metadata at connect time.
func fetchServerInfo(ctx context.Context, conn *sql.DB) (db.ServerInfo, error) {
	var info db.ServerInfo

	// Version
	conn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&info.Version)

	// Hostname
	conn.QueryRowContext(ctx, "SELECT @@hostname").Scan(&info.Hostname)

	// Instance name (use @@hostname as MySQL has no separate instance concept)
	info.InstanceName = info.Hostname

	// Platform
	conn.QueryRowContext(ctx, "SELECT @@version_compile_os").Scan(&info.Platform)

	// Startup time (derived from Uptime)
	var uptime int64
	if err := conn.QueryRowContext(ctx,
		"SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Uptime'",
	).Scan(&uptime); err == nil {
		info.StartupTime = time.Now().Add(-time.Duration(uptime) * time.Second)
	}

	return info, nil
}
