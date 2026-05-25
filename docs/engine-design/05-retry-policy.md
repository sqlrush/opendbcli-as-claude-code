# 05 — RetryPolicy 设计

## 当前问题

OpenDB 当前完全没有重试机制：HTTP 非 200 → 直接返回错误。
这导致间歇性网络问题、API 限流、服务过载都会直接中断诊断。

## 设计目标

对标 Claude Code 的 withRetry()：
- 指数退避 + jitter
- 厂商特定错误码识别（429/529/5xx）
- 限流头解析（retry-after）
- 最大重试次数限制
- 本地部署跳过网络重试

## 接口定义

```go
package retry

type Policy struct {
    maxRetries    int           // 默认 5
    baseDelay     time.Duration // 默认 500ms
    maxDelay      time.Duration // 默认 30s
    capability    *provider.RateLimitCapability
}

type Config struct {
    MaxRetries int
    BaseDelay  time.Duration
    MaxDelay   time.Duration
}

func DefaultConfig() Config {
    return Config{
        MaxRetries: 5,
        BaseDelay:  500 * time.Millisecond,
        MaxDelay:   30 * time.Second,
    }
}

func NewPolicy(cfg Config, cap *provider.RateLimitCapability) *Policy

// Execute 包装一个 API 调用，添加重试逻辑
func (p *Policy) Execute(
    ctx context.Context,
    fn func(ctx context.Context) (*engine.Response, error),
) (*engine.Response, error)

// ExecuteStream 包装流式 API 调用
func (p *Policy) ExecuteStream(
    ctx context.Context,
    fn func(ctx context.Context) (engine.Stream, error),
) (engine.Stream, error)
```

## 重试逻辑

```go
func (p *Policy) Execute(
    ctx context.Context,
    fn func(ctx context.Context) (*engine.Response, error),
) (*engine.Response, error) {
    var lastErr error

    for attempt := 0; attempt <= p.maxRetries; attempt++ {
        resp, err := fn(ctx)
        if err == nil {
            return resp, nil
        }

        // 判断是否可重试
        retryInfo := p.classifyError(err)
        if !retryInfo.Retryable {
            return nil, err
        }

        lastErr = err

        // 最后一次尝试不再等待
        if attempt == p.maxRetries {
            break
        }

        // 计算退避时间
        delay := p.calculateDelay(attempt, retryInfo)

        // 等待或被取消
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(delay):
        }
    }

    return nil, fmt.Errorf("max retries (%d) exceeded: %w", p.maxRetries, lastErr)
}
```

## 错误分类

```go
type RetryInfo struct {
    Retryable       bool
    RetryAfter      time.Duration // 服务器建议的等待时间
    IsRateLimit     bool          // 429 限流
    IsOverload      bool          // 529 过载 (Anthropic)
    IsServerError   bool          // 5xx
    IsNetworkError  bool          // 连接错误
    IsContextTooLong bool         // 413 请求过大
    IsOutputTruncated bool        // max_output_tokens 截断
}

func (p *Policy) classifyError(err error) RetryInfo {
    info := RetryInfo{}

    var httpErr *HTTPError
    if !errors.As(err, &httpErr) {
        // 非 HTTP 错误（网络超时等）
        if p.capability.IsLocal {
            // 本地部署：不重试网络错误（可能是模型在推理中）
            return info
        }
        info.Retryable = true
        info.IsNetworkError = true
        return info
    }

    switch httpErr.StatusCode {
    case 429:
        info.Retryable = true
        info.IsRateLimit = true
        info.RetryAfter = parseRetryAfter(httpErr.Headers)

    case 529: // Anthropic 过载
        if p.capability.OverloadCode == 529 {
            info.Retryable = true
            info.IsOverload = true
            info.RetryAfter = parseRetryAfter(httpErr.Headers)
        }

    case 413:
        info.IsContextTooLong = true
        // 不直接重试，由 Engine 触发压缩后重试
        info.Retryable = false

    case 408, 409:
        info.Retryable = true

    default:
        if httpErr.StatusCode >= 500 {
            info.Retryable = true
            info.IsServerError = true
        }
    }

    return info
}
```

## 退避计算

```go
func (p *Policy) calculateDelay(attempt int, info RetryInfo) time.Duration {
    // 如果服务器返回了 retry-after，优先使用
    if info.RetryAfter > 0 {
        return info.RetryAfter
    }

    // 指数退避: baseDelay * 2^attempt
    delay := p.baseDelay * time.Duration(1<<uint(attempt))

    // 上限
    if delay > p.maxDelay {
        delay = p.maxDelay
    }

    // 添加 0-25% jitter 防止惊群
    jitter := time.Duration(rand.Int63n(int64(delay) / 4))
    delay += jitter

    return delay
}
```

## 限流头解析

```go
func parseRetryAfter(headers http.Header) time.Duration {
    // 标准 Retry-After header
    if v := headers.Get("retry-after"); v != "" {
        if seconds, err := strconv.ParseFloat(v, 64); err == nil {
            return time.Duration(seconds * float64(time.Second))
        }
        // HTTP-date 格式
        if t, err := http.ParseTime(v); err == nil {
            return time.Until(t)
        }
    }
    return 0
}

// parseRateLimitInfo 解析厂商特定的限流头
func parseRateLimitInfo(headers http.Header, prefix string) *provider.RateLimitInfo {
    if prefix == "" {
        return nil
    }

    info := &provider.RateLimitInfo{}

    // Anthropic: anthropic-ratelimit-requests-remaining
    // OpenAI:    x-ratelimit-remaining-requests
    remaining := headers.Get(prefix + "-requests-remaining")
    if remaining == "" {
        remaining = headers.Get(prefix + "-remaining-requests")
    }
    if v, err := strconv.Atoi(remaining); err == nil {
        info.RemainingRequests = v
    }

    // 解析 token 余量
    tokenRemaining := headers.Get(prefix + "-tokens-remaining")
    if tokenRemaining == "" {
        tokenRemaining = headers.Get(prefix + "-remaining-tokens")
    }
    if v, err := strconv.Atoi(tokenRemaining); err == nil {
        info.RemainingTokens = v
    }

    return info
}
```

## HTTPError 类型

```go
// HTTPError 携带 HTTP 状态码和响应头，供重试策略判断
type HTTPError struct {
    StatusCode int
    Headers    http.Header
    Body       string
}

func (e *HTTPError) Error() string {
    return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// 所有 provider adapter 在非 200 时返回 HTTPError
// 使得 RetryPolicy 可以统一处理
```

## 与 Engine 的集成

```go
// Engine 中的使用方式
func (e *Engine) callWithRetry(ctx context.Context, req *engine.Request) (*engine.Response, error) {
    resp, err := e.retryPolicy.Execute(ctx, func(ctx context.Context) (*engine.Response, error) {
        return e.provider.Chat(ctx, req)
    })

    if err != nil {
        var httpErr *retry.HTTPError
        if errors.As(err, &httpErr) {
            // 413: 上下文过大 → 触发压缩后重试
            if httpErr.StatusCode == 413 {
                compressed := e.contextManager.ForceCompress(req.Messages)
                req.Messages = compressed
                return e.retryPolicy.Execute(ctx, func(ctx context.Context) (*engine.Response, error) {
                    return e.provider.Chat(ctx, req)
                })
            }
        }
        return nil, err
    }

    // max_output_tokens 截断恢复
    if resp.Truncated {
        return e.recoverTruncatedOutput(ctx, req, resp)
    }

    return resp, nil
}
```
