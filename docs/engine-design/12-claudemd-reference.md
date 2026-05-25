# 12 — CLAUDE.md 机制参考 + OpenDB 已创建的 CLAUDE.md

## 一、Claude Code 的 CLAUDE.md 完整机制

### 4 级优先级

```
级别 1 — Managed（组织级，最低优先级）
  由管理员通过 Anthropic 后台设置
  → 用户看不到文件，通过 API 下发
  → 用于企业统一策略（如"禁止操作生产数据库"）

级别 2 — User（用户级）
  ~/.claude/CLAUDE.md
  → 用户全局规则，影响所有项目
  → 不提交 git，个人偏好
  → 例: "我喜欢简洁风格""用中文回答"

级别 3 — Project（项目级）
  {项目根目录}/CLAUDE.md
  → 提交到 git，团队共享
  → 所有用 Claude Code 打开此项目的人都会遵守
  → 例: "用 Go 写""测试覆盖率 80%""commit 用 conventional format"

级别 4 — Local（本地私有，最高优先级）
  {项目根目录}/.claude/CLAUDE.md
  → 不提交 git（.claude/ 在 .gitignore 中）
  → 只有你自己看到，个人私有覆盖
  → 例: "我在做快速原型，跳过 lint""优先关注 internal/llm/ 目录"
```

### Project CLAUDE.md vs Local .claude/CLAUDE.md 的区别

```
项目/CLAUDE.md                    项目/.claude/CLAUDE.md
─────────────                     ──────────────────────
放在项目根目录                      放在 .claude 隐藏目录
提交到 git ✅                      不提交到 git ❌
团队所有人都看到                     只有你自己看到
团队共享的项目规则                   个人的私有覆盖
优先级: 级别 3                     优先级: 级别 4（更高）

冲突时: .claude/CLAUDE.md 覆盖 CLAUDE.md
原因: 私有规则 = "我知道团队规则但我有特殊需要"
```

### 加载时机和注入方式

```
在 20 步管线中的位置:

  第 15 步: prependUserContext()
  → 加载 4 级 CLAUDE.md，合并为一个字符串
  → 包裹在 <system-reminder> 标签中
  → 作为 IsMeta=true 的隐藏消息插入消息列表最前面
  → 模型看到，用户看不到

源码:
  加载: context.ts:getUserContext() → claudemd.ts:getMemoryFiles()
  注入: api.ts:prependUserContext()

注入时附带声明:
  "IMPORTANT: These instructions OVERRIDE any default behavior
   and you MUST follow them exactly as written."
  → 确保模型把 CLAUDE.md 规则视为最高优先级
```

### 支持 @include

```markdown
# CLAUDE.md 中可以引用其他文件

@docs/coding-standards.md
@.github/CONTRIBUTING.md

→ 被引用的文件内容也会被加载和注入
→ 避免 CLAUDE.md 过长，分散到多个文件
```

---

## 二、OpenDB 已创建的 CLAUDE.md

### 文件位置

```
~/opendb/CLAUDE.md                 ← 已创建 ✅ (项目级，提交到 git)
~/opendb/.claude/CLAUDE.md         ← 未创建（个人私有，按需创建）
~/opendb/.claude/settings.local.json ← 已有（Claude Code 本地设置）
```

### 内容来源

```
CLAUDE.md (~1500字) 从以下 .memory/ 文件中提炼:

.memory/design-decisions.md      → 项目概述、技术栈、交互模式
.memory/architecture-*.md        → 模型无关性、三层分离、安全分级
.memory/feedback-wording.md      → 措辞规范（禁用夸张词）
.memory/feedback-llm-raw-sql.md  → LLM 输出原生 SQL
.memory/feedback-testing.md      → 测试要求（人视角验证）
.memory/feedback-ruleengine.md   → 规则引擎流程（数据→代码）
.memory/feedback-deploy-tunnel.md → 部署检查隧道
用户全局 coding-style 规则       → 不可变、文件组织、错误处理
用户全局 git-workflow 规则       → commit 规范
```

### CLAUDE.md 和 .memory/ 的区别

```
.memory/MEMORY.md (6700字，29个文件)
  → 详细的项目历史、决策过程、演进路线图
  → Claude Code memory 系统用，跨会话记忆
  → 包含很多过程性信息（bug 记录、版本进度等）
  → 会被自动加载到会话上下文（如果启用 auto memory）

CLAUDE.md (~1500字)
  → 精炼的项目规则，只保留"必须遵守"的部分
  → 每次 Claude Code 会话自动加载到系统提示（第 15 步）
  → 任何人用 Claude Code 打开 opendb 项目都会遵循
  → 不包含过程性信息，只有规则
```

### 已创建的 CLAUDE.md 核心内容

```
~/opendb/CLAUDE.md 包含:

1. 项目概述 — OpenDB 是什么、Go 语言、支持的数据库
2. 技术栈 — go-ora, bubbletea, OpenAI 兼容 LLM
3. 代码规范 — 不可变数据、文件 200-400 行、函数 <50 行
4. 架构原则 — 模型无关性、诊断三层分离、安全分级
5. 措辞规范 — "冲高"不用"风暴/瓶颈/抖动"
6. LLM 输出规则 — 原生 SQL、禁止编造
7. 规则引擎流程 — 数据→代码，不直接手写
8. 测试要求 — 人视角验证真实输出
9. 部署 — 交叉编译、检查隧道
10. Commit 规范 — conventional format
```

---

## 三、对标关系：CLAUDE.md → OpenDB 用户自定义规则

```
Claude Code 的分层:                     OpenDB 新 Engine 的分层:
───────────────                         ──────────────────────

CLAUDE.md (给 Claude Code 开发者看的)    用户自定义规则 (给 OpenDB DBA 看的)
────────────────────────────            ────────────────────────────────

级别 2: ~/.claude/CLAUDE.md              级别 1: ~/.opendb/rules/*.md
  → 开发者个人全局偏好                      → DBA 个人全局诊断规范

级别 3: 项目/CLAUDE.md                   级别 2: ~/.opendb/rules/{实例名}/*.md
  → 团队共享的项目规则                      → 特定数据库实例的规则

级别 4: 项目/.claude/CLAUDE.md           级别 3: /diag 会话参数
  → 个人私有覆盖                           → 临时规则

注入位置: 第 15 步系统提示               注入位置: ContextBuilder Block 3
注入方式: <system-reminder> isMeta      注入方式: SystemPromptBlock
优先级声明: "OVERRIDE any default"      声明: "与默认冲突时以用户规则为准"
```

两个系统服务不同人群:
- CLAUDE.md → 给**开发者**定制 Claude Code 的行为
- 用户自定义规则 → 给**DBA**定制 OpenDB AI 诊断的行为

但机制完全相同：**用户写文本文件 → 框架加载 → 注入到系统提示 → 模型遵守**。
