/*-------------------------------------------------------------------------
 *
 * existingconfig.go
 *	  ExistingConfigStep is the first wizard step. It detects whether
 *	  config.yaml already exists in cfg.BaseDir and:
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/existingconfig.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

// ExistingConfigStep is the first wizard step. It detects whether config.yaml
// already exists in cfg.BaseDir and:
//
//   - If absent → auto-completes with no UI (fresh install path).
//   - If present → asks the user whether to overwrite. On confirm, the file
//     is renamed to config.yaml.bak.YYYYMMDD-HHMMSS BEFORE the wizard
//     proceeds (so the rest of the flow can still os.WriteFile() blindly).
//     On decline, the wizard exits cleanly with a hint to use `<binary>
//     configure` for incremental edits.
//
// Added 2026-04-27 after audit: prior behavior was to silently overwrite
// config.yaml on `dbaa setup`, blowing away years of accumulated connections
// and inline models. Auto-backup on confirm makes the operation reversible.
type ExistingConfigStep struct {
	cfg        *SetupConfig
	configPath string
	exists     bool
	confirm    *ConfirmModel
	backupPath string
	declined   bool
	done       bool
}

// NewExistingConfigStep checks whether config.yaml already exists at the
// resolved path and returns a step that either auto-completes (no file) or
// prompts for overwrite confirmation (file present).
func NewExistingConfigStep(cfg *SetupConfig) *ExistingConfigStep {
	configPath := filepath.Join(cfg.BaseDir, "config.yaml")
	_, err := os.Stat(configPath)
	exists := err == nil

	s := &ExistingConfigStep{
		cfg:        cfg,
		configPath: configPath,
		exists:     exists,
	}
	if exists {
		s.confirm = NewConfirmModel(
			"已存在配置文件 " + configPath + " — 是否覆盖? (旧文件会自动备份)",
		)
	}
	return s
}

func (s *ExistingConfigStep) Title() string { return "Existing config check" }
func (s *ExistingConfigStep) Done() bool    { return s.done }

func (s *ExistingConfigStep) Summary() string {
	if !s.exists {
		return CompletedLine("Existing config", "None — fresh install")
	}
	if s.declined {
		return CompletedLine("Existing config", "Kept (setup cancelled)")
	}
	return CompletedLine("Existing config", "Backed up to "+filepath.Base(s.backupPath))
}

func (s *ExistingConfigStep) Init() tea.Cmd {
	if !s.exists {
		// No file → skip this step instantly.
		s.done = true
		return emitDone
	}
	return nil
}

func (s *ExistingConfigStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !s.exists {
		// Already auto-completed via Init → just absorb subsequent msgs
		// (key buffering, WindowSizeMsg, etc.) WITHOUT re-emitting stepDoneMsg.
		// Re-emitting was the cause of the "Mode and DBType silently skipped"
		// bug: every extra msg routed here while m.current was still 1 produced
		// another stepDoneMsg, advancing m.current past Mode/DBType without
		// running their Update.
		if s.done {
			return s, nil
		}
		s.done = true
		return s, emitDone
	}

	updated, cmd := s.confirm.Update(msg)
	s.confirm = updated
	if !s.confirm.Done() {
		return s, cmd
	}

	if !s.confirm.Confirmed() {
		// User declined → exit setup cleanly. The wizard will tea.Quit on
		// next outer-loop tick because we set declined and signal done.
		s.declined = true
		s.done = true
		return s, tea.Quit
	}

	// Confirmed → back up the existing file before wizard proceeds.
	timestamp := time.Now().Format("20060102-150405")
	s.backupPath = s.configPath + ".bak." + timestamp
	if err := os.Rename(s.configPath, s.backupPath); err != nil {
		// Backup failed → bail out instead of risking blind overwrite.
		fmt.Printf("\n  ✗ 备份失败: %v\n  请手动备份 %s 后重试。\n", err, s.configPath)
		s.declined = true
		s.done = true
		return s, tea.Quit
	}

	s.done = true
	return s, emitDone
}

func (s *ExistingConfigStep) View() string {
	if !s.exists {
		// No-op step — never rendered in practice (Init returns emitDone).
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(InfoPanel("Existing configuration detected", strings.Join([]string{
		"在 " + s.configPath + " 找到了已有的 " + brand.Current().AppName + " 配置。",
		"",
		"继续: 自动备份为 config.yaml.bak.YYYYMMDD-HHMMSS, 然后用向导的新内容覆盖。",
		"取消: 退出向导, 不动任何文件。需要在已有配置上增量编辑请运行:",
		"      " + brand.Current().BinaryName + " configure",
	}, "\n")))
	b.WriteString("\n")
	b.WriteString(s.confirm.View())
	return b.String()
}
