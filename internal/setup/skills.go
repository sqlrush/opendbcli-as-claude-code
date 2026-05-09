/*-------------------------------------------------------------------------
 *
 * skills.go
 *	  SkillEntry describes a single skill with DB support info
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/skills.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/sqlrush/opendb/internal/brand"
)

// SkillEntry describes a single skill with DB support info
type SkillEntry struct {
	Command string
	Desc    string
	Oracle  bool
	MySQL   bool
	PgSQL   bool
}

// SkillCategory groups related skills
type SkillCategory struct {
	Name   string
	Skills []SkillEntry
}

// FeatureHighlight describes a key differentiating feature
type FeatureHighlight struct {
	Name string
	Desc string
}

// DefaultFeatureHighlights returns OpenDB's differentiating features
func DefaultFeatureHighlights() []FeatureHighlight {
	return []FeatureHighlight{
		{
			Name: "Sentinel - 实时异常检测",
			Desc: "后台常驻探针，每秒采集 7 项核心指标，自适应 3σ 基线检测。\n" +
				"异常时自动 200ms 高频采集，捕获现场并生成根因分析。",
		},
		{
			Name: "Model - 大模型智能诊断",
			Desc: "多轮链式推理（最多 20 轮），通过 function calling\n" +
				"调用 30+ 内置 skill，逐步收敛定位根因，给出可执行 SQL。",
		},
		{
			Name: "Rule Engine - 规则决策引擎",
			Desc: "273 条诊断规则，离线可用，毫秒级响应。\n" +
				"Model 不可用时自动兜底。",
		},
		{
			Name: "dbtop - 实时性能面板",
			Desc: "类似 top 的数据库版本，1 秒刷新。\n" +
				"全局指标 + Top 等待 + 活跃会话，一眼掌握状态。",
		},
	}
}

// DefaultSkillCategories returns read-only skills (no modification skills in this version)
func DefaultSkillCategories() []SkillCategory {
	all := true
	ora := true
	return []SkillCategory{
		{
			Name: "AI 智能诊断",
			Skills: []SkillEntry{
				{"/sentinel", "实时异常检测探针", all, all, all},
				{"/llm", "大模型多轮链式诊断", all, all, all},
				{"/rule", "规则引擎诊断(273条)", all, all, all},
				{"/diag", "自动诊断", all, all, all},
				{"/ora", "ORA错误码知识库", ora, false, false},
				{"/myerr", "MySQL错误码知识库", false, all, false},
				{"/pgerr", "PG错误码知识库", false, false, all},
			},
		},
		{
			Name: "实时监控",
			Skills: []SkillEntry{
				{"/dbtop", "实时性能面板(类似top)", all, all, all},
				{"/health", "全维度健康体检(20+项)", all, all, all},
				{"/sessions", "全部会话概览", all, all, all},
				{"/waits", "等待事件统计", all, all, all},
				{"/os", "操作系统指标", all, all, all},
			},
		},
		{
			Name: "会话与锁",
			Skills: []SkillEntry{
				{"/locks", "行锁和表锁信息", all, all, all},
				{"/blocktree", "锁阻塞链树状可视化", all, all, all},
				{"/latches", "Latch争用分析", ora, false, false},
				{"/deadlock", "死锁检测和分析", false, all, false},
			},
		},
		{
			Name: "SQL 分析",
			Skills: []SkillEntry{
				{"/slowsql", "慢SQL查找", all, all, all},
				{"/topsql", "TopSQL排名", all, all, all},
				{"/explain", "SQL执行计划分析", all, all, all},
				{"/awr", "AWR快照分析", ora, false, false},
				{"/ash", "ASH采样分析", ora, all, all},
			},
		},
		{
			Name: "内存与存储",
			Skills: []SkillEntry{
				{"/space", "表空间使用率", all, all, all},
				{"/sga", "SGA内存分解", ora, false, false},
				{"/pga", "PGA内存详情", ora, false, false},
				{"/redo", "Redo日志状态", ora, false, false},
				{"/wal", "WAL日志状态", false, false, all},
				{"/bufferpool", "BufferPool状态", false, all, false},
				{"/bloat", "表膨胀分析", false, false, all},
			},
		},
		{
			Name: "运维查询",
			Skills: []SkillEntry{
				{"/params", "参数查询", all, all, all},
				{"/alert", "告警日志", all, all, all},
				{"/backup", "备份状态", all, all, all},
				{"/jobs", "调度任务", all, all, all},
				{"/scheduler", "定时巡检引擎", all, all, all},
			},
		},
		{
			Name: "高可用",
			Skills: []SkillEntry{
				{"/standby", "DataGuard状态", ora, false, false},
				{"/replication", "复制状态", false, all, false},
				{"/slots", "复制槽", false, false, all},
			},
		},
		{
			Name: "系统工具",
			Skills: []SkillEntry{
				{"/help", "命令列表", all, all, all},
				{"/login", "连接数据库", all, all, all},
				{"/conn", "连接信息", all, all, all},
				{"/model", "模型管理", all, all, all},
				{"/config", "配置管理", all, all, all},
			},
		},
	}
}

// SkillsStep showcases features and skills table
type SkillsStep struct {
	categories []SkillCategory
	highlights []FeatureHighlight
	done       bool
}

func NewSkillsStep() *SkillsStep {
	return &SkillsStep{
		categories: DefaultSkillCategories(),
		highlights: DefaultFeatureHighlights(),
	}
}

func (s *SkillsStep) Title() string { return "Skills" }
func (s *SkillsStep) Done() bool    { return s.done }

func (s *SkillsStep) Summary() string {
	total := 0
	for _, cat := range s.categories {
		total += len(cat.Skills)
	}
	return CompletedLine("Skills", fmt.Sprintf("%d commands available", total))
}

func (s *SkillsStep) Init() tea.Cmd { return nil }

func (s *SkillsStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "enter" {
			s.done = true
			return s, emitDone
		}
	}
	return s, nil
}

// Column widths for the skills table
const (
	skillCmdW  = 16
	skillDescW = 24
	skillDBW   = 6
)

func (s *SkillsStep) View() string {
	var b strings.Builder

	// Feature highlights
	b.WriteString("\n")
	hlContent := ""
	for i, hl := range s.highlights {
		hlContent += StyleHighlight.Render("* "+hl.Name) + "\n"
		hlContent += StyleDim.Render(hl.Desc)
		if i < len(s.highlights)-1 {
			hlContent += "\n\n"
		}
	}
	b.WriteString(InfoPanel("特色功能", hlContent))
	b.WriteString("\n")

	// Skills table
	total := 0
	for _, cat := range s.categories {
		total += len(cat.Skills)
	}
	b.WriteString("\n" + InfoPanel("技能一览",
		fmt.Sprintf("%s 内置 %d+ 数据库管理技能。\n"+
			"Ora=Oracle  My=MySQL  PG=PostgreSQL", brand.Current().AppName, total)))
	b.WriteString("\n")

	for _, cat := range s.categories {
		b.WriteString("\n")
		b.WriteString("  " + StyleHighlight.Render("[ "+cat.Name+" ]") + "\n")
		b.WriteString(skillRow("Skill", "说明", "Ora", "My", "PG", true))
		b.WriteString("  " + strings.Repeat("-", skillCmdW+skillDescW+skillDBW*3) + "\n")

		for _, sk := range cat.Skills {
			ora := dbMark(sk.Oracle)
			my := dbMark(sk.MySQL)
			pg := dbMark(sk.PgSQL)
			desc := truncRW(sk.Desc, skillDescW-2)
			b.WriteString(skillRow(sk.Command, desc, ora, my, pg, false))
		}
	}

	b.WriteString("\n" + StyleDim.Render("  Press Enter to continue") + "\n")
	return b.String()
}

// skillRow builds one aligned row for the skills table
func skillRow(cmd, desc, ora, my, pg string, isHeader bool) string {
	var sb strings.Builder
	sb.WriteString("  ")

	// Column 1: Command
	if isHeader {
		sb.WriteString(StyleBrand.Render(cmd))
	} else {
		sb.WriteString(StyleBrand.Render(cmd))
	}
	sb.WriteString(rwPad(cmd, skillCmdW))

	// Column 2: Description
	sb.WriteString(desc)
	sb.WriteString(rwPad(desc, skillDescW))

	// Column 3-5: DB support (fixed width, single char)
	sb.WriteString(ora)
	sb.WriteString(rwPad(ora, skillDBW))
	sb.WriteString(my)
	sb.WriteString(rwPad(my, skillDBW))
	sb.WriteString(pg)
	sb.WriteString(rwPad(pg, skillDBW))

	sb.WriteString("\n")
	return sb.String()
}

// rwPad returns spaces needed to pad s to target display width
func rwPad(s string, targetW int) string {
	w := runewidth.StringWidth(s)
	pad := targetW - w
	if pad <= 0 {
		return ""
	}
	return strings.Repeat(" ", pad)
}

func dbMark(supported bool) string {
	if supported {
		return "Y"
	}
	return "-"
}

// padRW pads a string to the given display width
func padRW(s string, width int) string {
	return s + rwPad(s, width)
}

// truncRW truncates a string to fit within maxWidth display columns
func truncRW(s string, maxWidth int) string {
	w := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxWidth {
			return s[:i] + ".."
		}
		w += rw
	}
	return s
}
