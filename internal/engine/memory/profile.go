/*-------------------------------------------------------------------------
 *
 * profile.go
 *	  ProfileTemplate returns a starter PROFILE.md template for a new
 *	  instance. product is one of "oracle" / "mysql" / "postgres" /
 *	  "opengauss" (empty = generic). Specific products get extra fields
 *	  (e.g. MOT / CM for OG) so the LLM knows which engine-specific
 *	  attributes to look up on first diagnosis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/profile.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WriteProfile creates or overwrites the PROFILE.md for the active instance.
// PROFILE.md is a special memory file: not indexed in MEMORY.md, loaded fully
// into context on every session start.
func (s *Store) WriteProfile(content string) error {
	if s.activeInstance == "" {
		return fmt.Errorf("no active instance")
	}

	dir := s.instanceDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	path := filepath.Join(dir, ProfileFile)
	return atomicWrite(path, []byte(content))
}

// ProfileExists returns true if PROFILE.md exists for the active instance.
func (s *Store) ProfileExists() bool {
	if s.activeInstance == "" {
		return false
	}
	path := filepath.Join(s.instanceDir(), ProfileFile)
	_, err := os.Stat(path)
	return err == nil
}

// ProfileTemplate returns a starter PROFILE.md template for a new instance.
// product is one of "oracle" / "mysql" / "postgres" / "opengauss" (empty =
// generic). Specific products get extra fields (e.g. MOT / CM for OG) so
// the LLM knows which engine-specific attributes to look up on first
// diagnosis.
func ProfileTemplate(instance, product string) string {
	switch product {
	case "opengauss":
		return openGaussProfileTemplate(instance)
	case "postgres":
		return postgresProfileTemplate(instance)
	default:
		return genericProfileTemplate(instance)
	}
}

func genericProfileTemplate(instance string) string {
	return fmt.Sprintf(`# 实例画像: %s

> 最后更新: %s 由 LLM 自动维护

## 负载特征

- **业务类型**: 待观察
- **高峰时段**: 待观察
- **低峰时段**: 待观察
- **业务周期**: 待观察
- **资源模式**: 待观察

## 问题特征

- **高频问题**: 暂无记录
- **偶发问题**: 暂无记录
- **已知薄弱环节**: 暂无记录

## 常见问题及解决方案

| 问题 | 频率 | 解决方案 | 备注 |
|------|------|---------|------|
| (暂无) | - | - | - |
`, instance, time.Now().Format("2006-01-02"))
}

// openGaussProfileTemplate includes OG-specific fields (MOT/CM/WDR) so the
// LLM has a checklist to fill on first diagnosis. Keep the "历史诊断"
// section capped at ~10 entries — older ones get evicted into individual
// memory/diag-XXX.md files by the memory system.
func openGaussProfileTemplate(instance string) string {
	return fmt.Sprintf(`# 实例画像: %s (openGauss)

> 最后更新: %s 由 LLM 自动维护
> 保留最近 10 条历史诊断；溢出条目移入 memory/diag-*.md

## 基本信息
- **openGauss 版本**: 待探测（SELECT version()）
- **部署形态**: 单机 / 主备 / 分布式 — 待探测（SELECT local_role FROM pg_stat_get_stream_replications()）
- **CM 集群管理**: 有 / 无 — 待探测
- **WDR 快照**: on / off — 待探测（SHOW enable_wdr_snapshot）
- **pg_stat_statements 扩展**: 加载 / 未加载 — 待探测
- **MOT 内存引擎**: 启用 / 未启用 — 待探测（SELECT * FROM mot_mem_cfg LIMIT 1）
- **归档模式**: on / off — 待探测（SHOW archive_mode）

## 业务特征（LLM 逐步填充）
- **主要 schema**: 待观察
- **读写比**: 待观察
- **高峰时段**: 待观察
- **业务周期**: 待观察
- **连接模式**: 短连接 / 长连接 / 混合 — 待观察

## 已知配置异常
- (LLM 发现 max_process_memory / work_mem / shared_buffers 明显偏离推荐时记录)

## 历史诊断（append-only，最近 10 条）
| 日期 | 现象 | 根因 | 处理 |
|------|------|------|------|
| (暂无) | - | - | - |

## 踩过的坑
- (LLM 诊断出过的 XID / bloat / VACUUM / WLM 相关问题归档于此)
`, instance, time.Now().Format("2006-01-02"))
}

func postgresProfileTemplate(instance string) string {
	return fmt.Sprintf(`# 实例画像: %s (PostgreSQL)

> 最后更新: %s 由 LLM 自动维护
> 保留最近 10 条历史诊断；溢出条目移入 memory/diag-*.md

## 基本信息
- **PG 版本**: 待探测
- **部署形态**: 单机 / 主备 / 逻辑复制
- **pg_stat_statements**: 加载 / 未加载
- **归档模式**: on / off

## 业务特征（LLM 逐步填充）
- **主要 schema**: 待观察
- **读写比**: 待观察
- **高峰时段**: 待观察

## 历史诊断（append-only，最近 10 条）
| 日期 | 现象 | 根因 | 处理 |
|------|------|------|------|
| (暂无) | - | - | - |
`, instance, time.Now().Format("2006-01-02"))
}
