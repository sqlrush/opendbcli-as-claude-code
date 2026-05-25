# 05-附 — 重试策略详解：Claude Code withRetry.ts 源码对标

## 一、Claude Code 的 withRetry.ts 是什么

这是 Claude Code 里 **API 调用和 Opus 之间的唯一通道**。每一次 API 请求都经过这个函数包装。它的位置在架构中是：

```
Engine 主循环 (query.ts)
    ↓ 调用 API
    ↓
withRetry(operation)          ← 就在这里
    ├─ attempt 1 → 成功 → 返回
    ├─ attempt 1 → 429 → 等500ms → attempt 2
    ├─ attempt 2 → 529 → 等1s → attempt 3
    ├─ attempt 3 → 529 → 等2s → attempt 4
    ├─ attempt 4 → 529 → 连续3次529 → FallbackTriggeredError → 切Sonnet
    └─ ...
    ↓
Engine 收到响应，继续
```

**没有这一层，Claude Code 在真实世界中会频繁失败** — API 限流、服务过载、网络抖动在生产环境中是常态。

源码位置：`cc_source/source/src/services/api/withRetry.ts`（~650行）

---

## 二、退避算法源码对比

### Claude Code (`withRetry.ts:530-548`)

```typescript
const DEFAULT_MAX_RETRIES = 10
const BASE_DELAY_MS = 500

export function getRetryDelay(attempt, retryAfterHeader, maxDelayMs = 32000) {
    // 优先使用服务器返回的 retry-after
    if (retryAfterHeader) {
        const seconds = parseInt(retryAfterHeader, 10)
        if (!isNaN(seconds)) return seconds * 1000
    }
    // 指数退避: 500ms * 2^(attempt-1)，上限 32s
    const baseDelay = Math.min(BASE_DELAY_MS * Math.pow(2, attempt - 1), maxDelayMs)
    // 加 0-25% 随机抖动（防惊群）
    const jitter = Math.random() * 0.25 * baseDelay
    return baseDelay + jitter
}
// 退避序列: 500ms → 1s → 2s → 4s → 8s → 16s → 32s → 32s → 32s → 32s
```

### 当前 OpenDB

无。`openaicompat.go:76` — HTTP 非 200 直接返回错误：
```go
if httpResp.StatusCode != http.StatusOK {
    respBody, _ := io.ReadAll(httpResp.Body)
    return nil, fmt.Errorf("openai: HTTP %d: %s", httpResp.StatusCode, string(respBody))
}
```

### 新 Engine 设计（对标）

```go
func (p *Policy) calculateDelay(attempt int, info RetryInfo) time.Duration {
    if info.RetryAfter > 0 { return info.RetryAfter }           // 服务器指定优先
    delay := p.baseDelay * time.Duration(1<<uint(attempt))       // 500ms * 2^attempt
    if delay > p.maxDelay { delay = p.maxDelay }                 // 上限 30s
    jitter := time.Duration(rand.Int63n(int64(delay) / 4))       // 0-25% jitter
    return delay + jitter
}
```

---

## 三、错误分类源码对比

### Claude Code 区分了 8 种错误类型，每种不同处理

**核心分类逻辑 (`withRetry.ts:254-427`)：**

```typescript
catch (error) {
    // ── 分类 1: Fast mode 429/529 ──
    if (wasFastModeActive && (error.status === 429 || is529Error(error))) {
        retryAfterMs < 20s → 短等重试，保留 fast mode（保护 prompt cache）
        retryAfterMs >= 20s → 进入 cooldown，切标准速度
    }

    // ── 分类 2: 后台请求 529 → 直接放弃 ──
    if (is529Error && !isForeground) { throw CannotRetryError }
    // 注释原文: "during a capacity cascade each retry is 3-10× gateway
    //           amplification, and the user never sees those fail anyway"

    // ── 分类 3: 连续 529 → 模型降级 ──
    consecutive529Errors++
    if (consecutive529Errors >= 3 && fallbackModel) {
        throw new FallbackTriggeredError(model, fallbackModel)
        // → query.ts 捕获 → 用 Sonnet 重试
    }

    // ── 分类 4: 400 max_tokens 溢出 → 调整参数 ──
    if (parseMaxTokensContextOverflowError(error)) {
        retryContext.maxTokensOverride = adjustedMaxTokens
        continue  // 不消耗重试次数！
    }

    // ── 分类 5: 401 → 刷新 OAuth token ──
    // ── 分类 6: 403 token revoked → 清除缓存刷新 ──
    // ── 分类 7: Bedrock/Vertex 认证错误 → 清除凭证重试 ──
    // ── 分类 8: ECONNRESET/EPIPE → 禁用 keep-alive 重连 ──

    // ── 兜底: 标准退避等待 ──
    delayMs = getRetryDelay(attempt, retryAfter)
    await sleep(delayMs)
}
```

