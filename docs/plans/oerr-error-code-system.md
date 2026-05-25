# OpenDB 错误码体系设计方案

> 设计日期: 2026-04-17
> 状态: 已确认，待实施

## 目标

为 opendb 设计一套类似 Oracle ORA-XXXX 的错误码体系：
- 所有 panic 被捕获而非崩溃，记录错误码 + 上下文
- 常规错误也纳入编号管理
- 覆盖所有 goroutine
- 用户看到编号就能定位问题，开发者看到编号就能定位代码

## 错误码编号规则

**格式**: `ODB-XXYYYY`

- `ODB` — OpenDB 前缀
- `XX` — 模块代码（2 位）
- `YYYY` — 序号（4 位）

### 模块分配

| 代码 | 模块 | 说明 |
|------|------|------|
| 01 | core | 启动、配置、初始化 |
| 02 | conn | 连接管理 |
| 03 | ui | REPL、渲染、终端 |
| 04 | diag | 诊断引擎、LLM 交互 |
| 05 | sentinel | 哨兵监控 |
| 06 | rule | 规则引擎 |
| 07 | skill | Skill 执行 |
| 08 | llm | LLM Provider 通信 |
| 09 | storage | 文件 I/O、配置读写 |
| 10 | scheduler | 定时任务 |
| 90 | panic | panic 捕获（特殊段） |
| 99 | unknown | 兜底（未注册的错误） |

### 兜底机制

未注册的错误码统一走 `ODB-999999`。RecoverTo / SafeGo 中如果传入的 code 在 registry 里找不到，或漏传 code，都 fallback 到 `ODB-999999`。crash log 照样写完整调用栈。后续看到 999999 出现，就知道有新场景需要补编号。

### 示例

- `ODB-030001` — UI 渲染 slice 越界
- `ODB-040001` — 诊断引擎 LLM 调用超时
- `ODB-900001` — REPL 主循环未知 panic
- `ODB-900002` — goroutine 未知 panic
- `ODB-999999` — 未注册的兜底错误

## 严重级别

| 级别 | 含义 | 用户可见行为 |
|------|------|------------|
| FATAL | 无法恢复，必须退出 | 打印错误码 + 写 crash log + 退出 |
| ERROR | 功能失败但进程存活 | 打印错误码 + 提示 `/error ODB-XXYYYY` 查看详情 |
| WARN | 可降级继续 | 打印警告 + 错误码 |

## 核心代码结构

```
internal/oerr/
├── code.go          # 错误码常量定义（~80 行）
├── error.go         # ODBError 类型 + 构造函数（~50 行）
├── registry.go      # 错误码注册表：编号→描述+建议（~120 行）
├── recover.go       # SafeGo + RecoverTo + RecoverFatal（~60 行）
├── crash_log.go     # ~/.opendb/crash.log 写入（~50 行）
└── skill.go         # /error 命令查询错误详情（~80 行）
```

## 关键类型

```go
// ODBError 是 opendb 的标准错误类型
type ODBError struct {
    Code     string    // "ODB-030001"
    Severity Severity  // FATAL / ERROR / WARN
    Message  string    // 人类可读描述
    Cause    error     // 原始错误（可为 nil）
    Stack    string    // 调用栈（panic 时自动捕获）
    Time     time.Time
}

func (e *ODBError) Error() string {
    return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}
```

## 三层 Recover 覆盖

### 第一层 — main 入口

```go
func main() {
    defer oerr.RecoverFatal("ODB-010001") // 写 crash log + 打印 + 退出码 1
    // ...
}
```

### 第二层 — REPL 主循环每个 select case

```go
case prog := <-r.diagCh:
    oerr.RecoverTo(r.errCh, "ODB-030001", func() {
        r.renderDiagProgress(prog)
    })
```

### 第三层 — 所有 goroutine

```go
// 替代 go func() { ... }()
oerr.SafeGo("ODB-050001", func() {
    sentinel.Run(ctx)
})
```

### 覆盖范围

| 位置 | 能否覆盖 | 方式 |
|------|---------|------|
| main 初始化 | ✓ | 第一层 RecoverFatal |
| REPL 主循环事件处理 | ✓ | 第二层 RecoverTo |
| opendb 自己启动的 goroutine | ✓ | 第三层 SafeGo |
| 第三方库内部 goroutine | ✗ | Go 语言层面无解，但成熟库极少 panic |

## Crash Log

路径: `~/.opendb/crash.log`

```
[2026-04-17 10:32:15] ODB-030001 ERROR
Message: 渲染异常 — slice bounds out of range [36:2]
Stack:
  github.com/sqlrush/opendb/internal/ui.(*REPL).renderDiagProgress
    /Users/.../diag_renderer.go:315
  github.com/sqlrush/opendb/internal/ui.(*REPL).Run
    /Users/.../repl.go:401
```

## /error 查询命令

```
❯ /error ODB-030001

  ODB-030001  UI 渲染 slice 越界
  ──────────────────────────────
  模块: ui (03)
  级别: ERROR
  描述: 流式渲染中 display 长度小于缓存长度，导致切片越界
  建议: 已自动恢复，如频繁出现请升级到最新版本
  日志: ~/.opendb/crash.log
```

## 实施步骤

| 步骤 | 内容 | 改动范围 | 估算 |
|------|------|---------|------|
| 1 | 新建 `internal/oerr/` 包，定义 ODBError + 错误码常量 + registry | 新文件 | ~250 行 |
| 2 | 实现 RecoverFatal / RecoverTo / SafeGo | 新文件 | ~60 行 |
| 3 | 实现 crash_log 写入 | 新文件 | ~50 行 |
| 4 | main.go 加第一层 RecoverFatal | 改 1 处 | +3 行 |
| 5 | repl.go 主循环加第二层 RecoverTo | 改 ~5 处 | +20 行 |
| 6 | 全局搜索 `go func` 替换为 SafeGo | 改 ~15 处 | +30 行 |
| 7 | 注册第一批错误码（已知的 ~20 个高频错误） | registry 数据 | ~80 行 |
| 8 | 实现 /error 查询命令 | 新 skill | ~80 行 |

### 代码量估算

- **新增**: ~440 行（6 个新文件）
- **改动**: ~60 行（~15 个现有文件）

## 风险

- 步骤 6 改动面最大，需逐个检查所有 `go func` 调用点
- 第三方库内部 goroutine 无法注入 recover，但 pgx/go-ora 等库很成熟，panic 概率极低
- 999999 兜底确保不遗漏
