# UI 输出美化 & 渲染架构优化方案

> 分支: `feat/ui-output-enhance`  
> 基线: v0.9.23  
> 创建: 2026-04-02  
> 更新: 2026-04-02（全量 bug 扫描后重新评估）

## 背景

OpenDB 的 /llm 诊断输出与 Claude Code 相比视觉差距较大。经分析，90% 的差距来自 **Markdown 渲染器质量**，而非渲染引擎（Yoga/帧diff/虚拟滚动对 OpenDB 的命令式场景不适用）。

同时，对 OpenDB 开发以来所有 UI 交互 bug 进行了全量扫描，识别出 **7 个根因**，部分 bug 虽已修复但修法是打补丁，存在架构层面的优化空间。

## 核心原则

- 不换框架（bubbletea 重写失败的教训）
- 每次改动 < 50 行，改完终端验证
- 在分支上开发，验证通过后合入 main
- glamour/chroma/termenv 是独立库，不引入 bubbletea

---

## 根因分析

### 根因 1: ANSI 序列散落 — 295 处裸写，无抽象层

**历史 bug（全部已修，但修法是打补丁）**:

| Bug | 表现 | 补丁式修法 |
|-----|------|-----------|
| 启动清屏 | `\033[?1049l` 退出 alt screen 有清屏副作用 | 改为只发 `\033[r\033[?25h` |
| 退出残留 `[` 字符 | `term.Restore` 之后再发 ANSI 导致转义不完整 | 调整发送顺序 |
| tablebrowser stdin 竞争 | 退出 alt screen 后 `\033[r` 没重置 scroll region | 加 reset 调用 |
| bufio 没 flush 导致 hang | `\033[6n` 查询光标发到缓冲区没刷出去 | 各处加 flush |
| 输入框跳动 | drawInputArea 清行+重绘 ANSI 行号算错 | 调 offset 计算 |

**根因**: 没有统一的终端操作抽象，每个调用点自己拼 `\033[...]`，出错了一个个补。

### 根因 2: 宽度计算分裂 — 3 套函数，4 处重复

**历史 bug**:

| Bug | 表现 |
|-----|------|
| 欢迎页表格错位 | 中英文混排宽度算错，多次 commit 调列宽 |
| Skills 表格错位 | ✓/— 等 ambiguous width 符号，换 Y/- 才修好 |
| Permission 面板右边框错位 | 面板宽度不够文字换行碎裂 |
| 安装向导 Logo 对位 | 猫耳朵对位差 1 格，手动微调 |
| dropdown 标签宽度 | CJK 字符宽度不一致 |

**根因**: `displayWidth()` / `visibleWidth()` / `stripAnsi()` 分散在 repl.go、diag_renderer.go、markdown.go，各自实现略有不同。

### 根因 3: Scroll Region 状态散落 — 6 个变量交织

**历史 bug**:

| Bug | 表现 |
|-----|------|
| BUG-002 resize 错位 | `r.cols/r.rows` 只读一次，运行中不更新 |
| alternate screen 退出脏数据 | 主屏恢复后内容区有旧分隔线 |
| dropdown 内容不恢复 | dropdown 关闭后下方内容丢失 |
| 星星动画行号漂移 | scroll 时 diagStarRow 没同步减 |
| dropdown 缩小时输入框跳动 | dropScrollOffset 和实际 dropdown 高度不同步 |

**涉及状态**: `scrollMode`、`contentRow`、`dropScrollOffset`、`diagStarRow`、`skillStarRow`、`drawnTopRow`、`drawnEndRow`

**根因**: 没有 scroll 状态的单一管理者，每个功能各自维护行号，scroll 时需要手动同步所有行号。

### 根因 4: Markdown 渲染简陋 — 手写 403 行

- 只支持 `##`→`■`、`###`→`▸`、`**`→bold、代码块、表格
- 无语法高亮、无 inline code 样式、无列表缩进
- 表格必须攒完才能渲染，流式兼容差
- action 代码块静默丢弃输出

### 根因 5: Dropdown 3 套渲染函数 — 重复且不一致

- loginPicker、llmPicker、rulePicker、completionDropdown 各自渲染
- 4 个 bypass flag (`loginPickerBypass`, `modelPickerBypass`, `llmPickerBypass`, `rulePickerBypass`)
- 无模糊搜索过滤能力（连接数 >10 时不好用）

### 根因 6: 大函数无法安全修改

