---
name: testing-opus-forwarder
description: 核心测试策略 — 用 Opus 4.6 作为 LLM 验证 opendb 的 skill/CLI 能力，通过请求转发器桥接
type: project
---

## Opus 4.6 测试模式（核心开发策略）

### 核心思路
- 先打磨 opendb 的 skill 和 CLI 功能，而不是急着对接小模型
- 引入 **Opus 4.6 请求转发器**：opendb → 转发器 → claude CLI (Opus 4.6)
- opendb 配置转发器地址和端口，像调用 Ollama 一样调用 Opus 4.6
- 模拟故障后，opendb 调用 Opus 4.6 来排查问题

### 关键约束（最重要！）
- **Opus 4.6 必须只使用 opendb 上的 skill 和 CLI 来排查和解决问题**
- **绝对不能使用 Claude Code 的 skill 和 CLI**
- opus-forwarder 用 `--tools ""` 禁用 Claude Code 所有工具，确保纯文本输出
- 如果 opendb 的 skill/CLI 能力不够 → **记录缺失能力，测试后复盘**
- 这形成一个反馈循环：最强模型暴露工具短板 → 补齐短板

### 测试纪律
- 每次测试记录 Opus 4.6 尝试调用了哪些 skill
- 如果 Opus 想做某件事但 opendb 没有对应 skill → 记录为能力缺口
- 测试结束后统一复盘，确定下一步开发优先级

### 为什么这样做
- Opus 4.6 是当前最强推理模型，如果它用 opendb 的工具都解决不了问题，说明工具本身有缺陷
- 先用强模型验证工具完备性，再让弱模型（Qwen）在完备的工具上工作
- 避免分不清"是模型不行还是工具不行"的问题

### 架构（当前实现）
```
opendb (Oracle 测试机)
  → HTTP POST :11434 (OpenAI 兼容格式)
  → [SSH 反向隧道]
  → opus-forwarder (Mac, :11435)
  → claude -p --tools "" --output-format json (Claude CLI)
  → Opus 4.6 推理（纯文本，无工具）
  → 响应转换回 OpenAI 格式
  → opendb 收到响应，解析 action 块
  → 执行 skill，把结果喂回下一轮
```

### 部署位置
- **转发器部署在本地 Mac** — 通过 Claude Code 订阅（$200/月），无需 API Key
- Oracle 测试机通过 SSH 反向隧道访问 Mac:11435
- opendb config 不改，还是 `base_url: http://localhost:11434`

### opendb 输出原则
- opendb 只输出监控数据（触发条件、等待事件、Top SQL、阻塞链）
- **不输出任何判断或建议** — 根因分析、修复建议全部由 LLM 给出
- 如果 LLM 失败，只显示监控数据 + "AI 分析失败"
