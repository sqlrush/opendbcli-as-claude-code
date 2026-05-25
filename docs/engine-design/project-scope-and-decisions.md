# OpenDB LLM 通信优化专项 — 项目范围与决策记录

> 日期：2026-04-03

## 项目目标

优化 OpenDB 与 LLM 的通信效率和准确性，以 Claude Code 为标杆，让 OpenDB 变成同样聪明、高效、准确的方式和 LLM 沟通。

## 工作策略

- **方案 A：在 ~/opendb 仓库内 feature/engine-v2 分支直接开发**
- 新代码在 `internal/engine/` 下，engine 包不 import 现有 llm/agent 包（bridge 桥接）
- 开发完直接替换 diagnose.go 的调用路径，删掉老代码
- 有问题 git checkout main 回退
- ~/opendbllm/ 只放设计文档和实现计划
- 参考 ~/cc_source 中 Claude Code 源码和已分析的交互模式
- 仅优化 LLM 通信模块，不涉及 Skill 实现、探针层、规则引擎、UI 渲染等

## 已确认的决策

### 1. 优化范围：P0 + P1 + P2（12项）

本项目是 LLM 交互专项，尽可能做完善。

| 优先级 | 项目 |
|--------|------|
| **P0** | 系统提示重构、工具使用策略指导、结果截断优化、adaptive thinking |
| **P1** | HTTP重试+指数退避、上下文窗口管理、max_output_tokens恢复、工具并发执行 |
| **P2** | Prompt Cache、流式工具预执行、动态工具描述、task_budget |

### 2. 模型策略：通用基座 + 厂商专属增强

- 所有优化首先在通用层实现，让所有模型受益
- 基于每个厂商 API 的能力和特点，做专属优化
- Anthropic Claude API：prompt cache / adaptive thinking / effort / task_budget / cache_edits
- 其他厂商：基于各自 API 特点做专属增强（待讨论具体对接哪些厂商）

### 3. 架构方案：方案 B — 统一通信引擎（Unified Engine）

选择理由：
1. 消除技术债 — 当前 4 套 agent loop（Oracle/MySQL/PG/OpenGauss）90% 代码重复，合并为一个
2. 一处优化全局受益 — 加了重试，所有 DB 同时受益
3. 厂商能力检测天然支持"通用基座 + 专属增强"
4. 不过度工程化 — OpenDB 场景比 Claude Code 简单（无图片/MCP/@附件/IDE），取精华保精简
5. Go 语言 interface + struct 组合天然适合 Engine 模式

### 4. 项目边界

**在范围内（本项目优化）：**
- 通信基础设施：重试/降级、流式处理、prompt cache、上下文压缩
- 推理编排（Agent Loop）：系统提示、工具选择策略、工具执行编排、结果处理、收敛控制、上下文管理
- 这两层不可分割，对应 Claude Code 的 query.ts 和 OpenDB 的 agent/loop.go

**不在范围内：**
- Skill 本身的实现（/slowsql, /explain 等的查询逻辑）
- Sentinel 探针层（数据采集、3σ检测、爆发采集）
- 规则引擎（classify.go 的分类逻辑）
- UI 渲染（TUI 输出格式）
- 连接管理、配置系统

## 参考资料

- [对比分析报告](./opendb-vs-claudecode-llm-communication-comparison.md)
- ~/cc_source/ — Claude Code 源码分析
- ~/opendb/ — OpenDB 源代码
