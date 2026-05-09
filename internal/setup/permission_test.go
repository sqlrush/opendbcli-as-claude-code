/*-------------------------------------------------------------------------
 *
 * permission_test.go
 *	  Test cases for permission.go (setup package):
 *	  TestPermissionGuide_Oracle, TestPermissionGuide_MySQL,
 *	  TestPermissionGuide_Postgres.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/permission_test.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import "testing"

func TestPermissionGuide_Oracle(t *testing.T) {
	guide := PermissionGuideFor("oracle")
	if guide.DBType != "oracle" {
		t.Errorf("expected oracle, got %s", guide.DBType)
	}
	if len(guide.Required) == 0 {
		t.Error("expected non-empty required permissions for oracle")
	}
	if len(guide.NotRecommended) == 0 {
		t.Error("expected non-empty not-recommended permissions for oracle")
	}
	if guide.CreateSQL == "" {
		t.Error("expected non-empty create SQL for oracle")
	}
}

func TestPermissionGuide_MySQL(t *testing.T) {
	guide := PermissionGuideFor("mysql")
	if guide.DBType != "mysql" {
		t.Errorf("expected mysql, got %s", guide.DBType)
	}
	if len(guide.Required) == 0 {
		t.Error("expected non-empty required permissions for mysql")
	}
}

func TestPermissionGuide_Postgres(t *testing.T) {
	guide := PermissionGuideFor("postgres")
	if guide.DBType != "postgres" {
		t.Errorf("expected postgres, got %s", guide.DBType)
	}
}

func TestPermissionGuide_Unknown(t *testing.T) {
	guide := PermissionGuideFor("unknown")
	if guide.DBType != "unknown" {
		t.Errorf("expected unknown, got %s", guide.DBType)
	}
	if len(guide.Required) != 0 {
		t.Error("expected empty required for unknown DB type")
	}
}
