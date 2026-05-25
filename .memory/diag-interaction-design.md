---
name: diag-interaction-design
description: /diag 命令的 Claude Code 风格实时交互设计方案（用户确认的交互效果）
type: project
---

# /diag 交互设计方案

## 核心原则
- 仿 Claude Code 对话交互：命令显示在聊天历史，实时逐行展示进度，非阻塞命令队列
- `/diag` 为主命令，`/diagnose` 降为别名

## 进度展示规则
- **逐行实时**: 每轮 LLM 完成后立刻渲染一行（OnRoundFunc → channel → REPL select → writeOutputLine）
- **纯规则诊断**: 无 LLM 时走同步路径，瞬间返回，无进度条
- **有 LLM**: 走异步路径，逐行展示进度

## 动态星星指示器（核心体验，必须实现）

仿 Claude Code 的 `✶ Beboppin'...` → `✻ Worked for 1m 21s` 效果：

- **运行中**: 星星字符动态闪烁（goroutine + ticker 150ms 循环覆写同一行）
  - 字符循环: `✶ ✳ ✴ ✵ ⊹ ✦`
  - ANSI 定位原地覆写: `\033[行;1H`
- **完成后**: 星星停止闪烁，固定为 `✻`，显示总用时
  - 格式: `✻ Worked for 6.9s` 或 `✻ Worked for 1m 21s`

### 时间线示例
```
t=0.0s  ✶ 正在诊断异常 #1...          ← 星星闪烁中
t=0.1s  ├ 规则分析: 活跃会话冲高
t=0.2s  ✳ AI 深度分析中...             ← 星星持续闪烁
t=1.4s  ├ 第1轮: 查询 active_sessions (1.2s)
t=3.5s  ├ 第2轮: 查询 locks, waits (2.1s)
t=6.9s  ├ 第3轮: 输出最终诊断 (3.4s)
t=6.9s  ✻ Worked for 6.9s             ← 星星固定，显示总用时
```

### 实现要点
- 星星行用 `writeOutputLine` 写入后，记录其所在 row
- goroutine 每 150ms 用 `\033[row;1H` 定位到该行，覆写下一个星星字符
- DiagPhaseDone 时停止 ticker，覆写为 `✻ Worked for Xs`
- 闪烁 goroutine 与 REPL 主 goroutine 的写冲突：用 mutex 或在 diagCh 中发 tick 事件

## 进度行格式
```
✶ 正在诊断异常 #1...                         ← DiagPhaseStart (闪烁)
├ 规则分析: 活跃会话冲高 (基线 12 → 当前 85)  ← DiagPhaseRule
✳ AI 深度分析中...                            ← DiagPhaseAIStart (闪烁)
├ 第1轮: 查询 active_sessions (1.2s)         ← DiagPhaseAIRound
│  ↳ 返回 85 行, 68 个锁等待                  ← 步骤返回摘要
├ 第2轮: 查询 locks, waits (2.1s)
│  ↳ 返回 12 个锁链, 顶部阻塞 SID 338
├ 第3轮: 输出最终诊断 (3.4s)
✻ Worked for 6.9s                            ← DiagPhaseDone (固定)
```

## 符号含义
| 符号 | 含义 |
|------|------|
| `✶✳✴✵⊹✦` | 动态闪烁星星，表示 LLM 正在工作 |
| `✻` | 固定星星，表示完成 + 用时 |
| `├` | 进行中的步骤 |
| `│  ↳` | 步骤返回摘要（skill result 关键数字） |
| `⏳` | 排队中的命令 |
| `▸ 执行排队命令:` | diag 完成后自动执行的队列 |

## 命令队列交互
- diag 运行中用户输入命令回车 → 显示在聊天历史 + `⏳` 标记 → 入队
- diag 完成后 → 逐个执行队列中的命令
- 格式: `❯ SELECT ... ⏳` (排队中) → `▸ 执行排队命令: SELECT ...` (执行时)

## 实现架构
- DiagnoseSkill 添加 `onProgress` 回调
- REPL 添加 `diagCh`、`diagRunning`、`cmdQueue`
- 主循环 select 增加 `case prog := <-r.diagCh`
- 仿 sentinel alertCh 模式：所有渲染在 REPL 主 goroutine，channel 隔离
- OnRoundFunc 已就绪（prompt_loop.go），只需连接

## 失败降级
- LLM 超时/报错 → 显示 `└ ⚠ AI 分析失败: ...` → 规则诊断结果仍完整展示
