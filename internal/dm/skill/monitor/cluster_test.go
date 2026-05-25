/*-------------------------------------------------------------------------
 *
 * cluster_test.go
 *	  Test cases for cluster.go (monitor package):
 *	  TestClusterSkill_Metadata, TestClusterSkill_SingleMode,
 *	  TestClusterSkill_DSCMode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/cluster_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestClusterSkill_Metadata(t *testing.T) {
	s := NewClusterSkill(makeRoutedDriver())
	if s.Name() != "cluster" {
		t.Errorf("Name() = %q", s.Name())
	}
}

// 单机部署: 4 个集群视图都返回空 → cluster_type: single
func TestClusterSkill_SingleMode(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{contains: "V$DSC_EP_INFO", result: &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}},
		sqlMatcher{contains: "V$MPP_INSTANCES", result: &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}},
		sqlMatcher{contains: "V$DMWATCHER_INFO", result: &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}},
		sqlMatcher{contains: "V$ARCH_STATUS", result: &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}},
	)
	r, _ := NewClusterSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "cluster_type: single (no cluster)")
}

func TestClusterSkill_DSCMode(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$DSC_EP_INFO",
			result: &db.QueryResult{
				Columns: []string{"EP_NAME", "EP_SEQNO", "EP_STATUS", "INST_OK_FLAG"},
				Rows: [][]any{
					{"EP1", int64(0), "OK", "Y"},
					{"EP2", int64(1), "OK", "Y"},
				},
			},
		},
	)
	r, _ := NewClusterSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "cluster_type: DMDSC")
	assertSummaryContains(t, r.Rendered, "dsc_node_count: 2")
}

func TestClusterSkill_MPPMode(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$MPP_INSTANCES",
			result: &db.QueryResult{
				Columns: []string{"EP_NAME", "EP_STATUS", "EP_SEQNO"},
				Rows: [][]any{
					{"MPP_NODE1", "OK", int64(0)},
					{"MPP_NODE2", "OK", int64(1)},
					{"MPP_NODE3", "OK", int64(2)},
				},
			},
		},
	)
	r, _ := NewClusterSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "cluster_type: DMMPP")
	assertSummaryContains(t, r.Rendered, "mpp_node_count: 3")
}

// 当多种集群视图同时返回数据时, DSC 优先级最高 (覆盖 MPP/DW)
func TestClusterSkill_PriorityDSC(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$DSC_EP_INFO",
			result: &db.QueryResult{
				Columns: []string{"EP_NAME", "EP_SEQNO", "EP_STATUS", "INST_OK_FLAG"},
				Rows:    [][]any{{"EP1", int64(0), "OK", "Y"}},
			},
		},
		sqlMatcher{
			contains: "V$MPP_INSTANCES",
			result: &db.QueryResult{
				Columns: []string{"EP_NAME", "EP_STATUS", "EP_SEQNO"},
				Rows:    [][]any{{"MPP_NODE", "OK", int64(0)}},
			},
		},
	)
	r, _ := NewClusterSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	// DSC 优先, cluster_type 应该是 DMDSC (代码逻辑 if clusterType == single 才升级)
	assertSummaryContains(t, r.Rendered, "cluster_type: DMDSC")
	// 但 mpp_node_count 也应该出现 (二个块都渲染)
	assertSummaryContains(t, r.Rendered, "mpp_node_count: 1")
}

// 归档目的地状态正常显示
func TestClusterSkill_ArchDestStatus(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$ARCH_STATUS",
			result: &db.QueryResult{
				Columns: []string{"ARCH_TYPE", "ARCH_DEST", "ARCH_STATUS"},
				Rows: [][]any{
					{"REALTIME", "GRP1_RT_01", "VALID"},
				},
			},
		},
	)
	r, _ := NewClusterSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !contains(r.Rendered, "GRP1_RT_01") {
		t.Errorf("ArchDestStatus block missing. Got:\n%s", r.Rendered)
	}
}

func TestClusterSkill_DataGuardMode(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$DMWATCHER_INFO",
			result: &db.QueryResult{
				Columns: []string{"MID", "MAL_ID", "OGUID", "MNAME", "MSTATUS"},
				Rows:    [][]any{{int64(1), int64(1), int64(45331), "GRP1_PRIMARY", "OPEN"}},
			},
		},
	)
	r, _ := NewClusterSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "cluster_type: DataGuard")
	assertSummaryContains(t, r.Rendered, "dw_member_count: 1")
}
