# OpenDB Project Memory

## Project Identity
- **OpenDB**: 数据库专用 CLI 客户端（类似 Claude Code），AilinkDB 的执行层
- **AilinkDB**: AI 大脑（Qwen3.5-9B 微调），正在训练中，项目在 ~/ailinkdb
- **关系**: AilinkDB = 大脑（决策），OpenDB = 手脚（执行）

## Core Design Philosophy
- **最少字符，最大效果** — 让 DBA 输入更少的字符来达成复杂的任务
- 详见 [design-decisions.md](./design-decisions.md)

## Tech Stack Decisions
- **语言**: Go（单二进制，零依赖部署）
- **Oracle 驱动**: go-ora（纯 Go，无需 Oracle Client）
- **部署目标**: 拷贝文件 + 环境变量配置即可完成安装

## Architecture Decisions
- 详见 [design-decisions.md](./design-decisions.md)

## UI/UX Requirements (用户确认的需求)
- 详见 [ui-requirements.md](./ui-requirements.md)

## Debugging Lessons
- 详见 [debugging.md](./debugging.md)

## MCP 开发计划
- **目标**: 开发 opendb 成为 Qwen 3.5:9B 的 MCP 工具
- **方案**: HTTP API 基础 + MCP 薄包装，Skill 系统天然适配
- 详见 [mcp-architecture.md](./mcp-architecture.md)

## 模型选型
- **当前**: Qwen3.5-9B，<10B 级别综合能力最强（DBA 场景最优选择）
- **升级路径**: Qwen3.5-35B-A3B（同代 MoE，不要退回 Qwen3 系列）
- 详见 [model-selection.md](./model-selection.md)

## 架构原则
- **模型无关性**: 换模型只改配置不改代码，分层从弱到强渐进放开
- 详见 [architecture-model-agnostic.md](./architecture-model-agnostic.md)

## 诊断系统架构
- **三层分离**: 探针层(OpenDB必做) → 规则兜底(无LLM降级) → LLM链式推理(目标主路径)
- **核心原则**: 规则分类是兜底不是主路径，LLM 才是目标；喂给 LLM 的数据不带分类结论
- 详见 [architecture-diagnosis-layers.md](./architecture-diagnosis-layers.md)

## 场景方案
- **异常自动诊断**: 哨兵探测(3σ) → 爆发采集(200ms/帧) → 聚合分析 → Qwen 深挖 → 可执行建议
- **已实现 6 类 + 待实现 7 类**根因场景
- 详见 [scenario-spike-detection.md](./scenario-spike-detection.md)

## Sentinel 48 项检测策略
- **9 大类 48 项指标**: 会话/负载(10)、吞吐/速率(5)、等待/延迟(6)、内存/缓存(4)、存储/容量(5)、Redo/归档(4)、锁/并发(4)、SQL性能(4)、系统/模式(6)
- **9 种检测算法**: T1阈值、T2硬顶、T3趋势、T4加速度、T5复合、T6容量、T7偏移、T8回归、T9缺失
- **三层探针**: Fast(1s)、Medium(10s)、Slow(30s)
- 详见 [sentinel-48-metrics-strategy.md](./sentinel-48-metrics-strategy.md)

## /diag 交互设计（用户确认）
- **Claude Code 风格**: 逐行实时进度 + 非阻塞命令队列 + 异步执行
- **主命令**: `/diag`，`/diagnose` 降为别名
- 详见 [diag-interaction-design.md](./diag-interaction-design.md)

## 双模型诊断策略（核心需求）
- **9B（GuidedStrategy）**: LLM推理 + OpenDB辅助判断，OpenDB编排引导
- **27B+（AutonomousStrategy）**: 纯LLM推理链（10轮），失败后降级到GuidedStrategy
- 详见 [dual-model-strategy.md](./dual-model-strategy.md)

## Opus 4.6 测试模式（核心开发策略）
- **目标**: 用最强模型(Opus 4.6)验证 opendb skill/CLI 的完备性
- **方式**: Opus 4.6 请求转发器，opendb 像调 Ollama 一样调 Opus 4.6
- **约束**: Opus 只能用 opendb 的 skill/CLI，不能用 Claude Code 的工具
- **反馈循环**: 强模型暴露工具短板 → 补齐 → 再验证
- 详见 [testing-opus-forwarder.md](./testing-opus-forwarder.md)

