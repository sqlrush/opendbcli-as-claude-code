/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  Package driver implements the db.Driver interface for HuaweiCloud
 *	  GaussDB Centralized V2.0-8.x using
 *	  HuaweiCloudDeveloper/gaussdb-go.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/driver/driver.go
 *
 *-------------------------------------------------------------------------
 */
// Package driver implements the db.Driver interface for HuaweiCloud GaussDB
// Centralized V2.0-8.x using HuaweiCloudDeveloper/gaussdb-go.
//
// Distinct from internal/opengauss/driver because the GaussDB SCRAM-SHA256(10)
// authentication is incompatible with stock pgx — the customer-side EOF
// "AuthenticationSASL body is invalid: unterminated string" originates there.
package driver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/HuaweiCloudDeveloper/gaussdb-go/stdlib"

	"github.com/sqlrush/opendb/internal/db"
)

// GaussDriver implements db.Driver for HuaweiCloud GaussDB Centralized.
type GaussDriver struct {
	conn       *sql.DB
	serverInfo db.ServerInfo
}

// NewDriver creates a new GaussDriver, validates config, and connects.
func NewDriver(cfg db.ConnectionConfig) (*GaussDriver, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid gaussdb config: %w", err)
	}

	dsn := buildDSN(cfg)
	conn, err := sql.Open("gaussdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open gaussdb connection: %w", err)
	}
	conn.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping gaussdb: %w", err)
	}

	info, err := fetchServerInfo(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to query server info: %w", err)
	}

	return &GaussDriver{conn: conn, serverInfo: info}, nil
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

// buildDSN builds a gaussdb-go keyword/value DSN.
//
// gaussdb-go accepts both URL form (`gaussdb://user:pwd@host:port/db`) and
// keyword/value form. We use keyword/value because Password may contain `@`
// or `/` which complicates URL escaping. Note: gaussdb-go uses `database=`
// for database name, NOT `dbname=` like libpq/pgx.
func buildDSN(cfg db.ConnectionConfig) string {
	database := cfg.Database
	if database == "" {
		database = cfg.Service
	}
	if database == "" {
		database = "postgres"
	}

	sslmode := "disable"
	if v, ok := cfg.Options["sslmode"]; ok {
		sslmode = v
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s database=%s sslmode=%s connect_timeout=15",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, database, sslmode)

	if v, ok := cfg.Options["search_path"]; ok {
		dsn += " search_path=" + v
	}
	if v, ok := cfg.Options["application_name"]; ok {
		dsn += " application_name=" + v
	} else {
		dsn += " application_name=opendb"
	}

	return dsn
}

func (d *GaussDriver) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := d.conn.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("gaussdb query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (d *GaussDriver) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := d.conn.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("gaussdb exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{RowsAffected: rowsAffected, Duration: time.Since(start)}, nil
}

func (d *GaussDriver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	sqlOpts := &sql.TxOptions{}
	if opts != nil {
		sqlOpts.ReadOnly = opts.ReadOnly
	}
	tx, err := d.conn.BeginTx(ctx, sqlOpts)
	if err != nil {
		return nil, fmt.Errorf("gaussdb begin tx failed: %w", err)
	}
	return &gaussTx{tx: tx}, nil
}

func (d *GaussDriver) Ping(ctx context.Context) error { return d.conn.PingContext(ctx) }
func (d *GaussDriver) ServerInfo() db.ServerInfo      { return d.serverInfo }
func (d *GaussDriver) Close() error                   { return d.conn.Close() }

type gaussTx struct{ tx *sql.Tx }

func (t *gaussTx) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := t.tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("gaussdb tx query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (t *gaussTx) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := t.tx.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("gaussdb tx exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{RowsAffected: rowsAffected, Duration: time.Since(start)}, nil
}

func (t *gaussTx) Commit() error   { return t.tx.Commit() }
func (t *gaussTx) Rollback() error { return t.tx.Rollback() }

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
	return &db.QueryResult{Columns: cols, Rows: resultRows, Duration: time.Since(start)}, nil
}

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

func fetchServerInfo(ctx context.Context, conn *sql.DB) (db.ServerInfo, error) {
	var info db.ServerInfo

	var rawVersion string
	conn.QueryRowContext(ctx, "SELECT version()").Scan(&rawVersion)
	info.Version = extractGaussVersion(rawVersion)

	conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&info.InstanceName)

	_ = conn.QueryRowContext(ctx, "SELECT inet_server_addr()::text").Scan(&info.Hostname)

	info.Platform = "linux"

	conn.QueryRowContext(ctx, "SELECT pg_postmaster_start_time()").Scan(&info.StartupTime)

	return info, nil
}

// extractGaussVersion parses version() output. GaussDB returns strings like:
//   "(GaussDB Kernel V500R002C20 build ...)" or
//   "PostgreSQL 9.2.4 ... (openGauss 5.0.0 build ...)" for OG-derived builds.
// Returns the most identifying token, or the raw string if unmatched.
func extractGaussVersion(raw string) string {
	lower := strings.ToLower(raw)
	for _, marker := range []string{"gaussdb kernel", "gaussdb", "opengauss"} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		sub := raw[idx:]
		parts := strings.Fields(sub)
		if marker == "gaussdb kernel" && len(parts) >= 3 {
			return parts[0] + " " + parts[1] + " " + parts[2]
		}
		if len(parts) >= 2 {
			return parts[0] + " " + parts[1]
		}
		return sub
	}
	return raw
}
