# Charm / Crush TUI 分析与对 OpenDB 的借鉴

- 日期：2026-04-17
- 关联：[hermes-comparison-2026-04-17.md](hermes-comparison-2026-04-17.md)
- 配套待办：[plans/2026-04-17-hermes-inspired-improvements.md](plans/2026-04-17-hermes-inspired-improvements.md)（新增 T16–T21）

---

## 1. Charm 公司概况

| 项 | 信息 |
|---|---|
| 公司 | Charm, Inc.（品牌 charmbracelet） |
| 总部 | 美国纽约 |
| 成立 | 2019 年 |
| 员工数 | 约 8 人 |
| 创始人 | Toby Padilla（前 Apple / Last.fm / TweetDeck）、Christian Rocha（CEO） |
| 开源协议 | 大部分 MIT，部分项目 FSL |

**杠杆率：** 8 人团队做出了 Go 终端生态的事实标准。bubbletea 32k+ star，lipgloss 9k+ star，几乎所有 Go 写的 TUI/CLI 工具都在 import 他们至少一个库。

## 2. Charm 产品矩阵（与 opendb 相关的）

| 库 | 作用 | opendb 现状 |
|---|---|---|
| **bubbletea** | Elm 架构 TUI 框架（Model/Update/View） | **CLAUDE.md 声称在用，实际代码 0 引用** |
| **lipgloss** | 样式/颜色/布局 | 已用（`internal/ui/repl.go`） |
| **bubbles** | 组件库（输入框、列表、表格、进度条、viewport） | 未用 |
| **glamour** | Markdown 终端渲染 | 未用（自写了 `markdown.go` 606 行） |
| **gum** | shell 能用的 TUI 小工具 | N/A（属于终端用户工具） |
| **huh** | 交互表单/向导 | 未用（自写 `connwizard.go` 750、`modelwizard.go` 679） |
| **wish** | SSH 服务端框架 | 未用（但 opendb 未来做 SSH 远程会用到） |
| **crush** | Go 写的 AI coding agent CLI | 直接可参考的同栈竞品 |

## 3. Crush（同栈竞品）的关键特性

- **LLM 中途可切换且保留上下文** —— 牺牲 prompt cache hit 换灵活度（与 hermes 的"cache 不可破"相反）
- **MCP 三种 transport** —— stdio / HTTP / SSE（比 hermes 多一个 SSE）
- **LSP 作为上下文源** —— agent 感知类型/定义
- **多项目 × 多会话 × 多上下文**
- **基于 bubbletea + bubbles + lipgloss 全家桶**
- **FSL 协议**（非纯 OSI 开源，但源码可读）

## 4. OpenDB UI 当前状态诊断

```
internal/ui/*.go 共 9901 行
最大文件：repl.go          1951 行  ← 严重超 CLAUDE.md 800 行上限
         connwizard.go      750 行  ← 应由 huh 替代
         modelwizard.go     679 行  ← 应由 huh 替代
         markdown.go        606 行  ← 应由 glamour 替代
         diag_renderer.go   501 行
         repl_input.go      472 行
```

**核心矛盾：** `CLAUDE.md` 的技术栈声明 "TUI: bubbletea + lipgloss"，但代码实际 **0 处 import bubbletea**。所有交互是基于 `bufio.Scanner` + 原生 ANSI 控制码 + lipgloss 样式的手工拼装，导致：

1. **状态机散乱在全局变量**里，`repl.go` 无法拆分
2. **无组件化**，connwizard / modelwizard / picker / dropdown 各自为政，复用 = 0
3. **窗口大小变化、Ctrl+Z 挂起恢复、输入法、多行粘贴**等边界场景自己处理，易漏
4. **测试困难**：需要伪造终端而不是给 `tea.Program` 发消息
5. **和 `dbtop.go`、`alert_renderer.go`、`diag_renderer.go` 的刷新节奏冲突**——各组件自己 print 容易打架

## 5. Crush 的 UI 目录结构（可直接参考）

```
crush/
├── internal/app/          # tea.Model 顶层聚合
├── internal/tui/
│   ├── components/        # 每个小组件一个文件，<300 行
│   │   ├── chat/
│   │   ├── commands/
│   │   ├── completions/   # 自动补全
│   │   ├── dialogs/       # 各种弹窗
│   │   ├── files/         # 文件浏览
│   │   ├── header/
│   │   ├── status/
│   │   └── ...
│   ├── page/              # 页面级容器（chat、splash、settings）
│   ├── styles/            # lipgloss 样式集中
│   └── theme/             # 皮肤/主题
└── ...
```

**关键点：** `components/` 每个子目录是**独立 `tea.Model`**，父 Model 持有子 Model 列表，通过 Update 转发消息。opendb 的 `connpicker.go`、`picker.go`、`repl_dropdown.go`、`tablebrowser.go`、`skill_runner.go` 恰好是候选拆分对象。

## 6. 对 opendb 的三条结论

1. **bubbletea 迁移不可避免** —— `CLAUDE.md` 已写入技术栈，实际未用。继续手工拼 ANSI 短期快，长期维护成本指数上升。建议作为 P1 主线。
2. **huh 替代 wizard**（connwizard/modelwizard 合计 1429 行）可以直接减 80%+ 代码量。优先 P1。
3. **glamour 替代 markdown.go**（606 行）可以直接用 Charm 的 Markdown 终端渲染，并自动获得代码高亮、表格、任务列表支持。优先 P1。

> **小团队的设计哲学值得抄**：Charm 8 人能支撑整个生态，核心做法是 **每一层都做成独立库**（不做大而全框架）+ **dogfooding**（自家工具基于自家库）+ **一致的设计语言**。opendb 的 `internal/ui/` 完全可以采用同样的组件化思路。
