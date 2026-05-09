/*-------------------------------------------------------------------------
 *
 * main.go
 *	  Entry point for the main binary.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/e2e_sentinel/main.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	oracledriver "github.com/sqlrush/opendb/internal/oracle/driver"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/oracle/agent"
	"github.com/sqlrush/opendb/internal/llm/ollama"
	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

func lg(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// loggingProvider wraps llm.Provider and logs all requests/responses.
type loggingProvider struct {
	inner llm.Provider
}

func (p *loggingProvider) Name() string { return p.inner.Name() }
func (p *loggingProvider) ChatStream(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	return p.inner.ChatStream(ctx, req)
}

func (p *loggingProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.Response, error) {
	lg("  ─── LLM 请求 ───")
	lg("  Messages: %d", len(req.Messages))
	for i, m := range req.Messages {
		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		lg("    [%d] role=%s content(%d chars)=%s", i, m.Role, len(m.Content), content)
	}
	if len(req.Tools) > 0 {
		lg("  Tools: %d", len(req.Tools))
	}
	lg("  ─── 发送请求到 %s ───", p.inner.Name())

	start := time.Now()
	resp, err := p.inner.Chat(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		lg("  ✗ LLM 错误 (%.1fs): %v", elapsed.Seconds(), err)
		return nil, err
	}

	lg("  ─── LLM 响应 (%.1fs) ───", elapsed.Seconds())
	lg("  Tokens: input=%d, output=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	lg("  StopReason: %s", resp.StopReason)
	if len(resp.ToolCalls) > 0 {
		lg("  ToolCalls: %d", len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			lg("    %s(%s)", tc.Name, tc.Arguments)
		}
	}
	lg("  Content (%d chars):", len(resp.Content))
	for _, line := range strings.Split(resp.Content, "\n") {
		lg("    │ %s", line)
	}
	lg("  ─── LLM 响应结束 ───")
	return resp, nil
}

func main() {
	lg("╔═══════════════════════════════════════════════════════╗")
	lg("║  OpenDB E2E: Sentinel + Diagnose 全链路验证          ║")
	lg("╚═══════════════════════════════════════════════════════╝")

	// ── Step 1: Connect ──
	lg("")
	lg("▶ Step 1: 连接 Oracle")
	driver, err := oracledriver.NewDriver(db.ConnectionConfig{
		Host:     "8.160.176.23",
		Port:     1521,
		Service:  "orcl",
		User:     "opendb_test",
		Password: "OpenDB_Test_2026",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接失败: %v\n", err)
		os.Exit(1)
	}
	defer driver.Close()

	ctx := context.Background()
	if err := driver.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Ping 失败: %v\n", err)
		os.Exit(1)
	}
	info := driver.ServerInfo()
	lg("  ✓ 已连接 — %s:%d/%s", "8.160.176.23", 1521, "orcl")
	lg("  版本: %s, 实例: %s", info.Version, info.InstanceName)

	// ── Step 2: Baseline ──
	lg("")
	lg("▶ Step 2: 采集基线 (6样本, 1s间隔)")
	collector := dbtop.NewCollector(driver)
	var samples []sentinel.SentinelSample
	for i := 0; i < 6; i++ {
		snap := collector.Collect(ctx)
		samples = append(samples, sentinel.SentinelSample{
			Timestamp:     snap.Timestamp,
			ActiveNonIdle: snap.ActiveCount,
		})
		lg("  [%d] active=%d db%%=%.1f TPS=%.0f QPS=%.0f REDO=%.0fKB/s",
			i+1, snap.ActiveCount, snap.DBPercent, snap.TPS, snap.QPS, snap.RedoKBs)
		if i < 5 {
			time.Sleep(time.Second)
		}
	}

	// ── Step 3: Current snapshot ──
	lg("")
	lg("▶ Step 3: 当前快照")
	snap := collector.Collect(ctx)
	lg("  Sessions: total=%d active=%d cpu=%d io=%d idle=%d",
		snap.TotalSessions, snap.ActiveCount, snap.ActiveCPU, snap.ActiveIO, snap.IdleCount)
	lg("  Throughput: TPS=%.0f QPS=%.0f REDO=%.1fKB/s", snap.TPS, snap.QPS, snap.RedoKBs)
	lg("  Ratios: db%%=%.1f WTR%%=%.1f", snap.DBPercent, snap.WTRPercent)

	if len(snap.Sessions) > 0 {
		lg("  活跃会话:")
		for i, s := range snap.Sessions {
			if i >= 8 {
				lg("  ... 还有 %d 个", len(snap.Sessions)-8)
				break
			}
			sql := s.SQLText
			if len(sql) > 50 {
				sql = sql[:50] + "..."
			}
			lg("    SID=%-5d %-10s %-13s %-25s %s", s.SID, s.Username, s.SQLID, s.Event, sql)
		}
	}

	if len(snap.Events) > 0 {
		lg("  等待事件:")
		for i, ev := range snap.Events {
			if i >= 8 {
				break
			}
			lg("    %-35s waits=%-8d time=%8.1fs pct=%5.1f%%", ev.Event, ev.Waits, ev.TimeSec, ev.PCT)
		}
	}

	// ── Step 4: Burst ──
	lg("")
	lg("▶ Step 4: Burst 采集 (8帧, 200ms)")
	collector.SetMode(dbtop.ModeBurst)
	var frames []sentinel.BurstFrame
	var peakActive int
	for i := 0; i < 8; i++ {
		bSnap := collector.Collect(ctx)
		frames = append(frames, sentinel.BurstFrame{
			Seq: i, Snapshot: bSnap, Timestamp: bSnap.Timestamp,
		})
		if bSnap.ActiveCount > peakActive {
			peakActive = bSnap.ActiveCount
		}
		lg("  帧%d: active=%d sessions=%d", i+1, bSnap.ActiveCount, len(bSnap.Sessions))
		if i < 7 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	collector.SetMode(dbtop.ModeNormal)

	// ── Step 5: Analyze ──
	lg("")
	lg("▶ Step 5: 分析")
	avgActive := float64(0)
	for _, s := range samples {
		avgActive += float64(s.ActiveNonIdle)
	}
	avgActive /= float64(len(samples))

	burstResult := sentinel.BurstResult{
		Trigger: sentinel.TriggerEvent{
			Metric: "active_non_idle", Baseline: avgActive,
			Current: float64(snap.ActiveCount), Threshold: avgActive * 2,
		},
		Frames: frames, PeakActive: peakActive,
		StartTime: frames[0].Timestamp, EndTime: frames[len(frames)-1].Timestamp,
	}
	baseline := sentinel.Baseline{
		Ready: true, AvgActive: avgActive, StdActive: avgActive * 0.3, Samples: samples,
	}

	report := sentinel.Analyze(burstResult, baseline)

	lg("  分类: %s (置信度 %.0f%%)", report.Classification.Cause, report.Classification.Confidence*100)
	lg("  峰值: %d, 基线: %.1f, 持续: %.1fs", report.PeakActive, report.BaselineActive, report.DurationSec)
	for _, ev := range report.Classification.Evidence {
		lg("  证据: %s", ev)
	}
	for i, sql := range report.TopSQLs {
		if i >= 5 {
			break
		}
		lg("  SQL[%d]: %s 出现=%.0f%% 并发=%d wait=%s", i, sql.SQLID, sql.OccurrenceRate*100, sql.MaxConcurrent, sql.WaitClass)
		if sql.SQLText != "" {
			text := sql.SQLText
			if len(text) > 80 {
				text = text[:80] + "..."
			}
			lg("          %s", text)
		}
	}
	for _, wp := range report.WaitProfile {
		lg("  Wait: %-20s %.1f%%", wp.WaitClass, wp.Percentage)
	}

	// ── Step 6: Remediation ──
	lg("")
	lg("▶ Step 6: 修复建议模板")
	rem := sentinel.GetRemediation(report.Classification.Cause)
	for _, s := range rem.Suggestions {
		lg("  建议: %s", s)
	}
	for _, sql := range rem.InvestigateSQL {
		if len(sql) > 100 {
			sql = sql[:100] + "..."
		}
		lg("  排查SQL: %s", sql)
	}

	// ── Step 7: Compress ──
	lg("")
	lg("▶ Step 7: 压缩报告")
	compressed := agent.CompressReport(report)
	lg("  长度: %d 字符 (约 %d tokens)", len(compressed), len(compressed)/2)
	lg("  ┌─── 压缩报告 ───")
	for _, line := range strings.Split(compressed, "\n") {
		lg("  │ %s", line)
	}
	lg("  └─── 结束 ───")

	// ── Step 8: Build full prompt (show what LLM sees) ──
	lg("")
	lg("▶ Step 8: 构建 LLM 提示")
	systemPrompt := agent.SystemPromptForDiagnose(agent.ModePlaybook)
	lg("  System Prompt (%d chars):", len(systemPrompt))
	for _, line := range strings.Split(systemPrompt, "\n") {
		lg("  │ %s", line)
	}
	userPrompt := agent.DiagnoseUserPrompt("数据库活跃会话突增，请诊断根因并给出修复建议", compressed)
	lg("  User Prompt (%d chars):", len(userPrompt))
	for _, line := range strings.Split(userPrompt, "\n") {
		lg("  │ %s", line)
	}

	// ── Step 9: Call LLM ──
	lg("")
	lg("▶ Step 9: 调用 Ollama (qwen3.5:9b)")
	rawProvider := ollama.NewOllamaProvider("http://localhost:11434", "qwen3.5:9b")

	// Quick connectivity test
	lg("  测试连通性...")
	testReq := llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}
	testCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	testResp, testErr := rawProvider.Chat(testCtx, testReq)
	cancel()
	if testErr != nil {
		lg("  ✗ Ollama 连通性测试失败: %v", testErr)
		lg("  尝试直接 curl...")
		os.Exit(1)
	}
	lg("  ✓ Ollama 连通 — 测试响应: %q", truncate(testResp.Content, 50))

	// Full diagnosis call with logging
	lg("")
	lg("  开始正式诊断调用...")
	logProvider := &loggingProvider{inner: rawProvider}
	diagnoser := agent.NewDiagnoser(logProvider, nil, nil)

	startTime := time.Now()
	result, err := diagnoser.Diagnose(ctx, agent.ModePlaybook, &report, "数据库活跃会话突增，请诊断根因并给出修复建议")
	elapsed := time.Since(startTime)

	if err != nil {
		lg("  ✗ 诊断失败: %v", err)
		os.Exit(1)
	}

	lg("")
	lg("▶ Step 10: 诊断结果")
	lg("  耗时: %.1fs, 轮次: %d", elapsed.Seconds(), result.RoundsUsed)
	lg("  Tokens: input=%d, output=%d", result.TokensUsed.InputTokens, result.TokensUsed.OutputTokens)
	lg("═══════════════════════════════════════════════════════")

	output := agent.FormatDiagnoseResult(result)
	for _, line := range strings.Split(output, "\n") {
		fmt.Println(line)
	}

	lg("═══════════════════════════════════════════════════════")

	// Dump raw result as JSON for debugging
	lg("")
	lg("▶ 附: 原始结果 JSON")
	raw, _ := json.MarshalIndent(result, "  ", "  ")
	lg("  %s", string(raw))

	lg("")
	lg("✓ E2E 验证完成")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
