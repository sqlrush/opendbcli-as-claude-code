# OpenDB 产品主页设计规格

## 概述

OpenDB（Database CLI Agent）产品介绍主页，托管于 **opendbcli.org**。

**风格**: Cursor.com 风格 — 浅色暖白色系、排版驱动、大留白、终端 mockup 作为深色岛屿。
**目标受众**: DBA / 数据库工程师（主要）、CTO / 架构师（次要）。
**语言版本**: 中英双版本，导航栏语言切换。
**技术栈**: Next.js（SSR 模式，Node.js + PM2 + Nginx）。
**服务器**: 43.110.57.194，域名 opendbcli.org。
**GitHub 仓库**: https://github.com/sqlrush/opendbcli-as-claude-code
**内容素材**: https://github.com/sqlrush/opendbcli-as-claude-code/blob/main/README_zh.md

## 色彩系统（Cursor 浅色模式）

```
页面:
  --bg:             #f7f7f4        (暖白色背景)
  --bg-surface:     #efeee9        (略深的表面色)
  --bg-elevated:    #fff           (白色卡片浮层)
  --text-primary:   #26251e        (暖深色主文字)
  --text-secondary: rgba(38,37,30, 0.55)
  --text-tertiary:  rgba(38,37,30, 0.30)
  --border:         rgba(38,37,30, 0.08)
  --fill:           rgba(38,37,30, 0.04)
  --fill-hover:     rgba(38,37,30, 0.07)

终端（深色岛屿）:
  --terminal-bg:    #1a1a18
  --terminal-text:  #edecec
  --terminal-dim:   rgba(237,236,236, 0.35)
  --t-green:        #7ee787        (成功/提示符)
  --t-yellow:       #ffc61c        (进度/警告)
  --t-orange:       #ff6b35        (告警/根因)
  --t-blue:         #79c0ff        (信息)
```

## 字体

```
标题:    Inter / Noto Sans SC (800 weight), letter-spacing: -2.5px ~ -0.5px
正文:    Noto Sans SC / Inter (400/500 weight), 15-17px, line-height 1.6-1.7
终端:    JetBrains Mono (400/500/600), 13.5-14px, line-height 1.75-1.8
```

## 动画（Framer Motion）

- **页面元素**: fadeIn + translateY(2px)，Cursor 风格微动效
- **Hero 终端**: 打字机效果（命令 ~50ms/字符，输出 ~200ms/行）
- **功能板块**: 滚动触发淡入，终端 mockup 进入视口时上浮
- **Hero 动画循环**: 输入命令 → 输出逐行出现 → 根因+修复滑入 → 暂停 3s → 重置 → 循环

## 页面结构

### 板块 1: 置顶导航栏

```
[OpenDB logo] [功能] [文档] [GitHub] [博客]    [中文/EN] [快速开始]
```

- 置顶吸附，backdrop-filter 模糊效果
- 背景: rgba(247,247,244, 0.85)
- "快速开始" 按钮: 深色实心填充
- 语言切换: 中文 / EN 文字切换
- GitHub 链接指向: https://github.com/sqlrush/opendbcli-as-claude-code

### 板块 2: Hero 区

- 胶囊徽章: "像Claude Code一样交互的DBCLI Agent"（18px, weight 500, 圆角药丸）
- 主标题: "最少交互 / 最优诊断"（64px, weight 800, 两行）
- 英文版主标题: "Least Interaction, Optimal Diagnosis"
- 说明行: "Oracle · MySQL · PostgreSQL — 斜杠命令交互 / 原生SQL交互 / 自然语言交互"（17px, 单行）
- 快速安装区:
  - 左对齐标签行: **快速安装** macOS / Linux（标签可切换）
  - 深色终端命令块: `$ curl -fsSL https://www.opendbcli.org/install.sh | bash`（16px, 不换行, 点击复制）
  - GitHub 按钮: "GitHub ★ →" 链接到仓库
- 终端 mockup（深色岛屿）: /llm 诊断动画
  - 用户输入: `/llm 数据库为什么慢？`
  - 3 轮推理，进度指示器
  - 根因区块（橙色左边框）
  - 修复区块（绿色左边框）
  - 阴影: 0 24px 80px rgba(38,37,30, 0.12)

### 板块 3: 三种交互模式

- 标题: "三种模式，一个 Agent" / "Three Modes. One Agent."
- 3 张卡片网格（白色浮层背景，14px 圆角）:
  1. **斜杠命令** — 60+ 内置 Skill — `/health`, `/dbtop`, `/llm 诊断`
  2. **原生 SQL** — 保留你的习惯 — `SELECT count(*) FROM orders WHERE status='pending';`
  3. **自然语言** — 用大白话提问 — "为什么现在有这么多锁等待？"
- 每张卡片: 标签（大写）→ 标题 → 描述 → 深色终端代码块（14px 字体）

### 板块 4: 核心功能展示（左右交替布局）

5 个功能行，左右交替排列。每行: 文字描述侧 + 终端 mockup 侧（13.5px 字体）。

| # | 命令 | 标题 | 关键数据 | 终端展示内容 |
|---|------|------|----------|-------------|
| 1 | /health | 全维度健康检查 | 7 维度, 20+ 检查项, <5s | 7 维状态面板 ✓/⚠/✗ |
| 2 | /dbtop | 实时性能仪表盘 | 1s 刷新, 12+ 指标 | SGA/PGA/TPS/QPS, 等待事件柱状图, 活跃会话表 |
| 3 | /rule | 273 规则决策引擎 | 273 规则, 2.7s, 零依赖 | 根因 + 证据链 + 推荐 SQL |
| 4 | /llm | AI 深度诊断 | 最多 20 轮, 60+ Skill | 多轮函数调用 → 问题排名 → 修复 SQL |
| 5 | /sentinel | 7×24 异常检测 | 48 指标, 3σ, 全天候 | 异常告警 + 基线/当前/阈值 + 突发采集触发 |

布局规则:
- 奇数行: 左文字，右终端
- 偶数行: 左终端，右文字
- 关键数据用大号数字 + 小标签展示（如 "273" / "规则数"）
- 文字与终端间距: 56px
- 行间距: 88px

### 板块 5: 页脚

- 左侧: OpenDB logo + "最少交互，最优诊断。"
- 右侧: 文档 / GitHub / 问题反馈 / 开源协议

## 国际化策略

- Next.js i18n 路由: `/en/` 和 `/zh/` 路径
- 默认语言: 英文
- 导航栏语言切换器切换 locale
- 所有板块标题、描述翻译为对应语言
- 终端命令输出保持英文（产品真实输出）

## 响应式断点

- 桌面端: 最大宽度 1440px，内容区 1100px
- 平板 (≤1024px): 功能板块单列，模式卡片 2 列
- 手机 (≤640px): 全部单列，导航栏折叠为汉堡菜单

## 部署方案

- Next.js SSR，Node.js 18+
- PM2 进程管理
- Nginx 反向代理（443 → localhost:3000）
- 服务器: 43.110.57.194
- 域名: opendbcli.org（Let's Encrypt SSL）

## 参考 Mockup

完整交互式 mockup 文件:
`.superpowers/brainstorm/58902-1774960578/content/design-full-page-zh.html`
