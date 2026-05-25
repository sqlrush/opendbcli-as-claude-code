---
name: Engine V2 状态 (v0.9.27)
description: LLM Engine V2 已合入主干，7个模型测试状态和待办
type: project
---

## Engine V2 — v0.9.27 已合入 main

feature/engine-v2 于 2026-04-06 合入主干。统一 LLM 通信引擎替换了 4 套老 agent loop。

### 已验证模型

| 模型 | 状态 | 轮次 | 备注 |
|------|------|------|------|
| Opus | ✅ | 1 | 推断强但伪造证据链（已加工具记录注入缓解） |
| Kimi | ✅ | 6-10 | reasoning_content 已修复 |
| MiniMax | ✅ | 9 | tool call delta 累积已修复 |
| MiMo | ✅ | 14 | 第一个测通的模型 |
| DeepSeek | ✅ | 18 | 幽灵 tool call 已过滤 |
| Gemini | ✅ | 14 | SSE 超时降级已修复 |
| GLM-5 | ��� | 15 | 最诚实，主动标注数据来源 |

### v0.9.26 → v0.9.27 ��复

- 工具调用记录每轮注入（不只收敛时），防 Opus 伪造证据链
- 证据表格示例（GLM-5 风格）加入 system prompt
- streaming finish_reason 传递，修复截断恢复不触发的问题

### 待办

1. **Opus 证据链** — 工具记录每轮注入已实现，需验证 Opus 是否不再伪造
2. **SmartTruncate 截断过激** — 代码审查发现，未实际触发，暂不改
3. **上下文压缩过于激进** — 代码审查发现，未实际触发，暂不改
4. **诊断中连接断开** — 多轮工具调用密集查询时偶发 closed connection，导致后续命令阻塞
5. **压测服务器部署** — root@47.251.30.180:2222，已编译 linux 二进制待部署

### 关键教训（详见 opendbllm 项目记忆）

- 重构时必须逐一核对老代码功能点，胶水代码最容易遗漏
- streaming 不能假设所有 provider 支持，先试后降级
- 提示词约束要具体（数据-工具对应表），不能泛泛说"查 2-3 个维度"
- 改共用组件必须验证所有调用方（picker.go 教训）
- UI 变更必须跑 PTY+midterm 自动化测试
