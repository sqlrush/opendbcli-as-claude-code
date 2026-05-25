---
name: tool-calling-upgrade
description: 工具调用协议升级计划 — 当前文本模拟，未来支持原生 function calling + MCP 多模式
type: project
---

## LLM 工具调用协议升级（待做，优先级低）

### 当前状态
- opendb 使用 **文本模拟** 方式：模型在回复中输出 ```action {"skill":"x","args":"y"} ``` 文本块
- PromptLoop 用正则 (`actionBlockRe`) 解析，执行 skill，结果拼入下一轮 user message
- 原因：Qwen3.5 蒸馏模型不支持原生 function calling

### 问题
- 依赖模型严格遵循文本格式，弱模型可能输出错误格式
- 正则解析不如结构化协议可靠
- 工具描述写在 system prompt 里，占用 token

### 升级目标：多模式支持
用户要求支持多种工具调用模式，根据模型能力自动选择：

1. **文本模拟模式（当前）** — 适用于蒸馏小模型（Qwen3.5-9B）
2. **OpenAI Function Calling** — 适用于支持 tool_calls 的模型
3. **Anthropic Tool Use** — 适用于 Claude 系列（通过 opus-forwarder）
4. **MCP 协议** — 长期目标，opendb 作为 MCP server 暴露 skill

### 优先级
**当前不做。** 先把 skill 和 CLI 功能打磨好，工具调用协议是后续优化项。

### 实现思路（记录备用）
- LLM Provider 接口增加 `SupportsToolCalling() bool` 方法
- 支持原生 tool calling 的模型走 AgentLoop（结构化 tool_calls）
- 不支持的走 PromptLoop（文本模拟）
- opus-forwarder 可扩展为支持 Anthropic tools 参数透传