### 对比表

| 错误类型 | Claude Code 处理 | 当前 OpenDB | 新 Engine |
|---------|-----------------|------------|-----------|
| **429 限流** | 读 retry-after 头，退避重试 | `fmt.Errorf("HTTP %d")` 直接失败 | ✅ 对标：读 retry-after，退避 |
| **529 过载** | 最多3次→FallbackTriggeredError→切Sonnet | 直接失败 | ✅ 对标：OverloadCode=529，可触发降级 |
| **5xx 服务器错误** | 退避重试 | 直接失败 | ✅ 对标 |
| **408/409 超时/冲突** | 退避重试 | 直接失败 | ✅ 对标 |
| **413 上下文过大** | 不在 withRetry 处理，query.ts 触发压缩 | 直接失败 | ✅ 提升到 Engine 层：压缩→重试 |
| **400 max_tokens** | 解析数字，调整 max_tokens，不消耗重试次数 | 直接失败 | ✅ 提升到 Engine 层：recoverTruncatedOutput() |
| **401 认证过期** | 刷新 OAuth token → 重试 | 直接失败 | ❌ 简化：不做 OAuth |
| **ECONNRESET** | 禁用 keep-alive → 新连接重试 | 直接失败 | ✅ 简化：直接重试新连接 |

---

## 四、模型降级机制

### Claude Code (`withRetry.ts:327-364`)

```typescript
// 连续 3 次 529 → 切换到 fallback model
const MAX_529_RETRIES = 3

if (is529Error(error)) {
    consecutive529Errors++
    if (consecutive529Errors >= MAX_529_RETRIES) {
        if (options.fallbackModel) {
            logEvent('tengu_api_opus_fallback_triggered', {
                original_model: options.model,
                fallback_model: options.fallbackModel,
            })
            // 抛出特殊错误，让 query.ts 捕获并切换模型
            throw new FallbackTriggeredError(options.model, options.fallbackModel)
        }
    }
}
```

### query.ts 中的降级处理 (`query.ts:894-950`)

```typescript
if (error instanceof FallbackTriggeredError) {
    // 1. 切换到 fallback model (Opus → Sonnet)
    state.model = error.fallbackModel
    // 2. 清理 thinking signature（Sonnet 签名和 Opus 不同）
    clearThinkingSignatures(messages)
    // 3. 给用户显示警告: "Opus 过载，已切换到 Sonnet"
    yield warningMessage
    // 4. 用新模型重试当前轮次
    continue
}
```

### 新 Engine 设计

```go
// RetryPolicy 检测到连续过载 → 返回特殊错误
// Engine 主循环捕获 → 切换 provider（如果配置了 fallback）
// 比 Claude Code 更灵活：不限于 Opus→Sonnet，可以配置任意降级路径
```

---

## 五、前台/后台区分

### Claude Code 最精妙的设计之一

```typescript
// withRetry.ts:62-88
const FOREGROUND_529_RETRY_SOURCES = new Set([
    'repl_main_thread',        // 用户主线程 — 必须重试
    'agent:default',           // 子 agent — 必须重试
    'compact',                 // 压缩 — 影响后续请求
    'hook_agent',              // Hook — 必须重试
    'side_question',           // 侧边问题 — 用户在等
    'auto_mode',               // 安全分类器 — 影响权限决策
])

function shouldRetry529(querySource) {
    return querySource === undefined || FOREGROUND_529_RETRY_SOURCES.has(querySource)
}

// 后台请求（摘要、建议、标题生成）→ 529 直接放弃
if (is529Error(error) && !shouldRetry529(options.querySource)) {
    throw new CannotRetryError(error, retryContext)
}
```

**设计理由（源码注释原文）：**
> "during a capacity cascade each retry is 3-10× gateway amplification, and the user never sees those fail anyway"

翻译：后端过载时，后台请求重试会放大 3-10 倍流量，让过载更严重。而用户看不到后台请求失败，所以直接放弃是对整个系统最好的选择。

