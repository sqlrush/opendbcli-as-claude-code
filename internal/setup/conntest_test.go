/*-------------------------------------------------------------------------
 *
 * conntest_test.go
 *	  Test cases for conntest.go (setup package):
 *	  TestPermissionCheckQueries_Oracle,
 *	  TestPermissionCheckQueries_MySQL,
 *	  TestPermissionCheckQueries_Postgres.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/conntest_test.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import "testing"

func TestPermissionCheckQueries_Oracle(t *testing.T) {
	queries := PermissionCheckQueries("oracle")
	if len(queries) == 0 {
		t.Error("expected non-empty permission check queries for oracle")
	}
	for _, q := range queries {
		if q.SQL == "" {
			t.Error("query SQL must not be empty")
		}
		if q.Name == "" {
			t.Error("query Name must not be empty")
		}
	}
}

func TestPermissionCheckQueries_MySQL(t *testing.T) {
	queries := PermissionCheckQueries("mysql")
	if len(queries) == 0 {
		t.Error("expected non-empty permission check queries for mysql")
	}
}

func TestPermissionCheckQueries_Postgres(t *testing.T) {
	queries := PermissionCheckQueries("postgres")
	if len(queries) == 0 {
		t.Error("expected non-empty permission check queries for postgres")
	}
}

func TestOverprivilegeCheckQueries_Oracle(t *testing.T) {
	queries := OverprivilegeCheckQueries("oracle")
	if len(queries) == 0 {
		t.Error("expected non-empty overprivilege check queries for oracle")
	}
}

func TestOverprivilegeCheckQueries_Unknown(t *testing.T) {
	queries := OverprivilegeCheckQueries("unknown")
	if queries != nil {
		t.Error("expected nil for unknown DB type")
	}
}
