/*-------------------------------------------------------------------------
 * skills.go
 *    /skills management command for customer-provided pluggable skills.
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/external"
)

// SkillsSkill manages external skills loaded from ~/.opendb/skills.
type SkillsSkill struct {
	registry *skill.Registry
	manager  *external.Manager
	mcp      *external.MCPAdapter
}

func NewSkillsSkill(registry *skill.Registry, manager *external.Manager, mcpCfg config.MCPConfig) *SkillsSkill {
	return &SkillsSkill{registry: registry, manager: manager, mcp: external.NewMCPAdapter(mcpCfg)}
}

func (s *SkillsSkill) Name() string        { return "skills" }
func (s *SkillsSkill) Description() string { return "Manage pluggable skills" }
func (s *SkillsSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *SkillsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "skills",
		Description: "List, inspect, reload, or scaffold pluggable DBAA skills",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{"type": "string", "description": "Subcommand: list/show/doctor/reload/init/run"},
			},
		},
	}
}

func (s *SkillsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "skills",
		Aliases:     []string{"skill"},
		Usage:       "/skills <list|show|doctor|reload|init|run>",
		Description: "管理客户放在 external_skills.dirs 下的可插拔 skill。/help 仍负责展示全部命令；/skills 负责外部 skill 的发现、检查和重载。",
		Examples: []string{
			"/skills list",
			"/skills show my_check",
			"/skills doctor",
			"/skills init my_check",
		},
		ArgCompletions: []string{"list", "show", "doctor", "reload", "init", "run"},
		SubCommands: []skill.SubCommandDef{
			{Name: "list", Usage: "list", Description: "列出已加载的外部 skill", Examples: []string{"/skills list"}},
			{Name: "show", Usage: "show <name>", Description: "查看外部 skill 的 manifest 摘要", Examples: []string{"/skills show my_check"}},
			{Name: "doctor", Usage: "doctor", Description: "检查外部 skill 目录、加载错误和 MCP 配置状态", Examples: []string{"/skills doctor"}},
			{Name: "reload", Usage: "reload", Description: "重新扫描 external_skills.dirs 并注册外部 skill", Examples: []string{"/skills reload"}},
			{Name: "init", Usage: "init <name>", Description: "在默认 skill 目录创建脚本 skill 模板", Examples: []string{"/skills init my_check"}},
			{Name: "run", Usage: "run <name> [json-params]", Description: "直接运行一个已加载的 read_only 外部 skill", Examples: []string{"/skills run my_check {\"schema\":\"public\"}"}},
		},
	}
}

func (s *SkillsSkill) Validate(_ skill.Params) error { return nil }

func (s *SkillsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		args = "list"
	}
	parts := strings.Fields(args)
	cmd := strings.ToLower(parts[0])
	rest := strings.TrimSpace(strings.TrimPrefix(args, parts[0]))

	switch cmd {
	case "list", "ls":
		return skillsTextResult(s.renderList()), nil
	case "show":
		if len(parts) < 2 {
			return skillsTextResult("用法: /skills show <name>"), nil
		}
		return skillsTextResult(s.renderShow(parts[1])), nil
	case "doctor", "check":
		return skillsTextResult(s.renderDoctor()), nil
	case "reload":
		return s.reload()
	case "init":
		if len(parts) < 2 {
			return skillsTextResult("用法: /skills init <name>"), nil
		}
		return s.init(parts[1])
	case "run":
		if len(parts) < 2 {
			return skillsTextResult("用法: /skills run <name> [json-params]"), nil
		}
		jsonArg := strings.TrimSpace(strings.TrimPrefix(rest, parts[1]))
		return s.runExternal(ctx, parts[1], jsonArg)
	default:
		return skillsTextResult(fmt.Sprintf("未知子命令: %s\n用法: /skills <list|show|doctor|reload|init|run>", cmd)), nil
	}
}

func (s *SkillsSkill) renderList() string {
	if s.manager == nil || !s.manager.Enabled() {
		return format.Panel("External Skills", []format.PanelSection{{Lines: []string{"外部 skill 未启用。配置 external_skills.enabled: true 后重启或 /skills reload。"}}})
	}
	infos := s.manager.List()
	if len(infos) == 0 {
		return format.Panel("External Skills", []format.PanelSection{{Lines: []string{fmt.Sprintf("未加载外部 skill。默认目录: %s", s.manager.DefaultDir()), "创建模板: /skills init <name>"}}})
	}
	lines := []string{fmt.Sprintf("%-24s %-8s %-9s %-17s %s", "NAME", "KIND", "SECURITY", "DB_TYPES", "DESCRIPTION")}
	for _, info := range infos {
		lines = append(lines, fmt.Sprintf("%-24s %-8s %-9s %-17s %s",
			ellipsizeWidth(info.Name, 24),
			ellipsizeWidth(string(info.Kind), 8),
			ellipsizeWidth(info.Security, 9),
			ellipsizeWidth(strings.Join(info.DBTypes, ","), 17),
			ellipsizeWidth(info.Description, 44),
		))
	}
	return format.Panel("External Skills", []format.PanelSection{{Header: fmt.Sprintf("%d loaded", len(infos)), Lines: lines}})
}

func (s *SkillsSkill) renderShow(name string) string {
	if s.manager == nil {
		return fmt.Sprintf("未找到外部 skill: %s", name)
	}
	info, ok := s.manager.Get(name)
	if !ok {
		return fmt.Sprintf("未找到外部 skill: %s", name)
	}
	var lines []string
	lines = appendWrappedField(lines, "Name", info.Name)
	lines = appendWrappedField(lines, "Title", dash(info.Title))
	lines = appendWrappedField(lines, "Kind", string(info.Kind))
	lines = appendWrappedField(lines, "Security", info.Security)
	lines = appendWrappedField(lines, "DB Types", strings.Join(info.DBTypes, ", "))
	lines = appendWrappedField(lines, "Timeout", info.Timeout.String())
	lines = appendWrappedField(lines, "Path", info.Path)
	lines = appendWrappedField(lines, "Command", strings.Join(info.Command, " "))
	lines = appendWrappedField(lines, "Triggers", dash(strings.Join(info.Triggers, ", ")))
	lines = appendWrappedField(lines, "Tags", dash(strings.Join(info.Tags, ", ")))
	lines = appendWrappedField(lines, "Hash", shortHash(info.ManifestHash))
	lines = appendWrappedField(lines, "Desc", info.Description)
	return format.Panel("External Skill", []format.PanelSection{{Lines: lines}})
}

const externalSkillPanelLineWidth = 112

func ellipsizeWidth(s string, maxW int) string {
	s = strings.TrimSpace(s)
	if maxW <= 0 || format.DisplayWidth(s) <= maxW {
		return s
	}
	if maxW <= 3 {
		return format.TruncateWidth(s, maxW)
	}
	return strings.TrimSpace(format.TruncateWidth(s, maxW-3)) + "..."
}

func appendWrappedField(lines []string, label, value string) []string {
	const labelW = 10
	value = strings.TrimSpace(value)
	if value == "" {
		value = "-"
	}
	prefix := fmt.Sprintf("%-*s: ", labelW, label)
	contPrefix := strings.Repeat(" ", format.DisplayWidth(prefix))
	maxValueW := externalSkillPanelLineWidth - format.DisplayWidth(prefix)
	if maxValueW < 24 {
		maxValueW = 24
	}
	wrapped := wrapDisplay(value, maxValueW)
	if len(wrapped) == 0 {
		return append(lines, prefix+"-")
	}
	lines = append(lines, prefix+wrapped[0])
	for _, line := range wrapped[1:] {
		lines = append(lines, contPrefix+line)
	}
	return lines
}

func wrapDisplay(s string, maxW int) []string {
	if format.DisplayWidth(s) <= maxW {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var out []string
	var cur string
	flush := func() {
		if cur == "" {
			return
		}
		out = append(out, cur)
		cur = ""
	}
	for _, word := range words {
		if format.DisplayWidth(word) > maxW {
			flush()
			out = append(out, hardWrapDisplay(word, maxW)...)
			continue
		}
		next := word
		if cur != "" {
			next = cur + " " + word
		}
		if format.DisplayWidth(next) <= maxW {
			cur = next
			continue
		}
		flush()
		cur = word
	}
	flush()
	return out
}

func hardWrapDisplay(s string, maxW int) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		next := cur.String() + string(r)
		if cur.Len() > 0 && format.DisplayWidth(next) > maxW {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func (s *SkillsSkill) renderDoctor() string {
	var sections []format.PanelSection
	if s.manager == nil || !s.manager.Enabled() {
		sections = append(sections, format.PanelSection{Header: "Script Skills", Lines: []string{"external_skills.enabled=false 或 manager 未初始化"}})
	} else {
		lines := []string{
			fmt.Sprintf("默认目录: %s", s.manager.DefaultDir()),
			fmt.Sprintf("已加载: %d", len(s.manager.List())),
		}
		if errs := s.manager.LastErrors(); len(errs) > 0 {
			for _, err := range errs {
				lines = append(lines, "错误: "+err.Error())
			}
		} else {
			lines = append(lines, "状态: ok")
		}
		sections = append(sections, format.PanelSection{Header: "Script Skills", Lines: lines})
	}
	mcpLines := []string{fmt.Sprintf("enabled: %v", s.mcp.Enabled())}
	servers := s.mcp.Servers()
	if len(servers) > 0 {
		mcpLines = append(mcpLines, fmt.Sprintf("servers: %s", strings.Join(servers, ", ")))
	}
	tools := s.mcp.ToolInfos()
	if len(tools) > 0 {
		mcpLines = append(mcpLines, fmt.Sprintf("allowlisted tools: %d", len(tools)))
		for _, tool := range tools {
			mcpLines = append(mcpLines, fmt.Sprintf("- %s/%s -> %s (%s)", tool.Server, tool.ToolKey, tool.Name, dash(tool.Security)))
		}
	} else {
		mcpLines = append(mcpLines, "allowlisted tools: 0")
	}
	sections = append(sections, format.PanelSection{Header: "MCP Adapter", Lines: mcpLines})
	return format.Panel("Skills Doctor", sections)
}

func (s *SkillsSkill) reload() (*skill.Result, error) {
	if s.manager == nil || !s.manager.Enabled() {
		return skillsTextResult("外部 skill 未启用。"), nil
	}
	if err := s.manager.Reload(s.registry); err != nil {
		return skillsTextResult("外部 skill 重载失败: " + err.Error()), nil
	}
	return skillsTextResult(fmt.Sprintf("外部 skill 已重载: %d 个", len(s.manager.List()))), nil
}

func (s *SkillsSkill) init(name string) (*skill.Result, error) {
	if s.manager == nil {
		return skillsTextResult("外部 skill manager 未初始化。"), nil
	}
	if !simpleSkillName(name) {
		return skillsTextResult("skill 名称必须匹配 ^[A-Za-z][A-Za-z0-9_]{1,63}$"), nil
	}
	dir := filepath.Join(s.manager.DefaultDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return skillsTextResult("目录已存在: " + dir), nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	manifest := externalSkillTemplate(name)
	runner := externalRunnerTemplate()
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		return nil, err
	}
	runPath := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(runPath, []byte(runner), 0755); err != nil {
		return nil, err
	}
	return skillsTextResult(fmt.Sprintf("已创建外部 skill 模板: %s\n下一步: 编辑 skill.md/run.sh 后执行 /skills reload", dir)), nil
}

func (s *SkillsSkill) runExternal(ctx context.Context, name, jsonArg string) (*skill.Result, error) {
	if s.manager == nil {
		return skillsTextResult("未找到外部 skill: " + name), nil
	}
	info, ok := s.manager.Get(name)
	if !ok {
		return skillsTextResult("未找到外部 skill: " + name), nil
	}
	sk, ok := s.registry.Get(name)
	if !ok {
		return skillsTextResult(fmt.Sprintf("skill 已加载，但当前连接未匹配它的 db_types；适用 DB: %s", strings.Join(info.DBTypes, ", "))), nil
	}
	if sk.SecurityLevel() != skill.LevelReadOnly {
		return skillsTextResult("/skills run 只允许直接运行 read_only 外部 skill；更高权限请直接执行 /" + name + " 让 DBAA 权限守卫接管。"), nil
	}
	raw := map[string]any{}
	if jsonArg != "" {
		if err := json.Unmarshal([]byte(jsonArg), &raw); err != nil {
			return skillsTextResult("json-params 解析失败: " + err.Error()), nil
		}
	}
	params := skill.ParamsFromMap(raw)
	if err := sk.Validate(params); err != nil {
		return nil, err
	}
	return sk.Execute(ctx, params)
}

func skillsTextResult(text string) *skill.Result {
	return &skill.Result{Type: skill.ResultText, Data: text, Rendered: text, Summary: firstLineLocal(text)}
}

func firstLineLocal(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func shortHash(s string) string {
	if len(s) <= 12 {
		return dash(s)
	}
	return s[:12]
}

func simpleSkillName(name string) bool {
	if len(name) < 2 || len(name) > 64 {
		return false
	}
	for i, r := range name {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if !ok || (i == 0 && !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))) {
			return false
		}
	}
	return true
}

func externalSkillTemplate(name string) string {
	return fmt.Sprintf(`---
api_version: opendb.skill/v1
name: %s
title: %s
description: Customer read-only check
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 30s
command: ["./run.sh"]
parameters:
  type: object
  properties:
    args:
      type: string
      description: Optional free-form arguments
triggers:
  - %s
---

这个文件定义 DBAA 可插拔 skill。脚本从 stdin 读取 JSON，向 stdout 输出文本或 JSON。
`, name, name, strings.ReplaceAll(name, "_", " "))
}

func externalRunnerTemplate() string {
	return `#!/bin/sh
set -eu
payload=$(cat)
# payload JSON shape:
# {"api_version":"opendb.skill/v1","skill":"...","params":{...},"context":{"db_type":"gaussdb","connection":"..."}}
skill_name=$(printf '%s' "$payload" | sed -n 's/.*"skill":"\([^"]*\)".*/\1/p')
input_bytes=$(printf '%s' "$payload" | wc -c | tr -d ' ')
printf '外部 skill 已执行。\nSkill: %s\nInput bytes: %s\n' "$skill_name" "$input_bytes"
`
}
