/*-------------------------------------------------------------------------
 *
 * perfsnap_test.go
 *	  Test cases for perfsnap.go (monitor package):
 *	  TestPerfSnapSkill_Metadata, TestPerfSnapSkill_AWR_Active,
 *	  TestPerfSnapSkill_AWR_NotConfigured.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/perfsnap_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestPerfSnapSkill_Metadata(t *testing.T) {
	s := NewPerfSnapSkill(makeRoutedDriver())
	if s.Name() != "perfsnap" {
		t.Errorf("Name() = %q", s.Name())
	}
	// alias awr
	hasAWR := false
	for _, a := range s.CLIDef().Aliases {
		if a == "awr" {
			hasAWR = true
		}
	}
	if !hasAWR {
		t.Errorf("CLIDef().Aliases missing 'awr'")
	}
}

func TestPerfSnapSkill_AWR_Active(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$PARAMETER",
			result: &db.QueryResult{
				Columns: []string{"NAME", "VALUE"},
				Rows: [][]any{
					{"AWR_RPT_HOME", "/var/dm/awr_reports"},
					{"AWR_AUTO_FLUSH_FREQ", "60"},
				},
			},
		},
		sqlMatcher{
			contains: "WRM$_SNAPSHOT",
			result: &db.QueryResult{
				Columns: []string{"SNAP_ID", "BEGIN_INTERVAL_TIME", "END_INTERVAL_TIME"},
				Rows: [][]any{
					{int64(1051), "2026-05-02 10:00:00", "2026-05-02 11:00:00"},
					{int64(1050), "2026-05-02 09:00:00", "2026-05-02 10:00:00"},
				},
			},
		},
	)
	r, _ := NewPerfSnapSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "awr_status: active")
	assertSummaryContains(t, r.Rendered, "awr_rpt_home: /var/dm/awr_reports")
	assertSummaryContains(t, r.Rendered, "snap_count_recent: 2")
	assertSummaryContains(t, r.Rendered, "latest_snap_id: 1051")
	// 必须给出操作命令提示
	assertSummaryContains(t, r.Rendered, "")
	for _, want := range []string{"SP_INIT_AWR_SYS", "SP_AWR_REPORT_LAST_DAY", "DBMS_WORKLOAD_REPOSITORY"} {
		if !strings.Contains(r.Rendered, want) {
			t.Errorf("Rendered missing operational hint %q", want)
		}
	}
}

func TestPerfSnapSkill_AWR_NotConfigured(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$PARAMETER",
			result: &db.QueryResult{
				Columns: []string{"NAME", "VALUE"},
				Rows: [][]any{
					{"AWR_RPT_HOME", ""},
				},
			},
		},
		sqlMatcher{
			contains: "WRM$_SNAPSHOT",
			result: &db.QueryResult{
				Columns: []string{"SNAP_ID", "BEGIN_INTERVAL_TIME", "END_INTERVAL_TIME"},
				Rows:    [][]any{},
			},
		},
	)
	r, _ := NewPerfSnapSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "awr_status: not configured")
}

func TestPerfSnapSkill_FallbackToAlternateSchema(t *testing.T) {
	// 第一次查 SYS.WRM$_SNAPSHOT 失败, 回退到 WRM$_SNAPSHOT
	calls := 0
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		calls++
		if strings.Contains(sql, "V$PARAMETER") {
			return &db.QueryResult{
				Columns: []string{"NAME", "VALUE"},
				Rows:    [][]any{{"AWR_RPT_HOME", "/path"}},
			}, nil
		}
		// 第一次 (SYS.WRM$_SNAPSHOT) 失败
		if strings.Contains(sql, "SYS.WRM$_SNAPSHOT") {
			return nil, errors.New("DM-2099: schema not found")
		}
		// 第二次 (无 SYS prefix) 成功
		return &db.QueryResult{
			Columns: []string{"SNAP_ID", "BEGIN_TIME", "END_TIME"},
			Rows:    [][]any{{int64(1), "10:00", "11:00"}},
		}, nil
	}
	r, _ := NewPerfSnapSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "snap_count_recent: 1")
}
