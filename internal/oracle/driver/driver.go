/*-------------------------------------------------------------------------
 *
 * driver.go
 *	  OracleDriver implements db.Driver for Oracle databases using
 *	  go-ora.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/driver/driver.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	// Register the go-ora driver for database/sql.
	_ "github.com/sijms/go-ora/v2"

	"github.com/sqlrush/opendb/internal/db"
)

// OracleDriver implements db.Driver for Oracle databases using go-ora.
type OracleDriver struct {
	conn       *sql.DB
	serverInfo db.ServerInfo
}

// NewDriver creates a new OracleDriver, validates the configuration, and connects.
// It queries server info at connect time and caches it.
func NewDriver(cfg db.ConnectionConfig) (*OracleDriver, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid oracle config: %w", err)
	}

	dsn := buildDSN(cfg)
	conn, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open oracle connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping oracle: %w", err)
	}

	info, err := fetchServerInfo(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to query server info: %w", err)
	}

	return &OracleDriver{
		conn:       conn,
		serverInfo: info,
	}, nil
}

// validateConfig checks that required connection fields are present.
func validateConfig(cfg db.ConnectionConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("host is required")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", cfg.Port)
	}
	if cfg.Service == "" && cfg.Database == "" {
		return fmt.Errorf("service name or database (SID) is required")
	}
	// OS auth and wallet auth don't require a username.
	if cfg.User == "" && cfg.Options["AUTH TYPE"] != "OS" && cfg.Options["WALLET"] == "" {
		return fmt.Errorf("user is required")
	}
	return nil
}

// buildDSN builds a go-ora connection string from the given configuration.
// Format: oracle://user:password@host:port/service?options
// If Service is empty but Database is set, it uses the SID-based format.
// Supports privilege (sysdba/sysoper) and driver-specific Options.
func buildDSN(cfg db.ConnectionConfig) string {
	identifier := cfg.Service
	if identifier == "" {
		identifier = cfg.Database
	}

	dsn := fmt.Sprintf("oracle://%s:%s@%s:%d/%s",
		url.PathEscape(cfg.User), url.PathEscape(cfg.Password),
		cfg.Host, cfg.Port, identifier)

	params := url.Values{}

	// Add privilege parameter.
	switch cfg.Privilege {
	case "sysdba":
		params.Set("sysdba", "1")
	case "sysoper":
		params.Set("sysoper", "1")
	}

	// Add driver-specific options.
	for k, v := range cfg.Options {
		params.Set(k, v)
	}

	if len(params) > 0 {
		dsn += "?" + params.Encode()
	}
	return dsn
}

// Query executes a SELECT statement and returns the results.
func (d *OracleDriver) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()

	rows, err := d.conn.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("oracle query failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows, start)
}

// Exec executes a DML/DDL statement and returns the result.
func (d *OracleDriver) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()

	result, err := d.conn.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("oracle exec failed: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{
		RowsAffected: rowsAffected,
		Duration:     time.Since(start),
	}, nil
}

// BeginTx starts a new transaction with the given options.
func (d *OracleDriver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	sqlOpts := &sql.TxOptions{}
	if opts != nil {
		sqlOpts.ReadOnly = opts.ReadOnly
	}

	tx, err := d.conn.BeginTx(ctx, sqlOpts)
	if err != nil {
		return nil, fmt.Errorf("oracle begin tx failed: %w", err)
	}

	return &oracleTx{tx: tx}, nil
}

// Ping tests the database connection.
func (d *OracleDriver) Ping(ctx context.Context) error {
	return d.conn.PingContext(ctx)
}

// ServerInfo returns the cached server information.
func (d *OracleDriver) ServerInfo() db.ServerInfo {
	return d.serverInfo
}

// Close closes the underlying database connection.
func (d *OracleDriver) Close() error {
	return d.conn.Close()
}

// oracleTx wraps sql.Tx to implement db.Tx.
type oracleTx struct {
	tx *sql.Tx
}

func (t *oracleTx) Query(ctx context.Context, sqlStr string, args ...any) (*db.QueryResult, error) {
	start := time.Now()

	rows, err := t.tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("oracle tx query failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows, start)
}

func (t *oracleTx) Exec(ctx context.Context, sqlStr string, args ...any) (*db.ExecResult, error) {
	start := time.Now()

	result, err := t.tx.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("oracle tx exec failed: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return &db.ExecResult{
		RowsAffected: rowsAffected,
		Duration:     time.Since(start),
	}, nil
}

func (t *oracleTx) Commit() error {
	return t.tx.Commit()
}

func (t *oracleTx) Rollback() error {
	return t.tx.Rollback()
}

// scanRows reads all rows from a sql.Rows into a QueryResult.
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
			converted[i] = convertOracleValue(v)
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

// fetchServerInfo fetches Oracle instance metadata at connect time.
func fetchServerInfo(ctx context.Context, conn *sql.DB) (db.ServerInfo, error) {
	var info db.ServerInfo
	err := conn.QueryRowContext(ctx, queryServerInfo).Scan(
		&info.Version,
		&info.InstanceName,
		&info.Hostname,
		&info.Platform,
		&info.StartupTime,
	)
	if err != nil {
		return db.ServerInfo{}, fmt.Errorf("failed to scan server info: %w", err)
	}

	// Fetch hardware profile (best-effort, don't fail if unavailable).
	info.CPUCores, info.MemoryGB = fetchHardwareInfo(ctx, conn)

	return info, nil
}

// fetchHardwareInfo queries CPU cores and physical memory from Oracle.
// Returns (0, 0) if the info is not accessible (e.g., insufficient privileges).
func fetchHardwareInfo(ctx context.Context, conn *sql.DB) (cpuCores int, memoryGB float64) {
	// Try V$OSSTAT first (requires SELECT on V$OSSTAT).
	rows, err := conn.QueryContext(ctx,
		`SELECT STAT_NAME, VALUE FROM V$OSSTAT WHERE STAT_NAME IN ('NUM_CPU_CORES', 'PHYSICAL_MEMORY_BYTES')`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var value float64
			if err := rows.Scan(&name, &value); err != nil {
				continue
			}
			switch name {
			case "NUM_CPU_CORES":
				cpuCores = int(value)
			case "PHYSICAL_MEMORY_BYTES":
				memoryGB = value / (1024 * 1024 * 1024)
			}
		}
	}

	// Fallback: V$PARAMETER cpu_count if V$OSSTAT didn't return CPU cores.
	if cpuCores == 0 {
		var val string
		if err := conn.QueryRowContext(ctx,
			`SELECT VALUE FROM V$PARAMETER WHERE NAME = 'cpu_count'`).Scan(&val); err == nil {
			if n, err := fmt.Sscanf(val, "%d", &cpuCores); n == 0 || err != nil {
				cpuCores = 0
			}
		}
	}

	return cpuCores, memoryGB
}
