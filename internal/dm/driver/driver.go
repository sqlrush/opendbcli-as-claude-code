/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  Package driver implements the db.Driver interface for Dameng (DM)
 *	  database using the official dm-go-driver (vendored at
 *	  internal/_dmdriver).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/driver/driver.go
 *
 *-------------------------------------------------------------------------
 */
// Package driver implements the db.Driver interface for Dameng (DM) database
// using the official dm-go-driver (vendored at internal/_dmdriver).
//
// Critical platform note: dm-go-driver's security/ subpackage has only
// zzg_linux.go and zzh_windows.go — no darwin implementation. macOS clients
// cannot connect to DM. dbaa must be cross-compiled to Linux for DM use.
package driver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "dm" // vendored DM driver, registered as "dm" SQL driver

	"github.com/sqlrush/opendb/internal/db"
)

// DMDriver implements db.Driver for Dameng database.
type DMDriver struct {
	conn       *sql.DB
	serverInfo db.ServerInfo
}

// NewDriver creates a new DMDriver, validates config, and connects.
func NewDriver(cfg db.ConnectionConfig) (*DMDriver, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid dm config: %w", err)
	}

	dsn := buildDSN(cfg)
	conn, err := sql.Open("dm", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open dm connection: %w", err)
	}
	conn.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping dm: %w", err)
	}

	info, err := fetchServerInfo(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to query server info: %w", err)
	}

	return &DMDriver{conn: conn, serverInfo: info}, nil
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

// buildDSN builds a dm-go-driver DSN. URL form: dm://user:pass@host:port?...
// Password may contain '@' or '/' — URL-encode the password to avoid parse errors.
func buildDSN(cfg db.ConnectionConfig) string {
	pwd := url.QueryEscape(cfg.Password)
	dsn := fmt.Sprintf("dm://%s:%s@%s:%d", cfg.User, pwd, cfg.Host, cfg.Port)

	params := []string{}
	if v, ok := cfg.Options["schema"]; ok && v != "" {
		params = append(params, "schema="+v)
	}
	if v, ok := cfg.Options["appName"]; ok && v != "" {
		params = append(params, "appName="+v)
	} else {
		params = append(params, "appName=opendb")
	}
	if len(params) > 0 {
		dsn += "?" + strings.Join(params, "&")
	}
	return dsn
}

func (d *DMDriver) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := d.conn.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("dm query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (d *DMDriver) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := d.conn.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("dm exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{RowsAffected: rowsAffected, Duration: time.Since(start)}, nil
}

func (d *DMDriver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	sqlOpts := &sql.TxOptions{}
	if opts != nil {
		sqlOpts.ReadOnly = opts.ReadOnly
	}
	tx, err := d.conn.BeginTx(ctx, sqlOpts)
	if err != nil {
		return nil, fmt.Errorf("dm begin tx failed: %w", err)
	}
	return &dmTx{tx: tx}, nil
}

func (d *DMDriver) Ping(ctx context.Context) error { return d.conn.PingContext(ctx) }
func (d *DMDriver) ServerInfo() db.ServerInfo      { return d.serverInfo }
func (d *DMDriver) Close() error                   { return d.conn.Close() }

type dmTx struct{ tx *sql.Tx }

func (t *dmTx) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()
	rows, err := t.tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("dm tx query failed: %w", err)
	}
	defer rows.Close()
	return scanRows(rows, start)
}

func (t *dmTx) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()
	result, err := t.tx.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("dm tx exec failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{RowsAffected: rowsAffected, Duration: time.Since(start)}, nil
}

func (t *dmTx) Commit() error   { return t.tx.Commit() }
func (t *dmTx) Rollback() error { return t.tx.Rollback() }

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

	// V$INSTANCE has NAME, INSTANCE_NAME, BUILD_VERSION, START_TIME on DM 8.x
	row := conn.QueryRowContext(ctx, "SELECT INSTANCE_NAME, BUILD_VERSION, START_TIME FROM V$INSTANCE")
	var startTime sql.NullTime
	if err := row.Scan(&info.InstanceName, &info.Version, &startTime); err != nil {
		// Best-effort; some DM builds may have different column names.
		return info, nil
	}
	if startTime.Valid {
		info.StartupTime = startTime.Time
	}
	info.Platform = "linux"
	return info, nil
}
