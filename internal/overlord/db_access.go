/*-------------------------------------------------------------------------
 *
 * db_access.go
 *	  db_access.go implements Overlord's ability to directly connect to
 *	  Worker databases. This is the core of cross-node operations —
 *	  Overlord connects to primary and standby databases directly,
 *	  executing SQL in a single session for atomic coordination.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/db_access.go
 *
 *-------------------------------------------------------------------------
 */
// db_access.go implements Overlord's ability to directly connect to Worker databases.
// This is the core of cross-node operations — Overlord connects to primary and standby
// databases directly, executing SQL in a single session for atomic coordination.
package overlord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// DBAccessManager provides direct database access to any Worker's database.
// Overlord uses this to execute cross-node operations (failover, parameter sync, etc.)
// without going through the Worker's gRPC — it connects directly using stored connection strings.
type DBAccessManager struct {
	mu          sync.RWMutex
	connections map[string]*dbSession // workerID → active session
	connConfigs map[string]db.ConnectionConfig // workerID → connection config
	driverFactory connection.DriverFactory
	logger        *slog.Logger
}

// dbSession holds an active database connection for a Worker.
type dbSession struct {
	driver   db.Driver
	workerID string
}

// NewDBAccessManager creates a new database access manager.
func NewDBAccessManager() *DBAccessManager {
	return &DBAccessManager{
		connections: make(map[string]*dbSession),
		connConfigs: make(map[string]db.ConnectionConfig),
		logger:      cluster.DefaultLogger("db-access"),
	}
}

// SetDriverFactory sets the driver factory for creating database connections.
func (dam *DBAccessManager) SetDriverFactory(factory connection.DriverFactory) {
	dam.driverFactory = factory
}

// RegisterWorkerDB stores the connection config for a Worker's database.
// Called when a Worker registers or when Overlord receives connection info.
func (dam *DBAccessManager) RegisterWorkerDB(workerID string, cfg db.ConnectionConfig) {
	dam.mu.Lock()
	defer dam.mu.Unlock()
	dam.connConfigs[workerID] = cfg
	dam.logger.Info("Registered DB config", slog.String("worker_id", workerID), slog.String("db_type", cfg.DBType))
}

// Connect establishes a direct database connection to a Worker's database.
func (dam *DBAccessManager) Connect(ctx context.Context, workerID string) (db.Driver, error) {
	dam.mu.Lock()
	defer dam.mu.Unlock()

	// Return existing connection if available.
	if session, exists := dam.connections[workerID]; exists {
		return session.driver, nil
	}

	cfg, exists := dam.connConfigs[workerID]
	if !exists {
		return nil, fmt.Errorf("no DB config registered for worker %s", workerID)
	}

	if dam.driverFactory == nil {
		return nil, fmt.Errorf("no driver factory configured")
	}

	driver, err := dam.driverFactory(cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %s DB: %w", workerID, err)
	}

	dam.connections[workerID] = &dbSession{
		driver:   driver,
		workerID: workerID,
	}

	dam.logger.Info("Connected to worker database", slog.String("worker_id", workerID), slog.String("db_type", cfg.DBType))
	return driver, nil
}

// ExecuteSQL executes SQL directly on a Worker's database.
// This is the core operation for cross-node coordination.
func (dam *DBAccessManager) ExecuteSQL(ctx context.Context, workerID, sql string) (*db.QueryResult, error) {
	driver, err := dam.Connect(ctx, workerID)
	if err != nil {
		return nil, err
	}
	return driver.Query(ctx, sql)
}

// ExecuteOnMultiple executes the same SQL on multiple Worker databases in sequence.
// Returns results keyed by workerID.
func (dam *DBAccessManager) ExecuteOnMultiple(ctx context.Context, workerIDs []string, sql string) map[string]*SQLResult {
	results := make(map[string]*SQLResult)
	for _, wid := range workerIDs {
		qr, err := dam.ExecuteSQL(ctx, wid, sql)
		results[wid] = &SQLResult{
			WorkerID: wid,
			Result:   qr,
			Err:      err,
		}
	}
	return results
}