### 新 Engine 处理

当前 OpenDB 无后台请求（只有诊断主线程），所以不需要这个分类。但通过 `RateLimitCapability.IsLocal = true` 保留了类似能力——本地模型不重试网络错误（可能是模型在长时间推理，不是网络故障）。

---

## 六、持久重试模式（Unattended）

### Claude Code (`withRetry.ts:96-104`)

```typescript
// 无人值守场景（CI/CD、远程 agent）
const PERSISTENT_MAX_BACKOFF_MS = 5 * 60 * 1000    // 退避上限 5 分钟
const PERSISTENT_RESET_CAP_MS = 6 * 60 * 60 * 1000 // 总等待上限 6 小时
const HEARTBEAT_INTERVAL_MS = 30_000                 // 每 30 秒心跳

// 长等待期间分段 sleep，每段 30 秒发一次心跳
// 防止宿主环境认为会话空闲而杀掉进程
let remaining = delayMs
while (remaining > 0) {
    yield createSystemAPIErrorMessage(error, remaining, attempt, maxRetries)
    const chunk = Math.min(remaining, HEARTBEAT_INTERVAL_MS)
    await sleep(chunk)
    remaining -= chunk
}
```

### 新 Engine

不需要。OpenDB 是 DBA 交互式工具，不跑无人值守任务。

---

## 七、400 max_tokens 溢出解析

### Claude Code (`withRetry.ts:550-595`)

```typescript
// 解析 Anthropic API 的错误消息格式:
// "input length and `max_tokens` exceed context limit: 188059 + 20000 > 200000"
export function parseMaxTokensContextOverflowError(error) {
    if (error.status !== 400) return undefined
    const regex = /input length and `max_tokens` exceed context limit: (\d+) \+ (\d+) > (\d+)/
    const match = error.message.match(regex)
    // 提取: inputTokens=188059, maxTokens=20000, contextLimit=200000
    // → 计算: adjustedMaxTokens = contextLimit - inputTokens - 1000(安全缓冲)
    // → 但不低于 3000 (FLOOR_OUTPUT_TOKENS)
    retryContext.maxTokensOverride = adjustedMaxTokens
    continue  // ← 关键：不消耗重试次数！
}
```

### 新 Engine 设计

提升到 Engine 主循环层面，不在 RetryPolicy 中处理：
```go
// Engine.Run() 中:
if resp.Truncated {
    // 1. 升级 max_tokens: 8000 → 32000 → 最大值
    // 2. 注入续写提示
    // 3. 重新调用（走 callWithRetry）
    // 4. 合并前后内容
}
```

比 Claude Code 更直观——截断恢复是诊断逻辑，不是重试逻辑。

---

## 八、总结：新 Engine 借鉴了什么、简化了什么

| Claude Code withRetry.ts 能力 | 借鉴? | 说明 |
|------------------------------|-------|------|
| 指数退避 `500ms * 2^n` | ✅ 完全对标 | 核心算法一致 |
| 0-25% jitter 防惊群 | ✅ 完全对标 | 防止大量客户端同时重试 |
| retry-after 头优先 | ✅ 完全对标 | 服务器说等多久就等多久 |
| 最大重试 10 次 | ✅ 简化为 5 次 | DBA 场景不需要等那么久 |
| 429 限流重试 | ✅ 完全对标 | |
| 529 过载 + 3次后降级 | ✅ 完全对标 | OverloadCode=529 |
| 5xx 服务器错误重试 | ✅ 完全对标 | |
| 413 上下文过大 → 压缩 | ✅ 提升到 Engine 层 | 比 CC 更好：压缩后自动重试 |
| 400 max_tokens 溢出解析 | ✅ 提升到 Engine 层 | recoverTruncatedOutput() |
| 前台/后台区分 | ❌ 不需要 | OpenDB 无后台请求 |
| OAuth token 刷新 | ❌ 不需要 | OpenDB 用 API key |
| Fast mode cooldown | ❌ 不需要 | OpenDB 无 fast mode |
| Persistent retry (6h) | ❌ 不需要 | OpenDB 交互式使用 |
| Bedrock/Vertex 认证 | ❌ 不需要 | OpenDB 不走云厂商代理 |
| ECONNRESET → 禁用 keep-alive | ❌ 简化 | 直接重试新连接 |

**借鉴精华（9 项），去掉 OpenDB 不需要的（6 项），总量减半但核心能力完整。**
