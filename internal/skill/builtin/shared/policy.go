/*-------------------------------------------------------------------------
 *
 * policy.go
 *	  PolicySkill manages diagnostic policies (rules) for org and
 *	  instance levels. Policies are read-only to the LLM — only humans
 *	  can add/remove via this skill.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/policy.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/skill"
)

// PolicySkill manages diagnostic policies (rules) for org and instance levels.
// Policies are read-only to the LLM — only humans can add/remove via this skill.
type PolicySkill struct {
	policiesDir func() string // returns ~/.opendb/policies/
	instanceFn  func() string // returns current instance name
}

// NewPolicySkill creates a PolicySkill.
// policiesDir returns the base policies directory.
// instanceFn returns the current active instance name.
func NewPolicySkill(policiesDir func() string, instanceFn func() string) *PolicySkill {
	return &PolicySkill{policiesDir: policiesDir, instanceFn: instanceFn}
}

func (s *PolicySkill) Name() string        { return "policy" }
func (s *PolicySkill) Description() string  { return "诊断规范管理（导入、查看、删除）" }
func (s *PolicySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *PolicySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{} // Not exposed as LLM tool — CLI only
}

func (s *PolicySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "policy",
		Aliases:     []string{"import-policy"},
		Usage:       "/policy <list|show|add|remove> [参数]",
		Description: "管理诊断规范。规范控制 LLM 的诊断行为，如禁止某些操作、要求附加风险评估等。\n组织规范优先级高于实例规范，LLM 必须遵守且不能修改。",
		Examples: []string{
			"/policy list",
			"/policy show safety",
			"/policy add org safety /tmp/rules.md",
			"/policy add instance strict",
			"/policy remove safety",
		},
		ArgCompletions: []string{"list", "show", "add", "remove"},
		SubCommands: []skill.SubCommandDef{
			{
				Name:        "list",
				Usage:       "list",
				Description: "列出所有已导入的规范（组织级 + 实例级）",
				Examples:    []string{"/policy list"},
			},
			{
				Name:        "show",
				Usage:       "show <名称>",
				Description: "查看规范的完整内容。如果组织和实例下有同名规范，会提示选择",
				Examples:    []string{"/policy show safety"},
			},
			{
				Name:        "add",
				Usage:       "add <org|instance> <名称> [文件路径]",
				Description: "导入规范。级别: org=组织规范（所有实例生效）, instance=实例规范（当前实例）。\n不指定文件路径时进入交互输入模式",
				Examples: []string{
					"/policy add org safety /tmp/company-rules.md",
					"/policy add instance strict",
				},
			},
			{
				Name:        "remove",
				Usage:       "remove <名称>",
				Description: "删除规范。删除前会显示规范级别并要求确认",
				Examples:    []string{"/policy remove safety"},
			},
		},
	}
}

func (s *PolicySkill) Validate(params skill.Params) error {
	sub := params.StringOr("_sub", "")
	if sub == "" {
		sub = params.StringOr("_arg0", "")
	}

	switch sub {
	case "list", "":
		return nil
	case "show":
		name := params.StringOr("_arg1", "")
		if name == "" {
			return fmt.Errorf("缺少规范名称")
		}
		return nil
	case "add":
		level := params.StringOr("_arg1", "")
		if level != "org" && level != "instance" {
			return fmt.Errorf("级别必须是 org 或 instance")
		}
		name := params.StringOr("_arg2", "")
		if name == "" {
			return fmt.Errorf("缺少规范名称")
		}
		return nil
	case "remove":
		name := params.StringOr("_arg1", "")
		if name == "" {
			return fmt.Errorf("缺少规范名称")
		}
		return nil
	default:
		return fmt.Errorf("未知子命令: %s", sub)
	}
}

func (s *PolicySkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	sub := params.StringOr("_sub", "")
	if sub == "" {
		sub = params.StringOr("_arg0", "")
	}
	if sub == "" {
		sub = "list"
	}

	switch sub {
	case "list":
		return s.execList()
	case "show":
		return s.execShow(params.StringOr("_arg1", ""))
	case "add":
		level := params.StringOr("_arg1", "")
		name := params.StringOr("_arg2", "")
		filePath := params.StringOr("_arg3", "")
		return s.execAdd(level, name, filePath)
	case "remove":
		return s.execRemove(params.StringOr("_arg1", ""))
	default:
		return textResult(fmt.Sprintf("未知子命令: %s", sub)), nil
	}
}

// ── Subcommand implementations ──

func (s *PolicySkill) execList() (*skill.Result, error) {
	dir := s.policiesDir()
	if dir == "" {
		return textResult("未配置规范目录"), nil
	}

	var buf strings.Builder

	// Org policies
	orgFiles := listPolicyFiles(filepath.Join(dir, "org"))
	if len(orgFiles) > 0 {
		buf.WriteString("  # 组织规范（优先级最高）\n")
		for i, f := range orgFiles {
			firstLine := readFirstLine(f.path)
			buf.WriteString(fmt.Sprintf("  %d. %-16s %q  (%s)\n", i+1, f.name, firstLine, humanAge(f.mtime)))
		}
		buf.WriteString("\n")
	}

	// Instance policies
	inst := s.instanceFn()
	if inst != "" {
		instFiles := listPolicyFiles(filepath.Join(dir, inst))
		if len(instFiles) > 0 {
			buf.WriteString(fmt.Sprintf("  # 实例规范（%s）\n", inst))
			offset := len(orgFiles)
			for i, f := range instFiles {
				firstLine := readFirstLine(f.path)
				buf.WriteString(fmt.Sprintf("  %d. %-16s %q  (%s)\n", offset+i+1, f.name, firstLine, humanAge(f.mtime)))
			}
			buf.WriteString("\n")
		}
	}

	if buf.Len() == 0 {
		return textResult("  暂无规范。使用 /policy add org <名称> 导入"), nil
	}

	return textResult(buf.String()), nil
}

func (s *PolicySkill) execShow(name string) (*skill.Result, error) {
	matches := s.findByName(name)
	if len(matches) == 0 {
		return textResult(fmt.Sprintf("  未找到规范: %s", name)), nil
	}

	// If multiple matches, show selection prompt
	if len(matches) > 1 {
		var buf strings.Builder
		buf.WriteString(fmt.Sprintf("  找到 %d 个同名规范:\n", len(matches)))
		for i, m := range matches {
			buf.WriteString(fmt.Sprintf("  %d. %s [%s]\n", i+1, m.name, m.level))
		}
		buf.WriteString("  请指定级别: /policy show <名称>  (用 org/ 或 instance/ 前缀区分)")
		return textResult(buf.String()), nil
	}

	// Show the single match
	m := matches[0]
	data, err := os.ReadFile(m.path)
	if err != nil {
		return textResult(fmt.Sprintf("  读取失败: %v", err)), nil
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("  ┌─ %s [%s] ─\n", m.name, m.level))
	buf.WriteString("  │\n")
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		buf.WriteString(fmt.Sprintf("  │ %s\n", line))
	}
	buf.WriteString("  │\n")
	buf.WriteString(fmt.Sprintf("  │ 级别: %s\n", m.level))
	buf.WriteString(fmt.Sprintf("  │ 导入时间: %s\n", m.mtime.Format("2006-01-02")))
	buf.WriteString("  └─\n")

	return textResult(buf.String()), nil
}

func (s *PolicySkill) execAdd(level, name, filePath string) (*skill.Result, error) {
	dir := s.policiesDir()
	if dir == "" {
		return textResult("  未配置规范目录"), nil
	}

	var targetDir string
	switch level {
	case "org":
		targetDir = filepath.Join(dir, "org")
	case "instance":
		inst := s.instanceFn()
		if inst == "" {
			return textResult("  未连接到任何实例"), nil
		}
		targetDir = filepath.Join(dir, inst)
	}

	if err := os.MkdirAll(targetDir, 0750); err != nil {
		return textResult(fmt.Sprintf("  创建目录失败: %v", err)), nil
	}

	target := filepath.Join(targetDir, name+".md")

	if filePath != "" {
		// Import from file
		data, err := os.ReadFile(filePath)
		if err != nil {
			return textResult(fmt.Sprintf("  读取文件失败: %v", err)), nil
		}
		if err := os.WriteFile(target, data, 0640); err != nil {
			return textResult(fmt.Sprintf("  写入失败: %v", err)), nil
		}
	} else {
		// No file path — create an empty template for the user to edit
		template := fmt.Sprintf("# %s\n\n- (请编辑此文件添加规范内容)\n", name)
		if err := os.WriteFile(target, []byte(template), 0640); err != nil {
			return textResult(fmt.Sprintf("  写入失败: %v", err)), nil
		}
		return textResult(fmt.Sprintf("  ✓ 已创建: %s\n  请编辑文件添加内容: %s", name, target)), nil
	}

	levelLabel := "组织规范"
	if level == "instance" {
		levelLabel = "实例规范"
	}
	return textResult(fmt.Sprintf("  ✓ 已导入 %s: %s", levelLabel, name)), nil
}

func (s *PolicySkill) execRemove(name string) (*skill.Result, error) {
	matches := s.findByName(name)
	if len(matches) == 0 {
		return textResult(fmt.Sprintf("  未找到规范: %s", name)), nil
	}

	if len(matches) > 1 {
		var buf strings.Builder
		buf.WriteString(fmt.Sprintf("  找到 %d 个同名规范，请指定:\n", len(matches)))
		for i, m := range matches {
			buf.WriteString(fmt.Sprintf("  %d. %s [%s]\n", i+1, m.name, m.level))
		}
		return textResult(buf.String()), nil
	}

	m := matches[0]
	if err := os.Remove(m.path); err != nil {
		return textResult(fmt.Sprintf("  删除失败: %v", err)), nil
	}

	return textResult(fmt.Sprintf("  ✓ 已删除 %s [%s]", m.name, m.level)), nil
}

// ── Helpers ──

type policyMatch struct {
	name  string
	level string // "组织规范" or "实例规范"
	path  string
	mtime time.Time
}

func (s *PolicySkill) findByName(name string) []policyMatch {
	dir := s.policiesDir()
	if dir == "" {
		return nil
	}

	var matches []policyMatch

	// Check org
	orgPath := filepath.Join(dir, "org", name+".md")
	if info, err := os.Stat(orgPath); err == nil {
		matches = append(matches, policyMatch{name: name, level: "组织规范", path: orgPath, mtime: info.ModTime()})
	}

	// Check instance
	inst := s.instanceFn()
	if inst != "" {
		instPath := filepath.Join(dir, inst, name+".md")
		if info, err := os.Stat(instPath); err == nil {
			matches = append(matches, policyMatch{name: name, level: "实例规范", path: instPath, mtime: info.ModTime()})
		}
	}

	return matches
}

type policyFile struct {
	name  string
	path  string
	mtime time.Time
}

func listPolicyFiles(dir string) []policyFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []policyFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		files = append(files, policyFile{
			name:  name,
			path:  filepath.Join(dir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})
	return files
}

func readFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.SplitN(string(data), "\n", 3)
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if trimmed != "" {
			if len(trimmed) > 40 {
				return trimmed[:40] + "..."
			}
			return trimmed
		}
	}
	return "(空)"
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	hours := int(d.Hours())
	if hours < 1 {
		return "刚刚"
	}
	if hours < 24 {
		return fmt.Sprintf("%d小时前", hours)
	}
	days := hours / 24
	if days == 1 {
		return "昨天"
	}
	if days < 30 {
		return fmt.Sprintf("%d天前", days)
	}
	return fmt.Sprintf("%d月前", days/30)
}

func textResult(text string) *skill.Result {
	return &skill.Result{Type: skill.ResultText, Rendered: text}
}
