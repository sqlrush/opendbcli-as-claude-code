/*-------------------------------------------------------------------------
 *
 * skill_runner.go
 *	  SkillResultEvent carries the result of an async skill execution
 *	  back to the REPL main loop.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/skill_runner.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/skill"
)

// SkillResultEvent carries the result of an async skill execution back to the REPL main loop.
type SkillResultEvent struct {
	Result   *skill.Result
	Err      error
	Input    string // original user input (for post-processing)
	Vertical bool   // whether \G was used
}

// skillDescription returns a brief Chinese description for the running command.
func skillDescription(input string) string {
	cmd := strings.Fields(input)
	if len(cmd) == 0 {
		return "执行中..."
	}
	switch strings.ToLower(cmd[0]) {
	case "/sessions":
		return "正在查询会话信息..."
	case "/activesessions":
		return "正在查询活跃会话..."
	case "/waits":
		return "正在查询等待事件..."
	case "/locks":
		return "正在查询锁信息..."
	case "/latches":
		return "正在查询 Latch 争用..."
	case "/mutexes":
		return "正在查询 Mutex 争用..."
	case "/health":
		return "正在执行健康检查..."
	case "/tempsess":
		return "正在查询临时空间占用..."
	case "/undosess":
		return "正在查询 Undo 事务..."
	case "/pga":
		return "正在查询 PGA 内存..."
	case "/sga":
		return "正在查询 SGA 内存..."
	case "/redo":
		return "正在查询 Redo 日志..."
	case "/fra":
		return "正在查询 FRA 使用..."
	case "/asm":
		return "正在查询 ASM 磁盘组..."
	case "/sortusage":
		return "正在查询排序段..."
	case "/resource":
		return "正在查询资源限制..."
	case "/blocktree":
		return "正在分析阻塞链..."
	case "/segments":
		return "正在查询段空间..."
	case "/os":
		return "正在查询操作系统指标..."
	case "/slowsql":
		return "正在查询慢 SQL..."
	case "/topsql":
		return "正在查询 Top SQL..."
	case "/awr":
		return "正在查询 AWR 快照..."
	case "/ash":
		return "正在查询 ASH 采样..."
	case "/planhistory":
		return "正在查询执行计划历史..."
	case "/space":
		return "正在查询表空间..."
	case "/params":
		return "正在查询参数..."
	case "/alert":
		return "正在查询告警日志..."
	case "/backup":
		return "正在查询备份历史..."
	case "/standby":
		return "正在查询 Data Guard 状态..."
	case "/resize":
		return "正在查询表空间文件..."
	case "/jobs":
		return "正在查询调度作业..."
	case "/tableinfo":
		return "正在查询表结构..."
	case "/indexadvise":
		return "正在分析索引建议..."
	case "/explain":
		return "正在获取执行计划..."
	case "/kill":
		return "正在终止会话..."
	case "/alter":
		return "正在修改参数..."
	case "/scheduler", "/sched", "/cron":
		return "正在查询调度器状态..."
	case "/perfsnap", "/psnap":
		return "正在采集性能快照..."
	case "/gather":
		return "正在检查统计信息..."
	case "/indexhealth", "/idxhealth":
		return "正在检查索引健康..."
	case "/users":
		return "正在查询用户账户..."
	default:
		if strings.HasPrefix(strings.ToUpper(input), "SELECT") ||
			strings.HasPrefix(strings.ToUpper(input), "WITH") {
			return "正在执行 SQL 查询..."
		}
		return "执行中..."
	}
}

// syncCommands are commands that must execute synchronously because they
// either take over the terminal (picker, wizard, dbtop) or are instant local operations.
var syncCommands = map[string]bool{
	"/clear":   true,
	"/login":   true,
	"/conn":    true,
	"/dbtop":   true,
	"/help":    true,
	"/history": true,
	"/config":  true,
	"/logout":  true,
	"/model":   true,
	"/m":       true,
}

// isSyncCommand returns true if the command must execute synchronously.
func isSyncCommand(input string) bool {
	cmd := strings.Fields(input)
	if len(cmd) == 0 {
		return true
	}
	return syncCommands[strings.ToLower(cmd[0])]
}

// isBarePickerCommand returns true if the command is a bare picker command
// (no arguments) that needs interactive selection when dequeued.
func isBarePickerCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	return trimmed == "/login" || trimmed == "/model" || trimmed == "/m" ||
		trimmed == "/llm" || trimmed == "/rule"
}

// startSkillAsync launches a goroutine to execute a skill command,
// sending the result back via skillCh. The input cursor returns immediately.
func (r *REPL) startSkillAsync(input string) {
	// Parse \G vertical modifier before launching goroutine.
	vertical := false
	dispatchInput := input
	if strings.HasSuffix(strings.TrimSpace(input), `\G`) {
		vertical = true
		dispatchInput = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(input), `\G`))
	}

	ch := make(chan SkillResultEvent, 1)
	r.skillCh = ch
	r.skillRunning = true

	// Write star animation placeholder.
	r.writeOutputLine("")
	starRow := r.contentRow - 1
	if r.scrollMode {
		starRow = r.maxContentRow()
	}
	r.skillStarRow = starRow
	desc := skillDescription(dispatchInput)
	r.skillStar = newDotAnimator(starRow, desc, func(row int, content string) {
		fmt.Fprintf(r.writer, "\033[%d;1H\033[2K%s", row, content)
	})

	odberr.SafeGo(odberr.ErrSkillExec, func() {
		// 10 min timeout — long enough for /sqltune (typical 1-3 min, edge
		// cases 5+ min) and other LLM-driven skills. 2 min was too short
		// and caused mysleading "请检查数据库连接状态" errors.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err := r.dispatcher.Dispatch(ctx, dispatchInput)
		if ctx.Err() == context.DeadlineExceeded && err == nil {
			err = fmt.Errorf("执行超时（10 分钟）— 可能 LLM 响应慢 / SQL 复杂度高 / 工具调用挂起。可重试或检查 LLM 配置 (/model)")
		}
		ch <- SkillResultEvent{
			Result:   result,
			Err:      err,
			Input:    input,
			Vertical: vertical,
		}
	})

	r.drawInputArea()
}

// renderSkillResult handles a SkillResultEvent from the async goroutine.
// All rendering happens on the REPL main goroutine.
func (r *REPL) renderSkillResult(ev SkillResultEvent) {
	// Stop the star animation.
	if r.skillStar != nil {
		if ev.Err != nil {
			r.skillStar.stopWithError("失败")
		} else {
			r.skillStar.stop()
		}
		r.skillStar = nil
		r.skillStarRow = 0
	}

	// Clear skill running state.
	r.skillRunning = false
	r.skillCh = nil

	// Render error.
	if ev.Err != nil {
		r.writeOutputLine(errorStyle.Render("  ✗ " + ev.Err.Error()))
		r.writeOutputLine("")
		r.drawInputArea()
		r.flushPendingEvents()
		r.processCmdQueue()
		return
	}

	// Render result.
	if ev.Result != nil {
		r.renderResult(ev.Result, ev.Input, ev.Vertical)
		r.updateConnInfoFromResult(ev.Result, ev.Input)
	}

	r.writeOutputLine("")
	r.drawInputArea()
	r.flushPendingEvents()
	r.processCmdQueue()
}
