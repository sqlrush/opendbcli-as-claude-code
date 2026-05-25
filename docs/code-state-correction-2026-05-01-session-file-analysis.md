# 修正：session 文件分析方法的认知偏差

日期：2026-05-01
触发场景：用户报告"GaussDB·(gausstest) 当前数据库存在什么问题"诊断输出空白，让我看 session 文件取证

## 当时错误的认知

- 第一轮分析就直接断言"session 文件里 #10 是 5m14s 那次成功诊断的内容，失败那次内容已永久丢失"
- 没有用具体数字交叉比对就下结论
- 给用户的方法论里只讲了"session 是 atomic 整文件覆写"，没讲"如何反向追溯 session 来源"

## 实际状态（2026-05-01 重新比对后）

`~/.dbaa/sessions/gausstest/gausstest:6d4d9270-...jsonl` mtime 5月1 00:06：

- msg #0 user 文本: `"用户问题: 当前数据库存在什么问题..."`（不是 5m14s 那次的"VACUUM 哪些表"）
- msg #10 assistant content=3731，提到 `bench_og_hot WHERE uid=? — 58,133 次`
- 与用户截图的 5m14s 诊断（提到 27,713 次）数字不一致
- 与失败的 3m33s 诊断（屏幕空白看不到）也无法直接对照
- 结论：这个 session **不是用户截图的两次中的任何一次**，是另一次没截图的"当前数据库存在什么问题"调用

## 关键证据

1. session 文件里的具体数字 vs 用户截图里的具体数字不匹配（58,133 ≠ 27,713）
2. msg 数量 11 / 4-turn 与 3m33s 截图的"8 rounds / 25+ tool"不匹配
3. session 文件 mtime 5月1 00:06，时间上能对应到任何午夜附近的调用

## 学到的 session 文件分析方法论

### 反向追溯 session 来源的步骤

1. 看 msg #0 的 user 文本 — 确认是哪次问题
2. 看 msg 总数和 turn 分布 — 估算调用规模
3. 看最终 assistant content 里的具体数字（次数、表名、行数）— 与用户截图交叉验证
4. 比对 mtime 与用户描述的时间线

任意一项与用户截图数字对不上 → **不是同一次调用**，session 已被后续调用覆盖。

### 思考模式行为指纹（从 session 文件读出）

- 中间轮 assistant：content=0 + thinking>0 + tool_calls>0
- 最终轮 assistant：content>0 + thinking>0 + tool_calls=0

**bug 触发条件**：最终轮也走成 "content=0 + thinking>0 + tool_calls=0"，
触发 engine.go:259 done 分支但 OnStream 不调用（engine.go:265 `resp.Content != ""` 为 false），
用户屏幕空白；同时 saveSession 把空 content 持久化，下次成功调用又覆盖。
间歇性 bug 与思考模式"偶发只想不答"的概率分布吻合。

### 取证盲点

session 文件**不是审计日志**：

- atomic 整文件覆写
- 24h 内 resume 同一 SessionID，新调用 messages 累积；但每次保存都用当前完整 msgs 数组
- 失败的调用如果触发了 saveSession（engine.go done/error 分支都会调），会留下"空 content"消息；但被后续成功调用覆盖后就丢了

正确取证流程：bug 触发后**立即** `cp ~/.dbaa/sessions/<instance>/*.jsonl /tmp/snapshot-$(date +%s).jsonl`，
再做任何后续操作。

## 反思教训

1. CLAUDE.md "架构与功能问答规范"明确要求"基于扫描结果回答 + 引用具体文件路径/行号/函数名作为证据"。我第一轮分析就给了结论，没用具体数字交叉验证，违反了这条规则
2. 字段长度（content_len / thinking_len）不等于内容来源。下结论前必须读实际文本
3. 修正认知不应该只在心里改，要落到 docs，避免下次重蹈覆辙
