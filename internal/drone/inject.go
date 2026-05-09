/*-------------------------------------------------------------------------
 *
 * inject.go
 *	  inject.go implements fault injection for testing Worker's
 *	  self-healing capabilities. /inject command creates real database
 *	  anomalies in test environments. MUST be hidden in production
 *	  (security level: LevelDangerous).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/inject.go
 *
 *-------------------------------------------------------------------------
 */
// inject.go implements fault injection for testing Worker's self-healing capabilities.
// /inject command creates real database anomalies in test environments.
// MUST be hidden in production (security level: LevelDangerous).
package drone

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/skill"
)

// InjectSkill injects database faults for testing autonomous healing.
type InjectSkill struct {
	driver db.Driver
}

// NewInjectSkill creates a new fault injection skill.
func NewInjectSkill(driver db.Driver) *InjectSkill {
	return &InjectSkill{driver: driver}
}

func (s *InjectSkill) Name() string        { return "inject" }
func (s *InjectSkill) Description() string { return "Inject database faults for testing (test env only)" }
func (s *InjectSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelDangerous }

func (s *InjectSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "inject_fault",
		Description: "Inject a database fault for testing Worker self-healing (test environment only)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"fault": map[string]any{
					"type":        "string",
					"description": "Fault type to inject",
					"enum":        []string{"temp_full", "lock_contention", "slow_sql", "connection_exhaust", "io_latency", "replication_lag"},
				},
			},
			"required": []string{"fault"},
		},
	}
}

func (s *InjectSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "inject",
		Usage:          "/inject --fault <type>",
		ArgCompletions: []string{"temp_full", "lock_contention", "slow_sql", "connection_exhaust", "io_latency", "replication_lag"},
		Examples: []string{
			"/inject --fault temp_full",
			"/inject --fault lock_contention",
			"/inject --fault slow_sql",
		},
	}
}

func (s *InjectSkill) Validate(params skill.Params) error {
	fault := params.StringOr("fault", params.StringOr("args", ""))
	if fault == "" {
		return fmt.Errorf("--fault is required (temp_full, lock_contention, slow_sql, connection_exhaust, io_latency, replication_lag)")
	}
	return nil
}

func (s *InjectSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	fault := params.StringOr("fault", params.StringOr("args", ""))

	var injector faultInjector
	switch fault {
	case "temp_full":
		injector = &tempFullInjector{}
	case "lock_contention":
		injector = &lockContentionInjector{}
	case "slow_sql":
		injector = &slowSQLInjector{}
	case "connection_exhaust":
		injector = &connectionExhaustInjector{}
	default:
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("Fault type '%s' not yet implemented. Available: temp_full, lock_contention, slow_sql", fault),
			Summary:  "not implemented",
		}, nil
	}

	result, err := injector.Inject(ctx, s.driver)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: fmt.Sprintf("Fault injection failed: %v", err),
			Summary:  "injection failed",
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: result,
		Summary:  fmt.Sprintf("injected: %s", fault),
	}, nil
}

// faultInjector is the interface for all fault types.
type faultInjector interface {
	Inject(ctx context.Context, driver db.Driver) (string, error)
}

// ---- Fault Implementations ----

type tempFullInjector struct{}

func (f *tempFullInjector) Inject(ctx context.Context, driver db.Driver) (string, error) {
	// Create a session that consumes TEMP tablespace via large HASH JOIN.
	sql := `SELECT /*+ USE_HASH(a b) */ COUNT(*)
FROM (SELECT LEVEL n FROM DUAL CONNECT BY LEVEL <= 100000) a,
     (SELECT LEVEL n FROM DUAL CONNECT BY LEVEL <= 1000) b
WHERE a.n = b.n`

	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		driver.Query(ctx, sql)
	})

	return "Injected: TEMP tablespace pressure via large HASH JOIN.\n" +
		"Sentinel should detect TEMP usage surge within 10-30 seconds.\n" +
		"Worker LLM should diagnose and recommend kill session.", nil
}

type lockContentionInjector struct{}

func (f *lockContentionInjector) Inject(ctx context.Context, driver db.Driver) (string, error) {
	// Create a lock by updating a row without committing.
	// This creates lock contention when other sessions try to update the same row.
	sql := `BEGIN
  EXECUTE IMMEDIATE 'CREATE TABLE IF NOT EXISTS opendb_test_lock (id NUMBER, val VARCHAR2(100))';
  EXECUTE IMMEDIATE 'INSERT INTO opendb_test_lock VALUES (1, ''test'')';
  COMMIT;
  EXECUTE IMMEDIATE 'UPDATE opendb_test_lock SET val = ''locked'' WHERE id = 1';
  -- No COMMIT: holds the lock
  DBMS_LOCK.SLEEP(120);
  COMMIT;
END;`

	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		driver.Query(ctx, sql)
	})

	return "Injected: Lock contention via uncommitted UPDATE (2 min hold).\n" +
		"Sentinel should detect lock wait event surge.\n" +
		"Worker LLM should identify the blocking session.", nil
}

type slowSQLInjector struct{}

func (f *slowSQLInjector) Inject(ctx context.Context, driver db.Driver) (string, error) {
	// Execute a deliberately slow query.
	sql := `SELECT /*+ FULL(t1) FULL(t2) NO_INDEX(t1) NO_INDEX(t2) */
COUNT(*) FROM all_objects t1, all_objects t2
WHERE t1.object_name = t2.object_name`

	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		driver.Query(ctx, sql)
	})

	return "Injected: Slow SQL via full cartesian join on all_objects.\n" +
		"Sentinel should detect active session and CPU surge.\n" +
		"Worker LLM should identify the SQL and recommend optimization.", nil
}

type connectionExhaustInjector struct{}

func (f *connectionExhaustInjector) Inject(_ context.Context, _ db.Driver) (string, error) {
	// Connection exhaustion would require opening many parallel connections.
	// For safety, this is a placeholder that just reports the concept.
	return "Connection exhaustion injection requires spawning many parallel connections.\n" +
		"Not implemented for safety reasons — use database-level tools to simulate.", nil
}
