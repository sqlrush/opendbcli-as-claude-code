# 01 — Engine 整体架构

## 包结构

```
internal/engine/
├── engine.go              // Engine 主结构 + Run() 主循环
├── types.go               // 核心类型：Message, Request, Response, Usage
├── config.go              // EngineConfig, EngineInput, EngineResult
│
├── provider/
│   ├── capability.go      // ProviderCapability 能力声明
│   ├── adapter.go         // ProviderAdapter 接口
│   ├── anthropic.go       // Anthropic Claude 适配（独立协议）
│   ├── gemini.go          // Google Gemini 适配（独立协议）
│   ├── openaicompat.go    // OpenAI + 国产模型通用适配 (OpenAI/DeepSeek/Kimi/Qwen/GLM/MiniMax/MiMo)
│   ├── ollama.go          // Ollama 本地适配
│   ├── vllm.go            // vLLM 本地适配 (NVIDIA GPU)
│   └── mlx.go             // MLX 本地适配 (Apple Silicon)
│
├── context/
│   ├── builder.go         // ContextBuilder: 系统提示 + 上下文组装
│   ├── manager.go         // ContextManager: token追踪 + 压缩
│   └── compressor.go      // 压缩策略：截断/折叠/摘要
│
├── tool/
│   ├── orchestrator.go    // ToolOrchestrator: 并发/串行/预执行
│   ├── result.go          // ResultHandler: 截断/预算/格式化
│   └── schema.go          // 动态工具描述生成
│
├── retry/
│   ├── policy.go          // RetryPolicy: 指数退避 + 厂商错误处理
│   └── ratelimit.go       // 限流头解析 + 退避计算
│
└── profile/
    ├── profile.go         // PromptProfile 接口: DB特定配置
    ├── oracle.go          // Oracle 系统提示 + 工具策略
    ├── mysql.go           // MySQL
    ├── postgres.go        // PostgreSQL
    └── opengauss.go       // OpenGauss
```

## 依赖关系

```
                    ┌──────────┐
                    │  Engine  │ ← 唯一入口
                    └────┬─────┘
          ┌──────────┬───┴────┬──────────┬──────────┐
          ▼          ▼        ▼          ▼          ▼
    ContextBuilder  Provider  Tool      Context   Retry
                    Adapter   Orchestr.  Manager   Policy
          │          │        │          │          │
          ▼          ▼        ▼          ▼          ▼
    PromptProfile  Capability ResultH.  Compressor RateLimit
    (DB特定)       (厂商特定) (通用)    (通用)     (厂商特定)
```

## Engine 与现有 OpenDB 的集成点

```
现有 OpenDB                          新 Engine
─────────────────                    ──────────────
internal/llm/provider.go             → 被 engine/provider/adapter.go 替代
internal/llm/types.go                → 被 engine/types.go 替代
internal/llm/openaicompat/           → 被 engine/provider/*.go 替代
internal/llm/ollama/                 → 被 engine/provider/ollama.go 替代
internal/model/manager.go            → 保留，增加 Capability() 方法
internal/model/profile.go            → 保留，增加能力字段

internal/oracle/agent/loop.go        → 被 engine/engine.go 替代（统一循环）
internal/mysql/agent/loop.go         → 被 engine/engine.go 替代
internal/postgres/agent/loop.go      → 被 engine/engine.go 替代
internal/opengauss/agent/loop.go     → 被 engine/engine.go 替代

internal/oracle/agent/prompt*.go     → 迁移到 engine/profile/oracle.go
internal/mysql/agent/prompt.go       → 迁移到 engine/profile/mysql.go
internal/postgres/agent/prompt.go    → 迁移到 engine/profile/postgres.go
internal/opengauss/agent/prompt.go   → 迁移到 engine/profile/opengauss.go

internal/skill/executor.go           → Engine 通过接口调用，不改动
internal/skill/registry.go           → Engine 通过接口调用，不改动
```

## 设计原则

1. **Engine 不依赖任何 DB 特定代码** — DB 差异全部通过 PromptProfile 接口注入
2. **Engine 不依赖任何厂商特定代码** — 厂商差异全部通过 ProviderAdapter + Capability 注入
3. **Engine 可独立测试** — 不需要真实数据库连接或 LLM API
4. **向后兼容** — 现有 model.Manager 保留，Engine 作为新的调用层
5. **不可变消息** — 遵循 coding style，所有消息处理返回新对象
