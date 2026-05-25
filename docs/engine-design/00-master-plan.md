# OpenDB LLM 通信引擎 — 总实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 OpenDB 的 LLM 通信从"裸循环"升级为对标 Claude Code 的统一通信引擎，覆盖 P0+P1+P2 共 12 项优化。

**Architecture:** 统一通信引擎（Engine）替代现有 4 套分散的 agent loop。Engine 通过 ProviderCapability 驱动厂商适配，通过 PromptProfile 驱动 DB 差异化，实现"一处优化全局受益"。

**Tech Stack:** Go 1.26+, module `github.com/sqlrush/opendb`, 新增 `internal/engine/` 包。

---

## 开发策略

**方案 A：在 opendb 仓库内开分支直接开发。**

```bash
cd ~/opendb
git checkout -b feature/engine-v2
# 新代码全部在 internal/engine/ 下
# 开发完直接替换 diagnose.go 的调用路径
# 有问题 git checkout main 回退
```

纪律：`internal/engine/` 不 import `internal/llm/` 或 `internal/{oracle,mysql,postgres}/agent/`。
桥接层 `internal/engine/bridge/` 是唯一连接点。
`~/opendbllm/` 只放设计文档和实现计划。

---

## 5 个子计划总览

```
子计划 1: 基础层 (Core Types + ProviderCapability + RetryPolicy)
  → 定义所有数据结构和接口，不依赖其他新模块
  → 产出: engine 的骨架可编译通过

子计划 2: 工具层 (ToolOrchestrator + ResultHandler)
  → 并发执行 + 智能截断
  → 产出: 可独立测试的工具执行模块

子计划 3: 上下文层 (ContextManager + ContextBuilder + PromptProfile + SystemPrompts)
  → Token 追踪 + 压缩 + 系统提示重构 + 用户自定义规则
  → 产出: 可独立测试的上下文管理模块

子计划 4: Engine 主循环 + Provider Adapters
  → 统一 Agent Loop + OpenAICompat/Ollama/MLX 适配器（先做最常用的）
  → 产出: 可端到端运行的 Engine（对接 Ollama 测试）

子计划 5: 完整适配 + 集成
  → Anthropic/Gemini/vLLM 适配器 + 与现有 OpenDB 集成 + 用户自定义规则
  → 产出: 替换现有 agent loop，opendb 使用新 Engine
```

## 依赖关系

```
子计划 1: 基础层
    ↓
子计划 2: 工具层 ──────┐
    ↓                  ↓
子计划 3: 上下文层 ──→ 子计划 4: Engine + Adapters
                              ↓
                       子计划 5: 完整适配 + 集成
```

子计划 2 和 3 可以并行开发（互不依赖），但都依赖子计划 1。

## 各子计划文件对应

| 子计划 | 设计文档 | 产出文件 | 预估工作量 |
|--------|---------|---------|-----------|
| 1 基础层 | 02-core-types, 03-provider-adapter, 05-retry-policy | types.go, capability.go, adapter.go, policy.go, ratelimit.go, httperror.go | 中 |
| 2 工具层 | 06-tool-orchestrator | orchestrator.go, result.go, schema.go + 测试 | 中 |
| 3 上下文层 | 04-context-builder, 07-context-manager, 09-prompt-profile, 10-system-prompts, 11-user-rules | builder.go, manager.go, compressor.go, profile.go, oracle.go, mysql.go, postgres.go, prompts.go, rules.go + 测试 | 大 |
| 4 Engine+Adapters | 08-engine-main-loop, 03-provider-adapter | engine.go, config.go, openaicompat.go, ollama.go, mlx.go + 测试 | 大 |
| 5 集成 | 03-provider-adapter, 12-claudemd-reference | anthropic.go, gemini.go, vllm.go, 改造 diagnose.go + 集成测试 | 大 |

## 开发原则

1. **TDD** — 每个模块先写测试再写实现
2. **频繁提交** — 每完成一个 Task 就 commit
3. **不可变** — 遵循 coding style，函数返回新对象不修改原对象
4. **向后兼容** — 新 Engine 和现有 agent loop 并存，通过配置切换
5. **先跑通再优化** — 子计划 4 先对接 Ollama 跑通完整流程，再加其他 adapter

## 详细实现计划

各子计划的详细步骤在独立文件中：

- [子计划 1: 基础层](01-foundation.md) ← 先做这个
- [子计划 2: 工具层](02-tool-layer.md)
- [子计划 3: 上下文层](03-context-layer.md)
- [子计划 4: Engine + Adapters](04-engine-adapters.md)
- [子计划 5: 完整适配 + 集成](05-integration.md)
