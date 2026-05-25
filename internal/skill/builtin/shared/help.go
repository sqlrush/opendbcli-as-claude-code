/*-------------------------------------------------------------------------
 *
 * help.go
 *	  HelpSkill lists all registered skills with their descriptions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/help.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// HelpSkill lists all registered skills with their descriptions.
type HelpSkill struct {
	registry *skill.Registry
}

// NewHelpSkill creates a HelpSkill that reads from the given registry.
func NewHelpSkill(registry *skill.Registry) *HelpSkill {
	return &HelpSkill{registry: registry}
}

func (s *HelpSkill) Name() string                       { return "help" }
func (s *HelpSkill) Description() string                { return "List available commands" }
func (s *HelpSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *HelpSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "help",
		Description: "List available commands",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Optional filter prefix for command names",
				},
			},
		},
	}
}

func (s *HelpSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "help",
		Aliases:  []string{"h", "?"},
		Usage:    "/help [filter]",
		Examples: []string{"/help", "/help login"},
	}
}

func (s *HelpSkill) Validate(_ skill.Params) error { return nil }

func (s *HelpSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	filter := params.StringOr("args", "")

	// ── Layer 2/3: /help <command> [subcommand] ──
	if filter != "" {
		parts := strings.Fields(filter)
		cmdName := parts[0]

		// Find the skill by name
		matched := s.registry.Match(cmdName)
		if len(matched) == 1 && matched[0].Name() == cmdName {
			sk := matched[0]
			cliDef := sk.CLIDef()

			// Layer 3: /help <command> <subcommand>
			if len(parts) > 1 {
				subName := parts[1]
				for _, sub := range cliDef.SubCommands {
					if sub.Name == subName {
						return &skill.Result{
							Type:     skill.ResultText,
							Rendered: renderSubCommandHelp(cmdName, sub),
							Data:     renderSubCommandHelp(cmdName, sub),
						}, nil
					}
				}
				return &skill.Result{
					Type: skill.ResultText,
					Data: fmt.Sprintf("未知子命令: /%s %s", cmdName, subName),
				}, nil
			}

			// Layer 2: /help <command>
			// If skill has SubCommands or Description, use structured help
			if cliDef.Description != "" || len(cliDef.SubCommands) > 0 {
				return &skill.Result{
					Type:     skill.ResultText,
					Rendered: renderCommandHelp(sk),
					Data:     renderCommandHelp(sk),
				}, nil
			}

			// Fallback to legacy detailedHelp
			if detail, ok := detailedHelp[cmdName]; ok {
				return &skill.Result{
					Type: skill.ResultText,
					Data: detail,
				}, nil
			}
		}
	}

	// ── Layer 1: /help (overview) or /help <filter> ──
	var skills []skill.Skill
	if filter != "" {
		skills = s.registry.Match(filter)
	} else {
		skills = s.registry.All()
	}

	if len(skills) == 0 {
		msg := "No commands found"
		if filter != "" {
			msg = fmt.Sprintf("No commands matching %q", filter)
		}
		return &skill.Result{
			Type: skill.ResultText,
			Data: msg,
		}, nil
	}

	// Single exact match with legacy detailed help
	if len(skills) == 1 {
		if detail, ok := detailedHelp[skills[0].Name()]; ok {
			return &skill.Result{
				Type: skill.ResultText,
				Data: detail,
			}, nil
		}
	}

	// ANSI color codes.
	const (
		ansiReset    = "\033[0m"
		ansiBoldGold = "\033[1;33m" // AI category (highlight)
		ansiBoldRed  = "\033[1;31m" // modification commands (kill, alter, resize)
	)

	// Group skills by category.
	categories := []struct {
		label     string
		names     []string
		highlight bool // use accent color for this category
	}{
		{"监控大盘", []string{"dbtop", "health", "perfsnap", "indexhealth"}, false},
		{"会话/锁", []string{"sessions", "activesessions", "waits", "locks", "blocktree", "latches", "mutexes"}, false},
		{"SQL 分析", []string{"slowsql", "topsql", "explain", "sql", "awr", "ash", "planhistory"}, false},
		{"内存/存储", []string{"sga", "pga", "space", "tempsess", "undosess", "sortusage", "redo", "fra", "asm", "segments", "os"}, false},
		{"管理", []string{"kill", "alter", "resize", "gather", "jobs", "resource", "alert", "backup", "standby", "params", "scheduler"}, false},
		{"AI 诊断", []string{"llm", "rule", "sentinel", "ora"}, true},
		{"系统", []string{"login", "logout", "conn", "users", "help", "clear", "history", "config", "model", "policy", "skills"}, false},
		{"Schema", []string{"tableinfo", "indexadvise"}, false},
	}

	// Build a map of skill names for quick lookup and SecurityLevel check.
	skillSet := make(map[string]bool, len(skills))
	skillMap := make(map[string]skill.Skill, len(skills))
	for _, sk := range skills {
		skillSet[sk.Name()] = true
		skillMap[sk.Name()] = sk
	}

	var sections []format.PanelSection
	shown := make(map[string]bool)
	for _, cat := range categories {
		var lines []string
		for _, name := range cat.names {
			if !skillSet[name] {
				continue
			}
			desc := helpDescCN[name]
			if desc == "" {
				// Fallback to English description.
				for _, sk := range skills {
					if sk.Name() == name {
						desc = sk.Description()
						break
					}
				}
			}
			if cat.highlight {
				lines = append(lines, fmt.Sprintf(" %s/%-16s %s%s", ansiBoldGold, name, desc, ansiReset))
			} else if sk, ok := skillMap[name]; ok && sk.SecurityLevel() > skill.LevelReadOnly {
				// Modification commands: red highlight to signal caution.
				lines = append(lines, fmt.Sprintf(" %s/%-16s %s%s", ansiBoldRed, name, desc, ansiReset))
			} else {
				lines = append(lines, fmt.Sprintf(" /%-16s %s", name, desc))
			}
			shown[name] = true
		}
		if len(lines) > 0 {
			sectionHeader := cat.label
			if cat.highlight {
				sectionHeader = fmt.Sprintf("%s%s%s", ansiBoldGold, cat.label, ansiReset)
			}
			sections = append(sections, format.PanelSection{
				Header: sectionHeader,
				Lines:  lines,
			})
		}
	}

	// Show uncategorized skills.
	var uncatLines []string
	for _, sk := range skills {
		if !shown[sk.Name()] {
			desc := helpDescCN[sk.Name()]
			if desc == "" {
				desc = sk.Description()
			}
			uncatLines = append(uncatLines, fmt.Sprintf(" /%-16s %s", sk.Name(), desc))
		}
	}
	if len(uncatLines) > 0 {
		sections = append(sections, format.PanelSection{
			Header: "其他",
			Lines:  uncatLines,
		})
	}

	// Footer hint.
	footerLines := []string{
		fmt.Sprintf(" %s提示: 直接输入中文问题可触发 AI 诊断 (需配置 LLM)%s", ansiBoldGold, ansiReset),
		" /help <command> 查看详细用法",
	}
	sections = append(sections, format.PanelSection{
		Header: "提示",
		Lines:  footerLines,
	})

	rendered := format.Panel("OpenDB Commands", sections)

	// Build plain text Data for LLM consumption.
	var b strings.Builder
	for _, sk := range skills {
		desc := helpDescCN[sk.Name()]
		if desc == "" {
			desc = sk.Description()
		}
		fmt.Fprintf(&b, "/%s  %s\n", sk.Name(), desc)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     b.String(),
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d 个命令", len(skills)),
	}, nil
}

// helpDescCN maps skill names to Chinese descriptions for the help listing.
var helpDescCN = map[string]string{
	// 监控
	"health":         "数据库健康检查",
	"sessions":       "所有会话概览",
	"activesessions": "活跃会话列表",
	"waits":          "等待事件统计",
	"locks":          "行锁/表锁",
	"blocktree":      "层级阻塞链 (阻塞者→被阻塞会话树)",
	"latches":        "Latch 争用",
	"mutexes":        "Mutex 争用",
	"dbtop":          "实时性能监控面板",
	// SQL
	"slowsql": "慢 SQL 查询 (默认 1000ms)",
	"topsql":  "Top SQL (按执行时间/逻辑读等排序)",
	"explain": "SQL 执行计划",
	"sql":     "执行自定义 SQL",
	// 内存/存储
	"sga":       "SGA 内存详情",
	"pga":       "PGA 内存详情",
	"space":     "表空间使用率",
	"tempsess":  "临时空间占用会话",
	"undosess":  "Undo 占用会话",
	"sortusage": "排序段使用详情",
	"redo":      "Redo 日志状态",
	"fra":       "FRA 使用详情",
	"asm":       "ASM 磁盘组",
	// 管理
	"kill":     "终止会话",
	"alter":    "修改系统参数 (无值时安全检测)",
	"resize":   "表空间扩容",
	"jobs":     "调度作业状态",
	"resource": "资源限制使用",
	"alert":    "近期 Alert Log",
	"backup":   "备份历史",
	"standby":  "Data Guard 状态",
	"params":   "搜索 Oracle 参数",
	// AI
	"llm":      "数据库诊断 (规则 + AI)",
	"rule":     "规则引擎诊断 (265条规则, 无需LLM)",
	"sentinel": "哨兵监控控制",
	"ora":      "ORA 错误诊断知识库 (60+条)",
	// 系统
	"login":   "连接数据库",
	"logout":  "断开连接",
	"conn":    "显示当前连接信息",
	"help":    "显示帮助",
	"clear":   "清屏",
	"history": "查看命令历史",
	"config":  "查看/修改配置",
	// Schema
	"tableinfo":   "表结构信息",
	"indexadvise": "索引建议",
	// 新增
	"awr":         "AWR 快照分析",
	"ash":         "ASH 活跃会话采样分析",
	"planhistory": "SQL 执行计划历史 (检测计划回退)",
	"segments":    "段空间分析 (Top 表/索引)",
	"os":          "操作系统指标 (CPU/内存/IO)",
	// 性能快照
	"perfsnap": "性能快照对比 (智能 AWR)",
	// 巡检调度
	"scheduler": "定时巡检引擎 (start/stop/status/run/history)",
	// Phase 3/4 新增
	"gather":      "统计信息收集 (check/run)",
	"indexhealth": "索引健康检查 (UNUSABLE/碎片/未使用)",
	"users":       "用户账户与密码过期检查",
	"model":       "LLM 模型管理 (切换/添加)",
	"policy":      "诊断规范管理 (导入/查看/删除)",
	"skills":      "可插拔 skill 管理 (list/show/doctor/reload/init)",
	"license":     "许可证和试用状态",
}

// detailedHelp 提供各命令的详细帮助文档。
var detailedHelp = map[string]string{
	"dbtop": `/dbtop [刷新间隔秒数]  —— 实时数据库性能监控面板

用法:
  /dbtop        默认每秒刷新
  /dbtop 3      每 3 秒刷新
  q / ESC       退出监控

━━━ 指标说明 ━━━

头部区域:
  SGA          System Global Area 大小 (MB)，来自 v$sga
  PGA          Program Global Area 已分配大小 (MB)，来自 v$pgastat

  db%          数据库 CPU 使用率
               分子: 两次采样间 DB CPU 增量 (微秒)
               分母: 两次采样间的墙钟时间 (微秒)
               公式: db% = cpuDelta / wallMicro × 100
               含义: 越高说明 CPU 越繁忙

  WTR%         Wait Time Ratio，等待时间占比
               分子: DB time 增量 - DB CPU 增量 = 非 CPU 等待时间
               分母: DB time 增量 (数据库总活动时间)
               公式: WTR% = (dbTime - dbCPU) / dbTime × 100
               含义: 越高说明瓶颈在等待 (I/O、锁等) 而非 CPU

  SN           Session Number，总会话数 (v$session)
  AN           Active Number，活跃用户会话数 (status=ACTIVE, type=USER)
  ACPU         Active CPU，活跃且在使用 CPU 的会话数
  AIO          Active I/O，活跃且在等待 I/O 的会话数
  IDL          Idle，空闲或非用户会话数

  TPS          Transactions Per Second，每秒事务数
               公式: (user commits + user rollbacks) / 采样间隔
               来自 v$sysstat

  QPS          Queries Per Second，每秒执行数
               公式: execute count 增量 / 采样间隔
               来自 v$sysstat

  REDO         Redo 日志生成速率 (KB/s)
               公式: redo size 增量 / 1024 / 采样间隔
               来自 v$sysstat

等待事件区域 (Top Wait Events):
  EVENT        等待事件名称，含 DB CPU (来自 v$sys_time_model)
               及非空闲等待事件 (来自 v$system_event)
  WAITS        累计等待次数
  TIME(s)      累计等待时间 (秒)
  PCT          该事件占 Top 事件总等待时间的百分比
               公式: 该事件时间 / 所有 Top 事件时间之和 × 100

活跃会话区域 (Active Sessions):
  SID          Session ID
  USR          用户名
  SQLID        当前执行的 SQL ID
  EVENT        当前等待事件，ON CPU 表示正在使用 CPU
  E/T          Elapsed Time，当前 SQL 已执行时间 (秒)
               公式: (SYSDATE - SQL_EXEC_START) × 86400
  SQL          SQL 文本前 80 个字符

健康状态:
  ● HEALTHY    所有指标正常
  ● WARNING    部分指标超过警告阈值
  ● CRITICAL   关键指标超过临界阈值，建议执行 /health 查看详细诊断`,
}

// renderCommandHelp renders Layer 2: /help <command>
func renderCommandHelp(sk skill.Skill) string {
	cliDef := sk.CLIDef()
	var buf strings.Builder

	desc := helpDescCN[sk.Name()]
	if desc == "" {
		desc = sk.Description()
	}
	buf.WriteString(fmt.Sprintf("  /%s — %s\n\n", sk.Name(), desc))

	if cliDef.Description != "" {
		for _, line := range strings.Split(cliDef.Description, "\n") {
			buf.WriteString(fmt.Sprintf("  %s\n", line))
		}
		buf.WriteString("\n")
	}

	if len(cliDef.SubCommands) > 0 {
		buf.WriteString("  子命令:\n")
		for _, sub := range cliDef.SubCommands {
			buf.WriteString(fmt.Sprintf("    %-24s %s\n", sub.Usage, sub.Description))
		}
		buf.WriteString("\n")
	}

	if len(cliDef.Examples) > 0 {
		buf.WriteString("  示例:\n")
		for _, ex := range cliDef.Examples {
			buf.WriteString(fmt.Sprintf("    %s\n", ex))
		}
		buf.WriteString("\n")
	}

	if len(cliDef.SubCommands) > 0 {
		buf.WriteString(fmt.Sprintf("  输入 /help %s <子命令> 查看子命令详情\n", sk.Name()))
	}

	return buf.String()
}

// renderSubCommandHelp renders Layer 3: /help <command> <subcommand>
func renderSubCommandHelp(cmdName string, sub skill.SubCommandDef) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("  /%s %s\n\n", cmdName, sub.Name))
	buf.WriteString(fmt.Sprintf("  用法: /%s %s\n\n", cmdName, sub.Usage))

	if sub.Description != "" {
		for _, line := range strings.Split(sub.Description, "\n") {
			buf.WriteString(fmt.Sprintf("  %s\n", line))
		}
		buf.WriteString("\n")
	}

	if len(sub.Examples) > 0 {
		buf.WriteString("  示例:\n")
		for _, ex := range sub.Examples {
			buf.WriteString(fmt.Sprintf("    %s\n", ex))
		}
	}

	return buf.String()
}
