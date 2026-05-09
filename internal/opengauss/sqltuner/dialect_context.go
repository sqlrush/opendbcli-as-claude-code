/*-------------------------------------------------------------------------
 *
 * dialect_context.go
 *	  DialectCollector queries OG version, extensions, parameters,
 *	  replication state. Result feeds into system prompt §2 (M7
 *	  灵魂能力 — 让 LLM 知道 OG 能力边界).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/dialect_context.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
)

// DialectCollector queries OG version, extensions, parameters, replication state.
// Result feeds into system prompt §2 (M7 灵魂能力 — 让 LLM 知道 OG 能力边界).
type DialectCollector struct {
	driver db.Driver
}

func NewDialectCollector(d db.Driver) *DialectCollector { return &DialectCollector{driver: d} }

// 关键 GUC 参数列表 — LLM 用来判断 hash join 大表是否 spill / 是否走并行 / sort 是否溢出
var keyParams = []string{
	"work_mem", "maintenance_work_mem", "shared_buffers",
	"effective_cache_size", "random_page_cost", "seq_page_cost",
	"max_parallel_workers_per_gather", "from_collapse_limit",
	"join_collapse_limit", "geqo_threshold",
	"default_statistics_target",
}

// OG 5.0 已知能力边界（硬编码 — 不变化的特性约束）
var og50Unsupported = []string{
	"GIN 索引（OG 5.0 不支持）",
	"INCLUDE 列覆盖索引（PG 11+ 才有，OG 5.0 没有）",
	"BRIN 索引（OG 5.0 不支持）",
	"声明式分区的 RANGE+LIST 嵌套",
	"pg_hint_plan 扩展（用 OG 原生 /*+ */ 替代）",
}

var og50Supported = []string{
	"列存表（CREATE TABLE ... WITH (orientation=column)）",
	"INSERT/UPDATE/DELETE RETURNING",
	"OG 原生 /*+ */ HINT 语法（leading/scan/join/set 全集）",
	"声明式 RANGE/LIST 分区",
	"扩展统计 CREATE STATISTICS（dependencies / mcv / ndistinct）",
	"pg_stat_statements 扩展（如已装）",
}

func (d *DialectCollector) Snapshot(ctx context.Context) (*DialectInfo, error) {
	info := &DialectInfo{
		Parameters:       make(map[string]string),
		UnsupportedFeats: og50Unsupported,
		SupportedFeats:   og50Supported,
	}

	// version
	if res, err := d.driver.Query(ctx, "SELECT version()"); err == nil && len(res.Rows) > 0 {
		info.Version = asString(res.Rows[0][0])
	}

	// extensions
	if res, err := d.driver.Query(ctx, "SELECT extname FROM pg_extension ORDER BY extname"); err == nil {
		for _, row := range res.Rows {
			if len(row) > 0 {
				info.Extensions = append(info.Extensions, asString(row[0]))
			}
		}
	}

	// key parameters (one query, IN list)
	paramQuery := "SELECT name, setting, unit FROM pg_settings WHERE name IN (" + sqlInList(keyParams) + ")"
	if res, err := d.driver.Query(ctx, paramQuery); err == nil {
		for _, row := range res.Rows {
			if len(row) < 3 {
				continue
			}
			name := asString(row[0])
			val := asString(row[1])
			unit := asString(row[2])
			if unit != "" {
				val = val + unit
			}
			info.Parameters[name] = val
		}
	}

	// replication state
	if res, err := d.driver.Query(ctx, "SELECT count(*) FROM pg_stat_replication"); err == nil && len(res.Rows) > 0 {
		info.HighAvailability = asInt64(res.Rows[0][0]) > 0
	}

	// partitioned tables present
	if res, err := d.driver.Query(ctx, "SELECT count(*) FROM pg_class WHERE relkind = 'p'"); err == nil && len(res.Rows) > 0 {
		info.HasPartitionedTab = asInt64(res.Rows[0][0]) > 0
	}

	return info, nil
}

// PromptSection2 returns the M7 dialect block for system prompt injection.
// Injected verbatim into Section 2 of the 9-section prompt.
func (d *DialectInfo) PromptSection2() string {
	var b strings.Builder
	b.WriteString("当前数据库环境：\n")
	if d.Version != "" {
		b.WriteString("- 产品: " + truncOneLine(d.Version, 100) + "\n")
	}
	if len(d.Extensions) > 0 {
		b.WriteString("- 已装扩展: " + strings.Join(d.Extensions, ", ") + "\n")
	}
	if len(d.Parameters) > 0 {
		b.WriteString("- 关键参数:\n")
		for _, k := range keyParams { // stable order
			if v, ok := d.Parameters[k]; ok {
				b.WriteString(fmt.Sprintf("    %s = %s\n", k, v))
			}
		}
	}
	if d.HighAvailability {
		b.WriteString("- 高可用: 主备同步模式（schema 变更同步备库）\n")
	} else {
		b.WriteString("- 高可用: 单机模式\n")
	}
	if d.HasPartitionedTab {
		b.WriteString("- 已存在分区表: 是\n")
	}
	if len(d.UnsupportedFeats) > 0 {
		b.WriteString("- 不支持: " + strings.Join(d.UnsupportedFeats, "; ") + "\n")
	}
	if len(d.SupportedFeats) > 0 {
		b.WriteString("- 支持: " + strings.Join(d.SupportedFeats, "; ") + "\n")
	}
	b.WriteString("\n请根据以上能力边界给方案，给出的特性必须确认 OG 5.0 支持。\n")
	return b.String()
}

func truncOneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
