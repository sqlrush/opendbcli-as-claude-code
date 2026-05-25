/*-------------------------------------------------------------------------
 *
 * welcome.go
 *	  welcome — WelcomeStep plus helpers (NewWelcomeStep) used by the
 *	  setup package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/welcome.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/version"
)

// asciiLogo lives in brand layer now (brand.SetupLogo).
// Kept this comment for grep-discoverability when tracking logo changes.

type WelcomeStep struct {
	confirm *ConfirmModel
}

func NewWelcomeStep() *WelcomeStep {
	return &WelcomeStep{
		confirm: NewConfirmModel("Ready to set up " + brand.Current().AppName + "?"),
	}
}

func (w *WelcomeStep) Title() string { return "Welcome" }
func (w *WelcomeStep) Done() bool    { return w.confirm.Done() }

func (w *WelcomeStep) Summary() string {
	return CompletedLine(brand.Current().AppName+" "+version.Version, "Setup started")
}

func (w *WelcomeStep) Init() tea.Cmd { return nil }

func (w *WelcomeStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := w.confirm.Update(msg)
	w.confirm = updated
	if w.confirm.Done() && !w.confirm.Confirmed() {
		return w, tea.Quit
	}
	return w, cmd
}

func (w *WelcomeStep) View() string {
	var b strings.Builder

	logoStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	b.WriteString(logoStyle.Render(brand.Current().SetupLogo))
	b.WriteString("\n")

	center := lipgloss.NewStyle().Width(56).Align(lipgloss.Center)
	b.WriteString(center.Render(StyleSubtitle.Render(brand.Current().SetupTagline)))
	b.WriteString("\n")
	b.WriteString(center.Render(StyleHighlight.Render("最少交互，最优诊断 | Less input. More insight.")))
	b.WriteString("\n\n")

	// Product info line
	b.WriteString(BulletLine(version.Short() + "  |  Apache 2.0  |  " + brand.Current().Authorship))
	b.WriteString("\n\n")

	// Welcome panel — description from brand layer + fixed feature bullets
	contentLines := append([]string{}, brand.Current().SetupDescription...)
	contentLines = append(contentLines,
		"",
		StyleTitle.Render("核心功能:"),
		StyleSuccess.Render("·")+" 多数据库支持 — "+brand.Current().DBList,
		StyleSuccess.Render("·")+" 三种交互合一 — Skill 命令（类似 Claude Code 的 /命令）、",
		"  SQL 和数据库原生语法（整合 "+brand.Current().NativeCLITools+" 功能）、",
		"  自然语言（类似聊天工具）",
		StyleSuccess.Render("·")+" Sentinel 实时探针 — 自动检测异常，为 Model 和 Rule 提供诊断信息",
		StyleSuccess.Render("·")+" dbtop 秒级监控大盘 — 秒级捕获性能抖动",
		StyleSuccess.Render("·")+" Model 诊断 — 自然语言描述问题，Model 给出可执行方案",
		StyleSuccess.Render("·")+" Rule 诊断 — 规则引擎诊断，与 Model 能力互补",
		StyleSuccess.Render("·")+" 记忆系统 — 跨诊断积累经验，越用越懂你的数据库",
		StyleSuccess.Render("·")+" 内核工程师模式 — 自动抓系统堆栈、火焰图并分析源码优化方案",
		StyleSuccess.Render("·")+" 多模型支持 — GLM / Kimi / MiniMax / Qwen / DeepSeek 云端+本地部署",
		StyleSuccess.Render("·")+" 零依赖 — 拷贝即运行",
	)
	content := strings.Join(contentLines, "\n")
	b.WriteString(InfoPanel("Welcome", content))
	b.WriteString("\n")

	b.WriteString(w.confirm.View())
	return b.String()
}
