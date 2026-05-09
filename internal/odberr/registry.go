/*-------------------------------------------------------------------------
 *
 * registry.go
 *	  Entry describes a registered error code.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/registry.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Entry describes a registered error code.
//
// Entries are immutable after registration; returns from Lookup are copies.
type Entry struct {
	Code     string
	Module   string // short name: "ui", "diag", ...
	Severity Severity
	Title    string // one-line description
	Advice   string // user-facing suggestion
}

var (
	entriesMu sync.RWMutex
	entries   = map[string]Entry{}

	// countersMu guards creation of atomic counters; the counters
	// themselves are accessed with sync/atomic after allocation.
	countersMu sync.Mutex
	counters   = map[string]*int64{}
)

// Register adds an Entry to the registry.
// Duplicate codes overwrite (intentional — allows modules to refine advice).
func Register(e Entry) {
	entriesMu.Lock()
	entries[e.Code] = e
	entriesMu.Unlock()
}

// Lookup returns the Entry for code. If unregistered, the Unknown entry
// is returned with ok=false so callers can distinguish.
func Lookup(code string) (Entry, bool) {
	entriesMu.RLock()
	e, ok := entries[code]
	entriesMu.RUnlock()
	if ok {
		return e, true
	}
	entriesMu.RLock()
	fallback, hasFallback := entries[ErrUnknown]
	entriesMu.RUnlock()
	if hasFallback {
		// Return a copy with the caller's code echoed back,
		// so display is accurate even for unregistered codes.
		fallback.Code = code
		return fallback, false
	}
	return Entry{Code: code, Module: "unknown", Severity: SeverityError}, false
}

// Increment bumps the usage counter for code.
func Increment(code string) {
	ptr := counterFor(code)
	atomic.AddInt64(ptr, 1)
}

// Count returns how many times Increment has been called for code.
func Count(code string) int64 {
	ptr := counterFor(code)
	return atomic.LoadInt64(ptr)
}

func counterFor(code string) *int64 {
	countersMu.Lock()
	defer countersMu.Unlock()
	if c, ok := counters[code]; ok {
		return c
	}
	var c int64
	counters[code] = &c
	return &c
}

// AllEntries returns a sorted snapshot of registered entries.
// Useful for /error (no arg) listing and tests.
func AllEntries() []Entry {
	entriesMu.RLock()
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	entriesMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// init registers the first batch of known error codes. Additional codes
// can be added by modules via their own init() calling Register().
func init() {
	for _, e := range defaultEntries() {
		Register(e)
	}
}

// defaultEntries returns the built-in registry contents.
// Split out so tests can inspect the baseline without init side effects.
func defaultEntries() []Entry {
	return []Entry{
		// --- 01 core ---
		{ErrCoreMainPanic, "core", SeverityFatal,
			"主进程 panic",
			"崩溃已记录到 ~/.opendb/crash.log，请附带该文件反馈。"},
		{ErrCoreConfigLoad, "core", SeverityFatal,
			"配置加载失败",
			"检查 ~/.opendb/config.yaml 格式与权限，或运行 opendb setup 重建。"},
		{ErrCoreSetupWizard, "core", SeverityError,
			"安装向导异常",
			"重新执行 opendb setup；如持续失败请查看 crash.log。"},

		// --- 02 conn ---
		{ErrConnOpen, "conn", SeverityError,
			"数据库连接打开失败",
			"检查网络、监听端口与账号密码；运行 /login 重新登录。"},
		{ErrConnLost, "conn", SeverityWarn,
			"数据库连接中断",
			"opendb 将在下次命令时自动重连；频繁出现请检查网络稳定性。"},
		{ErrConnAuth, "conn", SeverityError,
			"认证失败",
			"密码或权限不足；/login 重新输入或更换账号。"},

		// --- 03 ui ---
		{ErrUIDiagRender, "ui", SeverityError,
			"诊断流渲染异常",
			"已自动恢复；若频繁出现请 opendb upgrade 到最新版本。"},
		{ErrUISkillRender, "ui", SeverityError,
			"Skill 结果渲染异常",
			"已自动恢复；附 crash.log 反馈可定位模板或宽度计算问题。"},
		{ErrUIResize, "ui", SeverityWarn,
			"终端尺寸调整处理失败",
			"尝试 /clear 或重启 opendb 以重建渲染状态。"},

		// --- 04 diag ---
		{ErrDiagLLMTimeout, "diag", SeverityError,
			"LLM 调用超时",
			"检查网络或更换更快的模型（/model）；长响应可提高 timeout 配置。"},
		{ErrDiagToolCall, "diag", SeverityError,
			"工具调用失败",
			"诊断链路中某个 skill 执行异常；查看输出中的失败 skill 名并手动 /skill 验证。"},
		{ErrDiagStreamTruncated, "diag", SeverityWarn,
			"流式输出被截断",
			"已自动触发恢复续写；若最终仍不完整，使用更大的 max_tokens 或更短的 prompt。"},

		// --- 05 sentinel ---
		{ErrSentinelLoop, "sentinel", SeverityError,
			"哨兵循环 panic",
			"哨兵已自动恢复；若连续出现请禁用 /sentinel 并反馈 crash.log。"},
		{ErrSentinelCollect, "sentinel", SeverityWarn,
			"哨兵采集单次失败",
			"下个周期会重试；持续失败请检查数据库可用性与权限。"},

		// --- 06 rule ---
		{ErrRuleEval, "rule", SeverityError,
			"规则评估异常",
			"单条规则异常不影响整体；查看 crash.log 中的规则 ID 可定位。"},
		{ErrRuleLoad, "rule", SeverityError,
			"规则加载失败",
			"JSON 规则文件解析错误；用 /rule list 定位哪条规则有问题。"},

		// --- 07 skill ---
		{ErrSkillExec, "skill", SeverityError,
			"Skill 执行异常",
			"单条 skill panic 已被捕获；/error 查看详情，再复现一次看是否稳定出现。"},
		{ErrSkillNotFound, "skill", SeverityWarn,
			"未知 skill",
			"输入 /help 查看所有可用命令；或使用模糊匹配输入命令前缀。"},
		{ErrSkillInvalidParams, "skill", SeverityWarn,
			"参数校验失败",
			"查看 /help <command> 获取参数说明。"},

		// --- 08 llm ---
		{ErrLLMRequest, "llm", SeverityError,
			"LLM 请求失败",
			"检查 provider 地址、API Key、模型名；/llm test 验证连通性。"},
		{ErrLLMParseResponse, "llm", SeverityError,
			"LLM 响应解析异常",
			"模型可能返回非预期格式；更换模型或升级 opendb 以支持新格式。"},
		{ErrLLMModelNotFound, "llm", SeverityError,
			"active_model 在配置中找不到",
			"用 /model 查看可用模型清单；config.yaml 的 active_model 必须等于 entry 的 name 字段；如改用文件清单需配 models_dir 字段。"},
		{ErrLLMNoActiveModel, "llm", SeverityWarn,
			"未配置 active_model，已降级到规则诊断",
			"用 /model 选一个模型，或 setup 重建配置。"},
		{ErrLLMConfigInvalid, "llm", SeverityError,
			"模型配置项无效",
			"检查 provider / base_url / api_key / model 字段是否完整。"},

		// --- 09 storage ---
		{ErrStorageRead, "storage", SeverityError,
			"文件读取失败",
			"检查 ~/.opendb 目录权限；必要时备份后运行 opendb setup 重建。"},
		{ErrStorageWrite, "storage", SeverityError,
			"文件写入失败",
			"检查磁盘空间与目录权限；crash.log 会记录目标路径。"},

		// --- 10 scheduler ---
		{ErrSchedulerRun, "scheduler", SeverityError,
			"定时任务执行异常",
			"单次任务失败不影响后续；/scheduler status 查看历史。"},

		// --- 11 cluster ---
		{ErrClusterRPC, "cluster", SeverityError,
			"集群 RPC 调用失败",
			"检查各节点 opendb agent status；网络分区会自动重连。"},

		// --- 90 generic panic ---
		{ErrPanicREPL, "panic", SeverityError,
			"REPL 主循环 panic",
			"已自动恢复；crash.log 含完整调用栈，附带反馈定位具体源头。"},
		{ErrPanicGoroutine, "panic", SeverityError,
			"后台 goroutine panic",
			"已自动恢复；主流程不受影响，crash.log 含完整调用栈。"},

		// --- 99 unknown (required) ---
		{ErrUnknown, "unknown", SeverityError,
			"未注册的错误",
			"首次见到该类错误；请附 crash.log 反馈，后续迭代会分配正式编号。"},
	}
}