// SQLResult holds the result of a SQL execution on a Worker database.
type SQLResult struct {
	WorkerID string
	Result   *db.QueryResult
	Err      error
}

// Disconnect closes the database connection for a Worker.
func (dam *DBAccessManager) Disconnect(workerID string) {
	dam.mu.Lock()
	defer dam.mu.Unlock()

	if session, exists := dam.connections[workerID]; exists {
		if closer, ok := session.driver.(interface{ Close() error }); ok {
			closer.Close()
		}
		delete(dam.connections, workerID)
	}
}

// Close disconnects from all Worker databases.
func (dam *DBAccessManager) Close() {
	dam.mu.Lock()
	defer dam.mu.Unlock()

	for wid, session := range dam.connections {
		if closer, ok := session.driver.(interface{ Close() error }); ok {
			closer.Close()
		}
		delete(dam.connections, wid)
		_ = wid
	}
}

// WorkerDBSkill creates a skill.Executor-compatible wrapper that executes skills
// on a specific Worker's database. This allows Overlord's LLM to use the same
// skill set (sql, health, explain, etc.) on any Worker's database.
func (dam *DBAccessManager) WorkerDBSkill(workerID string) *WorkerDBProxy {
	return &WorkerDBProxy{
		workerID: workerID,
		dam:      dam,
	}
}

// WorkerDBProxy wraps a Worker's database connection as a skill-compatible interface.
type WorkerDBProxy struct {
	workerID string
	dam      *DBAccessManager
}

// Query executes a SQL query on the Worker's database.
func (p *WorkerDBProxy) Query(ctx context.Context, sql string) (*db.QueryResult, error) {
	return p.dam.ExecuteSQL(ctx, p.workerID, sql)
}

// RegisterDBSkill registers a /worker-sql skill for direct DB access.
func RegisterDBAccessSkill(registry *skill.Registry, dam *DBAccessManager) {
	registry.Register(&workerSQLSkill{dam: dam})
}

// workerSQLSkill executes SQL on a Worker's database directly from Overlord.
type workerSQLSkill struct {
	dam *DBAccessManager
}

func (s *workerSQLSkill) Name() string        { return "worker-sql" }
func (s *workerSQLSkill) Description() string { return "Execute SQL on a Worker's database directly" }
func (s *workerSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }

func (s *workerSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "query_worker_database",
		Description: "Execute SQL directly on any Worker's database (Overlord connects using stored connection string)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"worker_id": map[string]any{"type": "string", "description": "Target Worker ID"},
				"sql":       map[string]any{"type": "string", "description": "SQL to execute"},
			},
			"required": []string{"worker_id", "sql"},
		},
	}
}

func (s *workerSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "worker-sql",
		Usage:   "/worker-sql <worker_id> <sql>",
		Examples: []string{
			`/worker-sql Oracle-A-037 "SELECT * FROM v$session WHERE status='ACTIVE'"`,
		},
	}
}

func (s *workerSQLSkill) Validate(params skill.Params) error {
	if params.StringOr("worker_id", "") == "" {
		return fmt.Errorf("worker_id is required")
	}
	if params.StringOr("sql", "") == "" {
		return fmt.Errorf("sql is required")
	}
	return nil
}

func (s *workerSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	workerID := params.StringOr("worker_id", "")
	sql := params.StringOr("sql", "")

	qr, err := s.dam.ExecuteSQL(ctx, workerID, sql)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: fmt.Sprintf("Failed to execute on %s: %v", workerID, err),
			Summary:  "sql execution failed",
		}, nil
	}

	return &skill.Result{
		Type:    skill.ResultTable,
		Data:    qr,
		Summary: fmt.Sprintf("executed on %s: %d rows", workerID, len(qr.Rows)),
	}, nil
}
