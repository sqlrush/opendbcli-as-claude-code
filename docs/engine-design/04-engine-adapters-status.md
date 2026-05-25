# 子计划 4 进度 — Engine + Adapters

## 当前状态：循环依赖待解决

### 已完成的代码

| 文件 | 内容 | 状态 |
|------|------|------|
| `engine/engine.go` | Run() 主循环（8步pipeline）| 已写，待编译 |
| `engine/engine_test.go` | 5个测试（playbook/多轮/超轮/回调/流式）| 已写，待编译 |
| `engine/adapter.go` | ProviderAdapter 接口 | 已写 |
| `engine/provider/openaicompat.go` | OpenAI + 7家国产厂商适配（~500行）| 已写，待编译 |
| `engine/provider/ollama.go` | Ollama 本地适配 | 已写，待编译 |
| `engine/provider/factory.go` | NewAdapter() 工厂 + DetectVendor() | 已写，待编译 |

### 遇到的问题：Go 循环依赖

```
engine 包    ←──── imports ────── provider 包
  │                                  │
  │ 定义:                             │ 定义:
  │   Request                        │   ProviderCapability
  │   Response                       │   OpenAICompatAdapter
  │   Message                        │   OllamaAdapter
  │   Stream                         │
  │   ProviderAdapter 接口            │ 需要:
  │                                  │   engine.Request (Chat方法签名)
  │ 需要:                             │   engine.Response (Chat返回值)
  │   provider.ProviderCapability    │   engine.Stream (ChatStream返回值)
  │                                  │
  └──── imports ────────────────────→┘
         循环!
```

Go 不允许两个包互相 import。这是经典的 Go 包设计问题。

### 解决方案

**方案：把共享类型移到 provider 包**

```
调整后:

provider 包（不 import 任何 engine 子包）:
  ├── types.go          ← Message, Request, Response, Stream, Usage 等（从 engine/types.go 移过来）
  ├── capability.go     ← ProviderCapability（已有）
  ├── adapter.go        ← ProviderAdapter 接口（引用 provider.Request/Response）
  ├── openaicompat.go   ← 具体实现
  ├── ollama.go
  └── factory.go

engine 包（只 import provider，不被 provider import）:
  ├── engine.go         ← Run() 主循环，使用 provider.Message/Request/Response
  ├── config.go         ← EngineConfig, EngineInput, EngineResult（保留在 engine）
  └── adapter.go        ← 删除，接口移到 provider 包

context 包（只 import provider，不 import engine）:
  ├── builder.go        ← 使用 provider.Message/SystemPromptBlock
  ├── manager.go
  └── ...

tool 包（只 import provider，不 import engine）:
  ├── orchestrator.go   ← 使用 provider.ToolCall
  └── ...

依赖关系:
  engine → provider (用 types + Capability + Adapter 接口)
  engine → context  (用 Builder/Manager)
  engine → tool     (用 Orchestrator)
  engine → retry    (用 Policy)
  context → provider (用 types + Capability)
  tool → provider    (用 ToolCall)
  retry → provider   (用 HTTPError + RateLimitCapability)

  provider → (不 import 任何 engine 子包)
  → 零循环!
```

### 具体操作步骤

```
1. 把 engine/types.go 中的类型移到 provider/types.go:
   - Message, ThinkingBlock, CacheControl, ToolCall, ToolSchema
   - SystemPromptBlock
   - Request, Response, Usage, CacheStats
   - Stream, StreamEvent, StreamEventType

2. engine/types.go 删除（或只保留 engine 专属类型如 EngineConfig/EngineInput/EngineResult）

3. 把 ProviderAdapter 接口从 engine/adapter.go 移回 provider/adapter.go
   （现在 adapter 引用的 Request/Response 也在 provider 包中，不再循环）

4. 更新所有 import:
   - engine.go: engine.Message → provider.Message
   - context/builder.go: 删除本地 Message 类型定义，用 provider.Message
   - context/manager.go: 同上
   - tool/orchestrator.go: engine.ToolCall → provider.ToolCall
   - retry/policy.go: 已经只用 provider.HTTPError，不需要改

5. 更新所有测试的 import

6. 编译验证 + 跑测试
```

### 影响范围

```
需要修改的文件:
  provider/types.go         ← 新建（从 engine/types.go 移入）
  provider/adapter.go       ← 恢复 ProviderAdapter 接口
  engine/types.go           ← 删除共享类型，只留 EngineConfig 等
  engine/adapter.go         ← 删除
  engine/engine.go          ← import 改为 provider.Message 等
  engine/engine_test.go     ← import 改为 provider.Message 等
  engine/types_test.go      ← 测试改为 provider 包
  context/builder.go        ← 删除本地 Message 定义，用 provider.Message
  context/compressor.go     ← 同上
  context/manager.go        ← 同上
  context/tokencount.go     ← 同上
  context/*_test.go         ← 更新 import
  tool/orchestrator.go      ← engine.ToolCall → provider.ToolCall
  tool/orchestrator_test.go ← 更新 import

不需要修改的文件:
  provider/capability.go    ← 已经没有外部依赖
  provider/openaicompat.go  ← 会直接用 provider 包内的类型
  provider/ollama.go        ← 同上
  retry/policy.go           ← 已经只用 provider 包
  profile/*.go              ← 已经不依赖 engine
```

### 预计工作量

这是纯机械性重构（移动类型 + 改 import），不涉及逻辑变更。
预计 20-30 分钟完成，然后所有测试应该仍然通过。
