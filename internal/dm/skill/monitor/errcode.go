/*-------------------------------------------------------------------------
 *
 * errcode.go
 *	  Package monitor — DM errcode skill: 查 V$ERR_INFO
 *	  错误码字典, 给 LLM 解读具体错误时用.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/errcode.go
 *
 *-------------------------------------------------------------------------
 */
// Package monitor — DM errcode skill: 查 V$ERR_INFO 错误码字典,
// 给 LLM 解读具体错误时用.
//
// 真机实测列名 (DM 8.1.4.200):
//   V$ERR_INFO              : CODE (INTEGER), ERRINFO (VARCHAR)  ← 仅 2 列
//   V$RUNTIME_ERR_HISTORY   : ECPT_CODE (错误码), ECPT_DESC (描述), ERR_TIME, ...
//
// 用法:
//   /errcode 9042       — 查精确码 (前缀 ERR- 可省)
//   /errcode locked     — 模糊匹配 ERRINFO
//   /errcode            — 列出最近 V$RUNTIME_ERR_HISTORY 触发过的错误码
package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

type ErrCodeSkill struct{ driver db.Driver }

func NewErrCodeSkill(driver db.Driver) *ErrCodeSkill { return &ErrCodeSkill{driver: driver} }

func (s *ErrCodeSkill) Name() string                       { return "errcode" }
func (s *ErrCodeSkill) Description() string                { return "查 DM 错误码字典 V$ERR_INFO" }
func (s *ErrCodeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ErrCodeSkill) Validate(_ skill.Params) error { return nil }

func (s *ErrCodeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "errcode",
		Description: "Look up DM error code in V$ERR_INFO (exact code or fuzzy description).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"q": map[string]any{
					"type":        "string",
					"description": "Error code (e.g. 9042) or fuzzy description (e.g. 'locked'). Empty = recently triggered errors.",
				},
			},
		},
	}
}

func (s *ErrCodeSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "errcode",
		Usage:   "/errcode [<code>|<keyword>]",
		Examples: []string{
			"/errcode 9042",
			"/errcode locked",
			"/errcode",
		},
	}
}

func (s *ErrCodeSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	q := strings.TrimSpace(params.StringOr("q", params.StringOr("args", "")))
	q = strings.TrimPrefix(strings.ToUpper(q), "ERR-")
	q = strings.TrimPrefix(q, "ERR")

	if q == "" {
		return s.recent(ctx)
	}
	if _, err := strconv.Atoi(q); err == nil {
		return s.byCode(ctx, q)
	}
	return s.byDesc(ctx, q)
}

// byCode 精确匹配错误码 (支持负数).
func (s *ErrCodeSkill) byCode(ctx context.Context, code string) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, fmt.Sprintf(
		`SELECT CODE, ERRINFO FROM V$ERR_INFO WHERE CODE = %s OR CODE = -%s`,
		code, code,
	))
	if err != nil {
		return nil, fmt.Errorf("dm errcode by code %s: %w", code, err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("未找到错误码 %s\n[summary]\nfound: false\nsearched_code: %s\n", code, code),
			Summary:  fmt.Sprintf("errcode %s — not found", code),
		}, nil
	}
	entries := []dmutil.SummaryEntry{
		{Key: "found", Val: true},
		{Key: "match_count", Val: len(r.Rows)},
		{Key: "err_code", Val: r.Rows[0][0]},
		{Key: "err_desc", Val: r.Rows[0][1]},
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("errcode %s — %d match", code, len(r.Rows)),
	}, nil
}

// byDesc 模糊匹配 ERRINFO (LIKE %keyword%, 限 20 条).
func (s *ErrCodeSkill) byDesc(ctx context.Context, kw string) (*skill.Result, error) {
	for _, r := range kw {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') &&
			r != ' ' && r != '_' && !(r >= 0x4e00 && r <= 0x9fff) {
			return &skill.Result{
				Type:     skill.ResultError,
				Rendered: "错误码关键字仅允许字母/数字/中文/空格/下划线",
				Summary:  "invalid keyword",
			}, nil
		}
	}
	sqlStr := fmt.Sprintf(
		`SELECT CODE, ERRINFO FROM V$ERR_INFO WHERE UPPER(ERRINFO) LIKE UPPER('%%%s%%') LIMIT 20`,
		kw,
	)
	r, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("dm errcode by desc %s: %w", kw, err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("无错误码描述包含 %q\n[summary]\nfound: false\nsearched_keyword: %s\n", kw, kw),
			Summary:  fmt.Sprintf("errcode keyword %s — not found", kw),
		}, nil
	}
	entries := []dmutil.SummaryEntry{
		{Key: "found", Val: true},
		{Key: "match_count", Val: len(r.Rows)},
		{Key: "first_err_code", Val: r.Rows[0][0]},
		{Key: "first_err_desc", Val: r.Rows[0][1]},
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("errcode keyword %s — %d match", kw, len(r.Rows)),
	}, nil
}

// recent 列出最近 V$RUNTIME_ERR_HISTORY 触发过的错误码 + 描述
// (LEFT JOIN V$ERR_INFO, 限 20 条).
// V$RUNTIME_ERR_HISTORY 的错误码字段实测是 ECPT_CODE, 描述 ECPT_DESC.
func (s *ErrCodeSkill) recent(ctx context.Context) (*skill.Result, error) {
	sqlStr := `SELECT h.ECPT_CODE, COUNT(*) AS HIT_COUNT, MAX(NVL(i.ERRINFO, h.ECPT_DESC)) AS ERR_DESC
FROM V$RUNTIME_ERR_HISTORY h
LEFT JOIN V$ERR_INFO i ON i.CODE = h.ECPT_CODE
GROUP BY h.ECPT_CODE
ORDER BY HIT_COUNT DESC
LIMIT 20`
	r, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("dm errcode recent: %w", err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "近期无运行错误\n[summary]\nrecent_error_kinds: 0\n",
			Summary:  "no recent errors",
		}, nil
	}
	entries := []dmutil.SummaryEntry{
		{Key: "recent_error_kinds", Val: len(r.Rows)},
		{Key: "top_err_code", Val: r.Rows[0][0]},
		{Key: "top_err_count", Val: r.Rows[0][1]},
		{Key: "top_err_desc", Val: r.Rows[0][2]},
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("recent errors — %d kinds", len(r.Rows)),
	}, nil
}
