/*-------------------------------------------------------------------------
 *
 * remediation_test.go
 *	  Test cases for remediation.go (sentinel package):
 *	  TestGetRemediation_AllCauses, TestGetRemediation_Unknown,
 *	  TestGetRemediation_InvalidCause.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/remediation_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"testing"
)

func TestGetRemediation_AllCauses(t *testing.T) {
	causes := []RootCauseType{
		CauseBadSQL,
		CauseIOSubsystem,
		CauseLatchStorm,
		CauseRedoBottleneck,
		CauseLockContention,
		CauseTrafficStorm,
	}

	for _, cause := range causes {
		t.Run(string(cause), func(t *testing.T) {
			r := GetRemediation(cause)
			if len(r.InvestigateSQL) == 0 {
				t.Errorf("%s: expected InvestigateSQL, got none", cause)
			}
			if len(r.Suggestions) == 0 {
				t.Errorf("%s: expected Suggestions, got none", cause)
			}
			if len(r.Parameters) == 0 {
				t.Errorf("%s: expected Parameters, got none", cause)
			}
		})
	}
}

func TestGetRemediation_Unknown(t *testing.T) {
	r := GetRemediation(CauseUnknown)
	if len(r.Suggestions) == 0 {
		t.Error("unknown cause should still have suggestions")
	}
	if len(r.InvestigateSQL) != 0 {
		t.Error("unknown cause should have no InvestigateSQL")
	}
}

func TestGetRemediation_InvalidCause(t *testing.T) {
	r := GetRemediation(RootCauseType("nonexistent"))
	if len(r.Suggestions) == 0 {
		t.Error("invalid cause should fall through to default with suggestions")
	}
}

func TestGetRemediation_BadSQL_Content(t *testing.T) {
	r := GetRemediation(CauseBadSQL)

	// Should mention execution plan
	found := false
	for _, sql := range r.InvestigateSQL {
		if len(sql) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("bad_sql remediation should have non-empty SQL")
	}

	// Should mention plan baseline in suggestions
	foundPlan := false
	for _, s := range r.Suggestions {
		if contains(s, "Plan Baseline") || contains(s, "执行计划") {
			foundPlan = true
			break
		}
	}
	if !foundPlan {
		t.Error("bad_sql suggestions should mention plan baseline or execution plan")
	}
}

func TestGetRemediation_LockContention_Content(t *testing.T) {
	r := GetRemediation(CauseLockContention)

	foundKill := false
	for _, s := range r.Suggestions {
		if contains(s, "kill") || contains(s, "KILL") {
			foundKill = true
			break
		}
	}
	if !foundKill {
		t.Error("lock_contention suggestions should mention kill session option")
	}
}

func TestGetRemediation_IOSubsystem_Content(t *testing.T) {
	r := GetRemediation(CauseIOSubsystem)

	foundCache := false
	for _, p := range r.Parameters {
		if p == "db_cache_size" {
			foundCache = true
			break
		}
	}
	if !foundCache {
		t.Error("io_subsystem parameters should include db_cache_size")
	}
}

func TestGetRemediation_Immutability(t *testing.T) {
	r1 := GetRemediation(CauseBadSQL)
	r2 := GetRemediation(CauseBadSQL)

	// Modifying r1 should not affect r2
	r1.Suggestions[0] = "MODIFIED"
	if r2.Suggestions[0] == "MODIFIED" {
		t.Error("remediation should return new copies, not shared references")
	}
}

// contains checks if s contains substr (simple helper for test readability).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
