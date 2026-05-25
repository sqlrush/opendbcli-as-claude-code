/*-------------------------------------------------------------------------
 *
 * views.go
 *	  Package monitor — DM views skill: 列出 DM 实例所有 V$
 *	  动态视图, 给 LLM 自动发现工具使用 (避免 Profile
 *	  里硬编码漏列).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/views.go
 *
 *-------------------------------------------------------------------------
 */
// Package monitor — DM views skill: 列出 DM 实例所有 V$ 动态视图,
// 给 LLM 自动发现工具使用 (避免 Profile 里硬编码漏列).
//
// 用法:
//   /views                — 列出全部 V$* 视图 + 按主题分类
//   /views session        — 名字含 SESSION 的视图
//   /views lock           — 名字含 LOCK 的视图
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

type ViewsSkill struct{ driver db.Driver }

func NewViewsSkill(driver db.Driver) *ViewsSkill { return &ViewsSkill{driver: driver} }

func (s *ViewsSkill) Name() string                       { return "views" }
func (s *ViewsSkill) Description() string                { return "列出 DM 全部 V$ 动态视图 (LLM 知识自动发现)" }
func (s *ViewsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ViewsSkill) Validate(_ skill.Params) error { return nil }

func (s *ViewsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "views",
		Description: "List DM V$ dynamic views (auto-discovery, optionally filtered by keyword).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"q": map[string]any{
					"type":        "string",
					"description": "Optional keyword filter on view name (e.g. session, lock, sql).",
				},
			},
		},
	}
}

func (s *ViewsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "views",
		Usage:   "/views [<keyword>]",
		Examples: []string{
			"/views",
			"/views session",
			"/views lock",
			"/views sql",
		},
	}
}

// 分类规则: 关键字 → 主题
var topicPatterns = []struct {
	topic    string
	patterns []string
}{
	{"session", []string{"SESSION", "CONNECT", "STMT"}},
	{"lock", []string{"LOCK", "TRX", "DEADLOCK"}},
	{"wait", []string{"WAIT", "EVENT"}},
	{"sql", []string{"SQL", "PARSE", "PLAN", "CACHE"}},
	{"memory", []string{"BUFFER", "MEM_POOL", "DICT", "POOL"}},
	{"storage", []string{"DATAFILE", "TABLESPACE", "RLOG", "CKPT"}},
	{"system", []string{"INSTANCE", "VERSION", "DATABASE", "PROCESS", "THREAD", "PARAMETER", "DM_INI"}},
	{"stats", []string{"STAT", "HISTORY", "RESOURCE_LIMIT"}},
	{"err", []string{"ERR", "DANGER"}},
}

func classifyView(name string) string {
	upper := strings.ToUpper(name)
	for _, t := range topicPatterns {
		for _, p := range t.patterns {
			if strings.Contains(upper, p) {
				return t.topic
			}
		}
	}
	return "other"
}

func (s *ViewsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	kw := strings.TrimSpace(params.StringOr("q", params.StringOr("args", "")))

	// 简单转义
	for _, r := range kw {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return &skill.Result{
				Type:     skill.ResultError,
				Rendered: "views 关键字仅允许字母/数字/下划线",
				Summary:  "invalid keyword",
			}, nil
		}
	}

	// V$DYNAMIC_TABLES 是 DM 自己的动态视图目录 (实测 380 项, 真机验证).
	// 列: NAME, ID, SCHNAME, SYNONYMS
	// SYSOBJECTS 只能找到注册成普通视图的 ~10 项, 不是真实数据源.
	var sqlStr string
	if kw == "" {
		sqlStr = `SELECT NAME, SCHNAME FROM V$DYNAMIC_TABLES ORDER BY NAME`
	} else {
		kwUpper := strings.ToUpper(kw)
		sqlStr = fmt.Sprintf(
			`SELECT NAME, SCHNAME FROM V$DYNAMIC_TABLES WHERE UPPER(NAME) LIKE '%%%s%%' ORDER BY NAME`,
			kwUpper,
		)
	}

	r, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("dm views: %w", err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("无 V$* 视图匹配 %q\n[summary]\nview_count: 0\nkeyword: %s\n", kw, kw),
			Summary:  fmt.Sprintf("views — 0 match for %q", kw),
		}, nil
	}

	// 按主题分类计数
	topicCount := make(map[string]int)
	for _, row := range r.Rows {
		if len(row) == 0 || row[0] == nil {
			continue
		}
		topicCount[classifyView(fmt.Sprintf("%v", row[0]))]++
	}

	entries := []dmutil.SummaryEntry{
		{Key: "view_count", Val: len(r.Rows)},
	}
	if kw != "" {
		entries = append(entries, dmutil.SummaryEntry{Key: "keyword", Val: kw})
	}
	for topic, n := range topicCount {
		entries = append(entries, dmutil.SummaryEntry{Key: "topic_" + topic, Val: n})
	}
	if len(r.Rows) > 0 {
		entries = append(entries, dmutil.SummaryEntry{Key: "first_view", Val: r.Rows[0][0]})
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("views — %d 项", len(r.Rows)),
	}, nil
}
