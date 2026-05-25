/*-------------------------------------------------------------------------
 *
 * cluster.go
 *	  cluster — ClusterSkill plus helpers (NewClusterSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/cluster.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// DM 集群有两种形态：
// 1. DMDSC (共享存储集群) — V$DSC_EP_INFO / V$DSC_NODES
// 2. DMMPP (大规模并行) — V$MPP_INSTANCES
// 3. DW (主备数据守护) — V$DMWATCHER_INFO / V$ARCH_DEST_STATUS
//
// 单机部署 (默认) 这些视图大多无数据。本 skill 探测可用视图并展示其内容。

const dscNodesSQL = `SELECT EP_NAME, EP_SEQNO, EP_STATUS, INST_OK_FLAG
FROM V$DSC_EP_INFO`

const mppNodesSQL = `SELECT EP_NAME, EP_STATUS, EP_SEQNO
FROM V$MPP_INSTANCES`

const dwInfoSQL = `SELECT MID, MAL_ID, OGUID, MNAME, MSTATUS
FROM V$DMWATCHER_INFO`

const archDestSQL = `SELECT ARCH_TYPE, ARCH_DEST, ARCH_STATUS
FROM V$ARCH_STATUS`

type ClusterSkill struct{ driver db.Driver }

func NewClusterSkill(driver db.Driver) *ClusterSkill { return &ClusterSkill{driver: driver} }

func (s *ClusterSkill) Name() string                       { return "cluster" }
func (s *ClusterSkill) Description() string                { return "DM 集群状态 (DMDSC / DMMPP / DW 数据守护)" }
func (s *ClusterSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ClusterSkill) Validate(_ skill.Params) error      { return nil }

func (s *ClusterSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "cluster", Description: "Show DM cluster topology (DSC/MPP/DW)"}
}
func (s *ClusterSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "cluster", Usage: "/cluster"}
}

func (s *ClusterSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	dsc, dscErr := s.driver.Query(ctx, dscNodesSQL)
	mpp, mppErr := s.driver.Query(ctx, mppNodesSQL)
	dw, dwErr := s.driver.Query(ctx, dwInfoSQL)
	arch, archErr := s.driver.Query(ctx, archDestSQL)

	var b strings.Builder
	entries := []dmutil.SummaryEntry{}
	clusterType := "single (no cluster)"

	if dscErr == nil && dsc != nil && len(dsc.Rows) > 0 {
		b.WriteString("=== DMDSC 共享存储集群 ===\n")
		b.WriteString(format.FormatTable(dsc))
		entries = append(entries, dmutil.SummaryEntry{Key: "dsc_node_count", Val: len(dsc.Rows)})
		clusterType = "DMDSC"
	}
	if mppErr == nil && mpp != nil && len(mpp.Rows) > 0 {
		b.WriteString("\n=== DMMPP 大规模并行 ===\n")
		b.WriteString(format.FormatTable(mpp))
		entries = append(entries, dmutil.SummaryEntry{Key: "mpp_node_count", Val: len(mpp.Rows)})
		if clusterType == "single (no cluster)" {
			clusterType = "DMMPP"
		}
	}
	if dwErr == nil && dw != nil && len(dw.Rows) > 0 {
		b.WriteString("\n=== 数据守护 (DW) ===\n")
		b.WriteString(format.FormatTable(dw))
		entries = append(entries, dmutil.SummaryEntry{Key: "dw_member_count", Val: len(dw.Rows)})
		if clusterType == "single (no cluster)" {
			clusterType = "DataGuard"
		}
	}
	if archErr == nil && arch != nil && len(arch.Rows) > 0 {
		b.WriteString("\n=== 归档目的地状态 ===\n")
		b.WriteString(format.FormatTable(arch))
	}

	if b.Len() == 0 {
		b.WriteString("(单机部署 — 未检测到 DSC/MPP/DW 集群视图数据)\n")
	}

	entries = append([]dmutil.SummaryEntry{
		{Key: "cluster_type", Val: clusterType},
	}, entries...)

	// 选第一个有数据的 QR 当作 Data
	var data *db.QueryResult
	if dsc != nil && len(dsc.Rows) > 0 {
		data = dsc
	} else if mpp != nil && len(mpp.Rows) > 0 {
		data = mpp
	} else if dw != nil && len(dw.Rows) > 0 {
		data = dw
	} else {
		// 用一个空 QR
		data = &db.QueryResult{Columns: []string{"info"}, Rows: [][]any{}}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     data,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("集群形态: %s", clusterType),
	}, nil
}
