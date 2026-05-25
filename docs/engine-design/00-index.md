# OpenDB LLM 通信引擎 — 设计文档索引

> 架构方案：B — 统一通信引擎（Unified Engine）
> 优化范围：P0 + P1 + P2（12项）
> 模型策略：通用基座 + 厂商专属增强

## 设计文档

| # | 文档 | 内容 | 状态 |
|---|------|------|------|
| 01 | [Engine 整体架构](01-engine-architecture.md) | 包结构、依赖关系、与现有代码集成点 | ✅ |
| 02 | [核心类型](02-core-types.md) | Message/Request/Response/Usage 扩展设计 | ✅ |
| 03 | [ProviderAdapter](03-provider-adapter.md) | 厂商适配接口、Capability 体系、各厂商实现 | ✅ |
| 04 | [ContextBuilder](04-context-builder.md) | 5层上下文注入、系统提示重构、环境/诊断上下文 | ✅ |
| 05 | [RetryPolicy](05-retry-policy.md) | 指数退避、错误分类、限流头解析、413恢复 | ✅ |
| 06 | [ToolOrchestrator + ResultHandler](06-tool-orchestrator.md) | 并发/串行执行、流式预执行、动态截断预算 | ✅ |
| 07 | [ContextManager](07-context-manager.md) | Token追踪、3层压缩、阈值控制 | ✅ |
| 08 | [Engine 主循环](08-engine-main-loop.md) | 统一 Agent Loop、截断恢复、双路径保留 | ✅ |
| 09 | [PromptProfile](09-prompt-profile.md) | DB特定配置、Oracle/MySQL/PG 实现 | ✅ |
| 10 | [系统提示词](10-system-prompts.md) | 生产级提示词完整内容（~4750字） | ✅ |
| 11 | [用户自定义规则](11-user-custom-rules.md) | 对标CLAUDE.md，3级优先级（全局/实例/会话） | ✅ |
| 12 | [CLAUDE.md机制参考](12-claudemd-reference.md) | CC的4级CLAUDE.md机制详解 + opendb已创建CLAUDE.md | ✅ |

## 深度对比文档

| 文档 | 内容 |
|------|------|
| [04-附: 5层上下文注入详解](04-context-builder-deep-dive.md) | 当前2层 vs 新5层逐层对比，含发送时序图 |
| [03-附: 5个Adapter详解](03-provider-adapter-deep-dive.md) | 三族API格式对比、国产模型兼容情况、为什么5个不多不少 |
| [05-附: 重试策略源码对标](05-retry-policy-deep-dive.md) | Claude Code withRetry.ts 源码级对比，9项借鉴6项简化 |
| [06-附: 工具执行+截断详解](06-tool-orchestrator-deep-dive.md) | 并发执行+智能截断+流式预执行，对标CC源码逐项对应 |
| [07-附: 上下文压缩详解](07-context-manager-deep-dive.md) | 3层压缩体系 vs Claude Code 5层，小模型从8轮崩到20轮完整跑完 |
| [08-附: 统一Agent Loop详细对比](08-engine-main-loop-comparison.md) | 当前裸循环 vs 新8步循环，8项提升量化对比 |
| [09-附: PromptProfile详解](09-prompt-profile-deep-dive.md) | 从2914行82%重复到520行零重复，加新DB省85%代码 |
| [10-附: 系统提示词优化详解](10-system-prompts-deep-dive.md) | 1200字→4750字逐项对比，18项优化来源(CC借鉴10/自研4/保留4) |

## 12项优化在设计中的覆盖

| # | 优化项 | 对应设计文档 |
|---|--------|-------------|
| **P0** | | |
| 1 | 系统提示重构 | 04-ContextBuilder Layer 1 |
| 2 | 工具使用策略指导 | 04-ContextBuilder Layer 1 + 09-PromptProfile |
| 3 | 结果截断优化 | 06-ToolOrchestrator ResultHandler |
| 4 | Adaptive thinking | 03-ProviderAdapter Capability |
| **P1** | | |
| 5 | HTTP重试+指数退避 | 05-RetryPolicy |
| 6 | 上下文窗口管理 | 07-ContextManager |
| 7 | max_output_tokens恢复 | 08-Engine recoverTruncatedOutput |
| 8 | 工具并发执行 | 06-ToolOrchestrator partition+concurrent |
| **P2** | | |
| 9 | Prompt Cache | 03-ProviderAdapter CachingCapability + 04-ContextBuilder cache marks |
| 10 | 流式工具预执行 | 06-ToolOrchestrator ExecuteStreaming |
| 11 | 动态工具描述 | 04-ContextBuilder Layer 4 + 09-PromptProfile ToolUsageHint |
| 12 | task_budget | 03-ProviderAdapter OutputCapability + 08-Engine buildRequest |

## 关联文档

- [项目范围与决策](../project-scope-and-decisions.md)
- [对比分析报告](../opendb-vs-claudecode-llm-communication-comparison.md)
- [厂商能力调研](../provider-capability-research.md)
