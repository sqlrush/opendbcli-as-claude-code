/*-------------------------------------------------------------------------
 *
 * health.go
 *	  HealthSkill: dashboard 总览 — 多视图并行 + summary banner
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/health.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// HealthSkill: dashboard 总览 — 多视图并行 + summary banner
type HealthSkill struct{ driver db.Driver }

func NewHealthSkill(driver db.Driver) *HealthSkill { return &HealthSkill{driver: driver} }

func (s *HealthSkill) Name() string                       { return "health" }
func (s *HealthSkill) Description() string                { return "数据库健康总览 (多视图聚合 dashboard)" }
func (s *HealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *HealthSkill) Validate(_ skill.Params) error      { return nil }
func (s *HealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "health", Description: "DM health dashboard (instance/sessions/locks/deadlocks/buffer)"}
}
func (s *HealthSkill) CLIDef() skill.CLIDef { return skill.CLIDef{Command: "health", Usage: "/health"} }

func (s *HealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	type metric struct {
		name string
		val  string
	}
	results := make([]metric, 0, 12)
	var mu sync.Mutex
	var wg sync.WaitGroup

	q := func(name, sqlStr string) {
		defer wg.Done()
		r, err := s.driver.Query(ctx, sqlStr)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			// 视图不存在 (DM-2106) 不是错误, 是单机部署的正常状态.
			// 例如 V$DMWATCHER_INFO 只在数据守护集群有, 单机显示 "—" 即可.
			msg := err.Error()
			if strings.Contains(msg, "Error -2106") || strings.Contains(msg, "无效的表或视图名") {
				results = append(results, metric{name, "—"})
				return
			}
			results = append(results, metric{name, fmt.Sprintf("ERR: %v", err)})
			return
		}
		if len(r.Rows) == 0 || len(r.Rows[0]) == 0 {
			results = append(results, metric{name, "—"})
			return
		}
		results = append(results, metric{name, fmt.Sprintf("%v", r.Rows[0][0])})
	}

	queries := []struct{ name, sql string }{
		// 实例与角色
		{"实例状态", "SELECT INSTANCE_NAME || ' (build ' || BUILD_VERSION || ')' FROM V$INSTANCE"},
		{"启动时间", "SELECT START_TIME FROM V$INSTANCE"},
		{"主备角色", "SELECT CASE ROLE$ WHEN 0 THEN 'PRIMARY' WHEN 1 THEN 'STANDBY' WHEN 2 THEN 'DBSTANDBY' ELSE 'OTHER' END FROM V$DATABASE"},
		{"归档模式", "SELECT ARCH_MODE FROM V$DATABASE"},
		// 会话与锁
		{"会话总数", "SELECT COUNT(*) FROM V$SESSIONS"},
		{"活跃会话", "SELECT COUNT(*) FROM V$SESSIONS WHERE STATE = 'ACTIVE'"},
		{"锁等待会话", "SELECT COUNT(DISTINCT TRX_ID) FROM V$LOCK WHERE BLOCKED = 1"},
		// 错误与异常
		{"累计死锁", "SELECT COUNT(*) FROM V$DEADLOCK_HISTORY"},
		{"累计危险事件", "SELECT COUNT(*) FROM V$DANGER_EVENT"},
		{"累计错误", "SELECT COUNT(*) FROM V$RUNTIME_ERR_HISTORY"},
		{"长 SQL 数", "SELECT COUNT(*) FROM V$LONG_EXEC_SQLS"},
		// 存储与日志
		{"上次 checkpoint", "SELECT MAX(START_TIME) FROM V$CKPT_HISTORY"},
		{"当前 LSN", "SELECT FILE_LSN FROM V$RLOG"},
		{"checkpoint LSN", "SELECT CKPT_LSN FROM V$RLOG"},
		// 集群（单机时返回 0/null）
		{"DSC 节点数", "SELECT COUNT(*) FROM V$DSC_EP_INFO"},
		{"DataGuard 成员数", "SELECT COUNT(*) FROM V$DMWATCHER_INFO"},
	}
	wg.Add(len(queries))
	for _, q1 := range queries {
		go q(q1.name, q1.sql)
	}
	wg.Wait()

	var b strings.Builder
	b.WriteString("=== DM 健康总览 ===\n")
	for _, m := range results {
		b.WriteString(fmt.Sprintf("  %-16s : %s\n", m.name, m.val))
	}

	// summary banner for LLM friendly output
	b.WriteString("\n[summary]\n")
	for _, m := range results {
		key := strings.NewReplacer("(", "", ")", "", " ", "_", "/", "_").Replace(m.name)
		b.WriteString(fmt.Sprintf("%s: %s\n", strings.ToLower(key), m.val))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("DM health — %d metrics", len(results)),
	}, nil
}
