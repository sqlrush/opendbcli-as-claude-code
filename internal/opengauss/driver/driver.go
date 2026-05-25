/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  OGDriver implements db.Driver for OpenGauss databases using pgx.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/driver/driver.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sqlrush/opendb/internal/db"
)

// OGDriver implements db.Driver for OpenGauss databases using pgx.
type OGDriver struct {
	conn       *sql.DB
	serverInfo db.ServerInfo
}

// NewDriver creates a new OGDriver for OpenGauss, validates config, and connects.
func NewDriver(cfg db.ConnectionConfig) (*OGDriver, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid opengauss config: %w", err)
	}

	dsn := buildDSN(cfg)
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open opengauss connection: %w", err)
	}
	conn.SetMaxOpenConns(1) // CLI tool: single connection for session state persistence

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping opengauss: %w", err)
	}

	info, err := fetchServerInfo(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to query server info: %w", err)
	}

	return &OGDriver{conn: conn, serverInfo: info}, nil
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

// buildDSN builds an OpenGauss connection string.
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

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=15",
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

func (d *OGDriver) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := d.conn.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("opengauss query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (d *OGDriver) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := d.conn.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("opengauss exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{RowsAffected: rowsAffected, Duration: time.Since(start)}, nil
}

func (d *OGDriver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	sqlOpts := &sql.TxOptions{}
	if opts != nil {
		sqlOpts.ReadOnly = opts.ReadOnly
	}
	tx, err := d.conn.BeginTx(ctx, sqlOpts)
	if err != nil {
		return nil, fmt.Errorf("opengauss begin tx failed: %w", err)
	}
	return &pgTx{tx: tx}, nil
}

func (d *OGDriver) Ping(ctx context.Context) error  { return d.conn.PingContext(ctx) }
func (d *OGDriver) ServerInfo() db.ServerInfo        { return d.serverInfo }
func (d *OGDriver) Close() error                     { return d.conn.Close() }

type pgTx struct{ tx *sql.Tx }

func (t *pgTx) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := t.tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("opengauss tx query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (t *pgTx) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := t.tx.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("opengauss tx exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{RowsAffected: rowsAffected, Duration: time.Since(start)}, nil
}

func (t *pgTx) Commit() error  { return t.tx.Commit() }
func (t *pgTx) Rollback() error { return t.tx.Rollback() }

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
	info.Version = extractOpenGaussVersion(rawVersion)

	conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&info.InstanceName)

	// OpenGauss may not support inet_server_addr(); fall back gracefully.
	_ = conn.QueryRowContext(ctx, "SELECT inet_server_addr()::text").Scan(&info.Hostname)

	// Platform is embedded in version string
	info.Platform = "linux" // default, will be parsed from version()

	conn.QueryRowContext(ctx, "SELECT pg_postmaster_start_time()").Scan(&info.StartupTime)

	return info, nil
}

// extractOpenGaussVersion parses the version() output to extract the OpenGauss
// version. The output typically looks like:
// "PostgreSQL 9.2.4 (openGauss 5.0.0 build ...)"
// Returns "openGauss X.X.X" if found, otherwise the raw string.
func extractOpenGaussVersion(raw string) string {
	lower := strings.ToLower(raw)
	idx := strings.Index(lower, "opengauss")
	if idx < 0 {
		return raw
	}

	// Extract from "openGauss" onward using original case.
	sub := raw[idx:]
	// Find the end of the version token (e.g., "openGauss 5.0.0").
	// Walk past "openGauss ", then capture version digits+dots.
	parts := strings.Fields(sub)
	if len(parts) >= 2 {
		return parts[0] + " " + parts[1]
	}
	return sub
}
