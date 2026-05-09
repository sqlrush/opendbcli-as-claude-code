/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Shared type definitions for the db package: ColumnInfo,
 *	  ConnectionConfig, ExecResult, QueryResult, ....
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/db/types.go
 *
 *-------------------------------------------------------------------------
 */
package db

import "time"

type ConnectionConfig struct {
	DBType    string
	Host      string
	Port      int
	Service   string
	User      string
	Password  string
	Database  string
	Privilege string            // sysdba|sysoper (empty = normal)
	Options   map[string]string // driver-specific options (wallet, OS auth, etc.)
}

type ServerInfo struct {
	Version      string
	InstanceName string
	Hostname     string
	Platform     string
	StartupTime  time.Time
	CPUCores     int     // from V$OSSTAT NUM_CPU_CORES or V$PARAMETER cpu_count
	MemoryGB     float64 // from V$OSSTAT PHYSICAL_MEMORY_BYTES
}

type QueryResult struct {
	Columns  []string
	Rows     [][]any
	Duration time.Duration
}

type ExecResult struct {
	RowsAffected int64
	Duration     time.Duration
}

type TxOptions struct {
	ReadOnly bool
}

type TableInfo struct {
	Schema    string
	Name      string
	RowCount  int64
	SizeBytes int64
}

type ColumnInfo struct {
	Name     string
	Type     string
	Nullable bool
	Default  string
}
