# OG Engine 上下文 / 记忆 / 画像 调用链

> 修复 commit: (pending v1.1.08)
> 验证时间: 2026-04-23
> 测试实例: openGauss 5.0.0 on 47.251.30.180:15432

## 调用链（修复后）

```
┌────────────────────────────────────────────────────────────────┐
│ 1. 启动                                                        │
│    cmd/opendb/main.go                                          │
│    ├─ REPL 路径:   registerSharedSkills → sharedMemStore       │
│    │              repl.SetActiveInstanceSync(store.SetActive…) │
│    └─ batch 路径:  batchMemStore.SetActiveInstance(connName)   │
└────────────────────┬───────────────────────────────────────────┘
                     │
                     ▼
┌────────────────────────────────────────────────────────────────┐
│ 2. /login 连接成功                                             │
│    REPL: "Connected to xxx"                                    │
│    ├─ initContextStores(name)                                  │
│    │   └─ DiagnoseSkill.SetContextStores(baseDir, instance)   │
│    │       ├─ sessionStore = NewFileSessionStore(.../sessions) │
│    │       ├─ memoryStore  = NewStore(.../memory)              │
│    │       │   memoryStore.SetActiveInstance(instance)         │
│    │       ├─ policyLoader = NewLoader(.../policies)           │
│    │       └─ sessionID = ResumeOrNew(store, instance)  ★新   │
│    │           复用最近 24h 内 active session，否则 mint 新    │
│    └─ activeInstanceSync(name)                                 │
│        └─ sharedMemStore.SetActiveInstance(instance)           │
│            (让 /profile /memory_write /memory_recall 指向当前) │
└────────────────────┬───────────────────────────────────────────┘
                     │
                     ▼
┌────────────────────────────────────────────────────────────────┐
│ 3. /llm 触发                                                   │
│    DiagnoseSkill.Execute                                       │
│    └─ agent.NewDiagnoser(...)                                  │
│        └─ diagnoser.SetContextStoresFrom(                     │
│              s.sessionStore, s.memoryStore,                    │
│              s.policyLoader, s.sessionID)                      │
└────────────────────┬───────────────────────────────────────────┘
                     │
                     ▼
┌────────────────────────────────────────────────────────────────┐
│ 4. Diagnoser.runEngine                                         │
│    ├─ opts = [WithSessionStore, WithMemoryStore,              │
│    │          WithPolicyLoader]                                │
│    ├─ if !memoryStore.ProfileExists():               ★新      │
│    │     WriteProfile(ProfileTemplate(instance, "opengauss"))  │
│    └─ engine.New(adapter, OpenGaussProfile, ...opts)           │
└────────────────────┬───────────────────────────────────────────┘
                     │
                     ▼
┌────────────────────────────────────────────────────────────────┐
│ 5. Engine.Run                                                  │
│    ├─ 如果 SessionID 不为空 且 sessionStore 存在：             │
│    │     historyMessages = sessionStore.Load(SessionID).Msgs   │
│    ├─ contextBuilder.Build(input + historyMessages)            │
│    │   └─ buildSystemPrompt:                                   │
│    │       ├─ universalSystemPrompt                            │
│    │       ├─ profile.SystemPromptRules (OG-specific)          │
│    │       │   包含 "记忆与画像（必须主动维护）" 章节  ★新   │
│    │       ├─ memoryStore.LoadProfile (PROFILE.md 全文)        │
│    │       └─ memoryStore.LoadIndex (MEMORY.md 索引)           │
│    ├─ 多轮循环：LLM 调工具（含 memory_write/update/recall/     │
│    │            profile）                                      │
│    └─ 每轮 sessionStore.Save(updated session)                  │
└────────────────────────────────────────────────────────────────┘
```

## 修复清单

| # | 修法 | 代码 |
|---|---|---|
| **A** | batch 模式下也调 `SetContextStores` | `cmd/opendb/main.go:batchCtxInitializer` + `batchDiagSkills[...]` loop |
| **B** | `SetContextStores` 用 `ResumeOrNew` 复用最近 active session（24h 内） | `internal/engine/session/resume.go` + 四库 DiagnoseSkill |
| **C** | 注册 memory_write / memory_recall / memory_update / profile 4 个 shared skill | `cmd/opendb/main.go:registerSharedSkills` |
| **D** | REPL `/login` 成功时同步 sharedMemStore 的 activeInstance | REPL `activeInstanceSync` 回调 |
| **E** | Diagnoser 首次 /llm 自动用 OG 模板 seed PROFILE.md | `internal/opengauss/agent/diagnose.go:runEngine` |
| **F** | ProfileTemplate 按 product 分支（generic / postgres / opengauss） | `internal/engine/memory/profile.go` |
| **G** | OG profile 加 "记忆与画像（必须主动维护）" 章节指导 LLM | `internal/engine/profile/opengauss.go` |

## 真机验证证据（2026-04-23 19:40）

### 端到端证明

```
第一轮 /llm → 实例首次诊断
  - 文件产物:
      ~/.opendb/sessions/og/og:<uuid>.jsonl   (session 持久化)
      ~/.opendb/memory/og/PROFILE.md          (OG 模板自动写入)
      ~/.opendb/memory/og/MEMORY.md           (索引)
      ~/.opendb/memory/og/pattern_xxx.md      (LLM 调 memory_write
                                               写的 pattern 记忆)
  - LLM 实际调用工具: profile / health / activesessions / waits / xid /
                      space  (LLM 认识 profile 工具并主动调)

第二轮 /llm "你对这个实例了解多少" (另一个 opendb 进程)
  - session 复用: 未重建 session 文件
  - LLM 引用第一轮结果: "对 WLM 长驻会话做进一步核实（方案 2 中的 SQL）"
      ← 证明 historyMessages 被注入
  - LLM 主动要求: "确认后我会调用 memory_update 写回 PROFILE.md"
      ← 证明 prompt 里 OG "记忆与画像" 章节生效

/profile 命令
  - 输出 PROFILE.md 全文 (OG 模板)
```

## 关键回归保护

`session.ResumeOrNew` 的 24h cap 防止无限复用陈年 session
（memory store 也会按 file 数量轮转）。

如果 REPL `activeInstanceSync` 漏传或 Diagnoser 不用 `SetContextStoresFrom`,
下次跑 batch /llm 第二轮会丢失 history，这是回归信号。