| 函数 | 行数 | 风险 |
|------|------|------|
| `handleKeyInput()` | ~1000 行 | 加快捷键要精确定位 case 分支 |
| `drawInputArea()` | ~200 行 | 7 步状态机，重排序即崩 |
| `handleEnter()` | ~700 行 | 命令分发 + picker 逻辑混合 |

### 根因 7: 事件缓冲竞态 — 双 buffer + sleep 兜底

- `blockingUI` 期间用 `pendingAlerts` 缓冲，退出后 `time.Sleep(50ms)` 等 goroutine drain
- 50ms 可能不够（慢系统/SSH），事件可能丢失

---

## 改动项

### #1 glamour 替换手写 markdown.go [P0] — 解决根因 4

**现状**: `internal/ui/markdown.go` (403行) 手写解析

**目标**: 用 glamour 实现完整 Markdown 终端渲染
- 语法高亮代码块（chroma，支持 SQL/Go/Shell 等 200+ 语言）
- 表格对齐、列表缩进、标题层级
- 代码块有边框/背景色
- 行内代码有区分样式

**关键适配**: glamour 是一次性渲染，/llm 是流式输出。需要：
- 按段落/代码块边界攒 buffer
- 攒完一个完整 block 调 glamour 渲染
- 与现有 `diagStreamBuf` 逻辑对接

**改动范围**: `internal/ui/markdown.go` 重写 + `diag_renderer.go` 适配流式调用

**预估**: ~200 行

### #2 termenv 替换裸 ANSI [P0] — 解决根因 1

**现状**: 295 处手写 `\033[...]`，顺序/flush 错误频发

**目标**: 用 termenv（lipgloss 同一家，已兼容）封装所有终端操作

```go
// 现在: 裸 ANSI，每处都可能写错
fmt.Fprintf(w, "\033[%d;1H\033[2K", row)
fmt.Fprintf(w, "\033[?1049h")
fmt.Fprintf(w, "\033[1;%dr", maxRow)

// 改为 termenv:
output.MoveCursor(row, 1)
output.ClearLine()
output.AltScreen()
output.SetScrollableRegion(1, maxRow)
```

**改动方式**: 渐进替换，每次一个文件，每次 < 50 行
- 第 1 批: tablebrowser.go + dbtop.go（最独立）
- 第 2 批: diag_renderer.go + markdown.go
- 第 3 批: repl.go（最大，分多次）

**预估**: ~300 行（渐进）

### #3 抽取 termwidth 包 [P1] — 解决根因 2

**现状**: `displayWidth()` / `visibleWidth()` / `stripAnsi()` 在 3 个文件各有一份

**目标**: `internal/ui/termwidth/` 统一宽度计算

```go
package termwidth

func StringWidth(s string) int           // strip ANSI → runewidth
func Truncate(s string, max int) string  // 保留 ANSI 的截断
func PadRight(s string, width int) string // 按显示宽度右填充
func WrapLine(s string, width int) []string // 保留 ANSI 的换行
```

**预估**: ~100 行新代码 + 删除 3 处重复

### #4 ScreenManager + SIGWINCH [P1] — 解决根因 3

**现状**: 6 个 scroll 相关变量散落在 REPL struct，无 SIGWINCH 监听

**目标**: 抽取 `ScreenManager` 统一管理终端状态

```go
type ScreenManager struct {
    w            *bufio.Writer
    rows, cols   int
    scrollTop    int
    scrollBottom int
    contentRow   int
    trackedRows  map[string]*int  // "diagStar" → &diagStarRow
    sigwinchCh   chan os.Signal
}

func (sm *ScreenManager) ScrollUp(n int)              // 自动调整所有注册行号
func (sm *ScreenManager) OnResize(fn func(rows, cols int))  // SIGWINCH 回调
func (sm *ScreenManager) EnterAltScreen()
func (sm *ScreenManager) LeaveAltScreen(restoreFn func())   // 退出后自动回调重绘
```

**收益**:
- BUG-002 (resize) 根治
- 星星行号漂移消失（trackedRows 自动同步）
- alternate screen 退出重绘统一化
- dropdown scroll offset 由 ScreenManager 管理

**预估**: ~150 行

### #5 统一 Picker 组件 [P2] — 解决根因 5

**现状**: 3 套渲染函数 + 4 个 bypass flag

**目标**: 统一 `Picker` 组件，支持模糊搜索

```go
type Picker struct {
    items       []PickerItem
    selectedIdx int
    scrollOff   int
    maxVisible  int
    query       string  // 模糊搜索
    filterFn    func(query string, items []PickerItem) []PickerItem
}

func (p *Picker) Render(sm *ScreenManager, startRow int) int
func (p *Picker) HandleKey(ev KeyEvent) PickerAction
```