## 智能体演进路线图
- **目标**: 99% 自动处理数据库问题
- **核心逻辑**: 先让 AI 看得全（P0）→ 再让 AI 动得了（P1）→ 最后让 AI 自己跑（P2/P3）
- 详见 [roadmap.md](./roadmap.md)

## LLM Engine V2（v0.9.27 已合入 main）
- **Engine V2 替换了 4 套老 agent loop**，统一通信引擎，支持 7 个模型
- **已验证**: Opus/Kimi/MiniMax/MiMo/DeepSeek/Gemini/GLM-5
- **关键机制**: streaming 自适应降级、截断恢复、工具调用记录每轮注入、证据表格格式
- 详见 [project-engine-v2-status.md](./project-engine-v2-status.md)

## 工具调用协议升级（已完成）
- **已实现**: Engine V2 通过 OpenAI Function Calling 原生协议，替换了文本模拟
- **支持**: OpenAI-compatible / Anthropic / Gemini / Ollama / opus-forwarder
- 历史: [tool-calling-upgrade.md](./tool-calling-upgrade.md)

## 告警描述 + 监控数据模板
- **告警描述**: 48个指标各有中文+English+单位+触发策略的描述模板
- **监控数据**: 9大场景各有定制模板，不用通用模板（如temp撑爆不展示等待事件/Top SQL）
- **右边框对齐**: 所有box的右边框必须对齐
- 详见 [alert-templates.md](./alert-templates.md)

## 措辞规范
- **根因描述禁止夸张用词**: 不用"风暴/瓶颈/抖动"，改用"xxx冲高"
- 详见 [feedback-wording.md](./feedback-wording.md)

## LLM诊断输出原则
- **Skill已补齐**: 40+ skill 覆盖监控/查询/管理/诊断/AI 五大类
- **可考虑切回 /命令优先**: skill 覆盖率已足够
- 详见 [feedback-llm-raw-sql.md](./feedback-llm-raw-sql.md)
- 历史: [feedback-llm-skill-first.md](./feedback-llm-skill-first.md)
- Skill清单: [project-skill-gaps.md](./project-skill-gaps.md)

## Skill 异步执行（已完成 v0.7.03）
- **已实现**: 所有 skill 命令非阻塞异步执行，结果异步回显到聊天窗口
- 详见 [project-async-skill-execution.md](./project-async-skill-execution.md)

## 交互优化专项（前置条件已满足，可启动）
- **前置**: 异步执行 ✅ + Skill Gap ✅ + 收尾项完成后启动
- **方式**: 在 Oracle 测试服务器上逐一运行所有功能，检查功能/贴图/美观度
- **输出**: 细化到每个命令的交互优化方案
- **原则**: 交互优化完成后再做新功能
- 详见 [project-interaction-optimization.md](./project-interaction-optimization.md)

## 版本管理
- **格式**: `vX.Y.ZZ`，后两位(patch)每次commit自动+1，前两位(major.minor)由用户决策
- 详见 [versioning-strategy.md](./versioning-strategy.md)

## Deployment
- **每次部署必须检查 SSH 隧道** — 详见 [feedback-deploy-tunnel.md](./feedback-deploy-tunnel.md)
- **测试服务器**: root@8.160.176.23, oracle 用户运行
- **启动命令**: `opendb`（在 PATH 中，实际路径 `/usr/local/bin/opendb`）
- **交叉编译**: `GOOS=linux GOARCH=amd64 go build -o opendb-linux ./cmd/opendb/`
- **部署步骤**: `scp opendb-linux root@8.160.176.23:/home/oracle/opendb && ssh root@8.160.176.23 "pkill opendb; sleep 1; cp /home/oracle/opendb /usr/local/bin/opendb"`
- **环境变量**: 无需额外设置（唯一可选: `OPENDB_CONFIG` 指定配置路径，默认 `~/.opendb/config.yaml`）
- **部署要求**: 每次部署后确认环境变量和配置一并到位，保证 `opendb` 命令可直接测试
- **Oracle 测试**: opendb_test / OpenDB_Test_2026, 127.0.0.1:1521/orcl