**预估**: ~200 行

### #6 流式输出减闪烁 [P2] — 解决根因 4

**现状**: 每次 stream chunk 清行+重写 (`\033[2K\r%s`)

**目标**: 追加模式，只写新增部分，换行时才清理 partial 状态

**改动范围**: `diag_renderer.go` 中 `DiagPhaseAIStream` 处理逻辑

**预估**: ~20 行

### #7 lipgloss box 输出分区 [P2] — 解决根因 4

**现状**: 所有输出逐行平铺，无视觉层次

**目标**: 用 lipgloss 给关键输出区域加视觉结构
- 诊断结论: 带圆角边框的 box
- SQL 建议: 左竖线 + 缩进
- 规则引擎结果: 与 LLM 结果视觉区分

**预估**: 每处 ~10 行

### #8 拆文件（纯搬运） [P2] — 解决根因 6

按已有的 `project-ui-refactor-plan.md` 执行：

```
repl.go (3197行) →
  repl.go            (~500行) REPL struct + Run() + handleEnter()
  repl_input.go      (~400行) handleKeyInput() + 补全
  repl_render.go     (~300行) drawInputArea() + clear
  repl_output.go     (~300行) writeOutputLine() + scroll + viewport
  repl_async.go      (~400行) diag/skill runner + queue
  repl_wizard.go     (~600行) connWizard + modelWizard
  repl_sqlrewrite.go (~200行) SQL rewrite
```

纯机械搬运，不改任何逻辑，每拆一个文件就 `go build` + 手动验证。

### #9 事件 drain 替代 sleep [P3] — 解决根因 7

**现状**: `time.Sleep(50ms)` 后 flush

**目标**: channel drain 模式

```go
// 改为: drain 所有排队事件
for {
    select {
    case alert := <-r.sentinelSkill.AlertCh():
        renderAlert(alert)
    case sched := <-r.schedulerSkill.EventCh():
        renderSchedulerEvent(sched)
    default:
        return // 队列清空，退出
    }
}
```

**预估**: ~20 行

### #10 UI 自动化测试: pty + midterm + golden file [P1]

**背景**: 当前 UI 测试依赖人工终端验证或 tmux capture-pane 脚本，无法自动检测贴图错误、表格错位、resize 后渲染异常。业界调研结论：VHS 本质是录屏工具（需要 Chrome + ffmpeg），不适合做自动化测试；Go 原生方案更轻量可靠。

**方案**: `creack/pty`（PTY 中启动 opendb）+ `vito/midterm`（内存终端模拟器解析 ANSI）+ `golden file` 对比

**工作原理**:
1. pty 启动 opendb — 和人打开终端一模一样，可连真实数据库
2. midterm 解析所有 ANSI 序列（scroll region、alt screen、颜色）— 还原人看到的画面
3. 拿到渲染后的屏幕文本，做 golden file diff 或精确坐标断言

**封装 TestTerminal helper**:

```go
// internal/ui/uitest/helpers.go

type TestTerminal struct {
    pty  *os.File
    term *midterm.Terminal
    cmd  *exec.Cmd
}

func NewTestTerminal(t *testing.T, rows, cols int, args ...string) *TestTerminal
func (tt *TestTerminal) SendLine(s string)              // 发送命令
func (tt *TestTerminal) SendKey(key byte)               // 发送特殊键
func (tt *TestTerminal) WaitFor(regex string, timeout time.Duration) error
func (tt *TestTerminal) Screen() string                 // 当前画面（全屏文本）
func (tt *TestTerminal) CellAt(row, col int) rune       // 指定位置字符
func (tt *TestTerminal) RowText(row int) string          // 指定行文本
func (tt *TestTerminal) Resize(rows, cols int)           // resize + SIGWINCH
func (tt *TestTerminal) RequireGolden(t *testing.T)      // golden file 对比
func (tt *TestTerminal) Close()
```

**测试用例组织**（按用户场景，非按功能文件）:

```
internal/ui/uitest/
  helpers.go                         # TestTerminal 封装
  golden_test.go                     # golden file 场景
  alignment_test.go                  # 对齐断言场景
  testdata/
    TestWelcomePage.golden           # 欢迎页画面
    TestHealthOutput.golden          # /health 输出
    TestDbtopFirstFrame.golden       # dbtop 首帧
    TestLlmDiagnosis.golden          # /llm 诊断输出
    TestDropdownPicker.golden        # /login 下拉列表
```

**示例测试**:

```go
func TestHealthRightBorder(t *testing.T) {
    tt := NewTestTerminal(t, 40, 120, "-c", "oratest")
    defer tt.Close()

    tt.SendLine("/health")
    tt.WaitFor("Health Check", 5*time.Second)
    tt.RequireGolden(t)  // 对比 testdata/TestHealthRightBorder.golden
}

func TestResizeNoCrash(t *testing.T) {
    tt := NewTestTerminal(t, 40, 120, "-c", "oratest")
    defer tt.Close()

    tt.SendLine("/dbtop")
    tt.WaitFor("TPS", 3*time.Second)
    tt.Resize(30, 80)  // 模拟终端缩小
    time.Sleep(1 * time.Second)

    for row := 0; row < 30; row++ {
        line := tt.RowText(row)
        if runewidth.StringWidth(line) > 80 {
            t.Errorf("resize 后行 %d 溢出: 宽度 %d", row, runewidth.StringWidth(line))
        }
    }
}
```

**VHS 角色**: 仅用于生成 README demo GIF（`docs/demos/*.tape`），不用于自动化测试。

**预估**: ~200 行（helpers.go）+ 每个场景 ~20 行测试

---

## 执行顺序总览

| 顺序 | 改动 | 根因 | 改动量 | 优先级 | 效果 |
|------|------|------|--------|--------|------|
| **1** | **UI 自动化测试 (pty+midterm)** | 测试缺失 | ~200行 | **P0** | **安全网 — 后续所有改动都能自动验证** |
| 2 | glamour 替换 markdown.go | 根因4 | ~200行 | **P0** | LLM 输出视觉质量飞跃 |
| 3 | termenv 替换裸 ANSI | 根因1 | ~300行（渐进） | **P0** | 消除一整类序列错误 |
| 4 | 抽取 termwidth 包 | 根因2 | ~100行 | **P1** | 宽度计算统一，表格对齐根治 |
| 5 | ScreenManager + SIGWINCH | 根因3 | ~150行 | **P1** | resize 根治 + scroll 状态收敛 |
| 6 | 统一 Picker 组件 | 根因5 | ~200行 | **P2** | 消除 4 套 dropdown + 4 个 bypass flag |
| 7 | 流式输出减闪烁 | 根因4 | ~20行 | **P2** | 追加模式替代清行重写 |
| 8 | lipgloss box 分区 | 根因4 | 每处~10行 | **P2** | 输出有视觉层次 |
| 9 | 拆文件（纯搬运） | 根因6 | 0 逻辑改动 | **P2** | 可维护性 |
| 10 | 事件 drain 替代 sleep | 根因7 | ~20行 | **P3** | 消除竞态 |

## 验收标准

1. /llm 诊断输出：代码块有语法高亮+边框，表格对齐，标题有层次
2. 流式输出过程中无明显闪烁
3. 诊断结论、SQL 建议、规则结果视觉上有明确区分
4. 终端 resize 后 dbtop/REPL 自动适配，无错位
5. 所有现有交互功能不受影响（/login, /dbtop, picker, 补全等）
6. 真实 macOS 终端验证通过（不只是 tmux）
7. UI 自动化测试覆盖核心场景（欢迎页、/health、/dbtop、/llm、picker、resize）

## 依赖引入

| 库 | 用途 | 是否引入 bubbletea |
|----|------|--------------------|
| glamour | Markdown 终端渲染 | 否（独立库） |
| chroma | 语法高亮 | 否（glamour 内含） |
| termenv | 终端操作封装 | 否（独立库，lipgloss 底层依赖） |
| lipgloss | 已有依赖，增加 box 用法 | N/A |
| creack/pty | PTY 接口（测试用） | 否 |
| vito/midterm | 内存终端模拟器（测试用） | 否 |
| charmbracelet/x/exp/golden | golden file 对比（测试用） | 否 |

## 风险

1. glamour 的流式适配可能比预期复杂 → 备选：增强手写 markdown.go 而非整体替换
2. glamour 输出宽度可能与现有 visibleWidth() 计算不一致 → 需要验证右边框对齐
3. 新依赖增加二进制体积 → glamour + chroma 约增加 5-10MB，可接受
4. termenv 渐进替换期间存在"两套写法并存"的过渡期 → 按文件替换，避免混用
5. ScreenManager 抽取涉及 REPL struct 核心字段 → 需要逐步迁移，不能一步到位
6. midterm 对 OpenDB 使用的部分 ANSI 序列（如 DECSTBM scroll region）支持程度需验证
