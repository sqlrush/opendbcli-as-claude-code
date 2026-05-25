# /trace Skill 设计文档

> OS 堆栈采集 + 火焰图生成 + 数据库源码级性能分析

## 1. 目标

在数据库宿主机上采集数据库进程的 OS 级堆栈信息，生成火焰图，并结合数据库源码由 LLM 分析性能瓶颈、等待和阻塞的根因，给出源码层面的优化方案。

### 非目标

- 不做远程 SSH 采集（仅宿主机本地）
- 不做长时间 profiling（采集窗口 3-5 秒）
- 不预置源码知识到 system prompt

## 2. 核心流程

```
用户 /trace  或  Sentinel CPU 冲高  或  LLM 主动 tool_call
                        ↓
              ┌─────────────────┐
              │  宿主机检测      │  ps 查数据库进程 + 检查连接 host
              └────────┬────────┘
                  不在 → 返回"不可用，需部署在数据库宿主机"
                  在 ↓
              ┌─────────────────┐
              │  TraceCollector  │  perf record -g -p <pid> -- sleep 3
              │  .Capture()     │  perf script → 折叠栈帧 → SVG
              └────────┬────────┘
                       ↓
              TraceResult {
                Collapsed  string   // 折叠栈帧文本（给 LLM）
                SVGPath    string   // 火焰图 SVG 路径（给人）
                TopFuncs   []Func   // Top N 热点函数
                PID        int
                Duration   int
                Timestamp  time.Time
              }
                       ↓
              ┌─────────────────┐
              │  SourceLookup   │  按 TopFuncs 在源码目录 grep 函数定义
              │  .Grep()        │  返回函数签名 + 关键逻辑片段
              └────────┬────────┘
                       ↓
              ┌─────────────────┐
              │  LLM 分析       │  折叠栈帧 + 源码片段 → prompt → 分析报告
              └────────┬────────┘
                       ↓
              终端输出：Top 热点函数表格 + SVG 路径 + LLM 分析结论
```

## 3. 架构组件

### 3.1 OS 命令执行层

```
internal/os/exec.go
```

白名单 + 超时的 OS 命令执行器。所有 OS 类 skill 的统一安全入口。

```go
// 白名单
var allowedCmds = map[string]bool{
    "perf": true, "ps": true, "pstack": true,
}

// 执行
func Run(ctx context.Context, name string, args ...string) ([]byte, error)
```

- 不在白名单的命令直接拒绝
- 默认 30 秒超时
- 未来扩展 `iostat`、`sar`、`df` 等只需往白名单加一行
- 文件 < 50 行，不做框架

### 3.2 TraceCollector（采集层）

```
internal/trace/collector.go
```

无状态，纯工具。三个调用方共用同一个 Collector：

| 调用方 | 场景 |
|--------|------|
| `/trace` skill | 用户手动执行 |
| Sentinel | CPU 冲高时自动采集（需配置开启） |
| LLM engine | 诊断过程中 LLM 主动 tool_call |

核心方法：

```go
type Collector struct{}

type TraceResult struct {
    Collapsed string      // 折叠栈帧文本（LLM 消费）
    SVGPath   string      // 火焰图 SVG 文件路径（人查看）
    TopFuncs  []HotFunc   // Top N 热点函数
    PID       int
    Duration  int         // 采集秒数
    Timestamp time.Time
    DBType    string      // mysql / postgres
}

type HotFunc struct {
    Name       string  // 函数全名，如 ha_innodb::write_row
    Samples    int     // 采样数
    Percentage float64 // 占比
    Stack      string  // 完整调用链
}

// Capture 执行一次堆栈采集，返回结果
func (c *Collector) Capture(ctx context.Context, opts CaptureOpts) (*TraceResult, error)

type CaptureOpts struct {
    PID      int           // 目标进程 PID
    Duration time.Duration // 采集时长，默认 3s，上限 10s
    TopN     int           // 提取热点函数数量，默认 20
    OutDir   string        // SVG 输出目录
}
```

内部流程：
1. `os.Run("perf", "record", "-F", "99", "-g", "-p", pid, "--", "sleep", duration)`
2. `os.Run("perf", "script")` → 原始堆栈文本
3. Go 内置折叠逻辑（等价于 `stackcollapse-perf.pl`，纯 Go 实现，无外部依赖）
4. Go 内置火焰图 SVG 生成（使用 Go 库，无需 `flamegraph.pl`）
5. 解析折叠栈帧提取 Top N 热点函数

### 3.3 SourceLookup（源码查找）

```
internal/trace/source.go
```

按热点函数名在数据库源码目录中查找函数定义。

```go
type SourceLookup struct {
    SourceDir string // 本地源码路径
}

type FuncSource struct {
    FuncName string
    FilePath string // 源码文件相对路径
    Line     int    // 起始行号
    Snippet  string // 函数签名 + 关键逻辑（限 50 行以内）
}

// Grep 查找热点函数的源码定义
func (s *SourceLookup) Grep(funcs []HotFunc) ([]FuncSource, error)
```

- 有源码目录：精确 grep，返回函数签名 + 关键逻辑片段
- 无源码目录：返回空，LLM 基于自身知识分析

### 3.4 宿主机检测

```
internal/trace/hostcheck.go
```

双重检测确认 OpenDB 与数据库进程在同一台机器：

```go
// IsLocal 检测数据库进程是否在本机运行
func IsLocal(ctx context.Context, dbType string, connHost string) (pid int, ok bool)
```

逻辑：
1. 检查连接 host 是否为 `localhost` / `127.0.0.1` / `::1` / 本机 IP
2. `ps aux` 查找数据库进程：
   - MySQL: `mysqld`
   - PostgreSQL: `postgres` (postmaster)
   - Oracle: `ora_pmon_*`
   - OpenGauss: `gaussdb`
3. 两者都满足才返回 `ok=true` 和进程 PID

### 3.5 Trace Skill（四库独立）

```
internal/mysql/skill/monitor/trace.go
internal/postgres/skill/monitor/trace.go
internal/oracle/skill/monitor/trace.go
internal/opengauss/skill/monitor/trace.go
```

每库一个 skill 文件，各自实现 `Skill` 接口。四库共用 `trace.Collector` 和 `trace.SourceLookup`，但进程发现和输出格式各自控制。

CLI 定义：

```
命令:     /trace
用法:     /trace [--duration 5] [--type cpu|wait]
别名:     无
安全级别: ReadOnly (采集本身是只读操作，perf 不修改任何状态)
```

参数：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| duration | 3 | 采集秒数，范围 1-10 |
| type | cpu | `cpu` = on-CPU 火焰图；`wait` = off-CPU（阻塞/等待分析） |

Execute 流程：
1. 调用 `hostcheck.IsLocal()` → 不在宿主机返回不可用提示
2. 调用 `Collector.Capture()` → 获取 `TraceResult`
3. 调用 `SourceLookup.Grep()` → 获取源码片段（可选）
4. 拼装终端输出：Top 热点表格 + SVG 路径
5. 返回 `Result{Data: TraceResult}` 供 LLM 消费

### 3.6 Engine Tool 注册

将 Collector 包装为 engine tool，LLM 在 assist/auto 模式下可主动调用：

```go
// ToolDef 供 LLM 识别
ToolDef{
    Name: "trace",
    Description: "采集数据库进程 OS 堆栈，返回热点函数和折叠栈帧",
    Parameters: {
        "duration": {"type": "integer", "description": "采集秒数(1-10)", "default": 3},
        "type":     {"type": "string", "enum": ["cpu", "wait"], "default": "cpu"},
    },
}
```

LLM 在诊断过程中发现 CPU 冲高时，可自主决定调用 `trace` 工具获取堆栈数据，再结合源码深入分析。

## 4. Sentinel 联动

### 触发条件（三重门控）

```
自动采集 = 在宿主机 AND trace.auto=true(默认false) AND Sentinel检测到CPU冲高
```

### 流程

```
Sentinel 检测到 CPU 冲高
  → 检查 trace.auto 配置 → false → 不采集
  → 检查 IsLocal() → false → 不采集
  → Collector.Capture(ctx, pid, 3s)
  → 结果存入 Sentinel 的 BurstReport
  → 用户执行 /llm 时，TraceResult 作为上下文注入诊断 prompt
```

### 存储

TraceResult 附加到 Sentinel 的 BurstReport 中：

```go
type BurstReport struct {
    // ... 现有字段
    TraceResult *trace.TraceResult `json:"trace_result,omitempty"`
}
```

## 5. 配置

### config.yaml

```yaml
trace:
  auto: false              # Sentinel 联动自动采集（默认关闭）
  duration: 3              # 默认采集秒数
  top_n: 20                # 提取热点函数数量
  output_dir: ~/.opendb/trace/  # SVG 输出目录

  source:
    # 方式 1: 本地路径
    dir: /usr/src/mysql-server
    # 方式 2: Git 仓库（自动 clone 到 ~/.opendb/source-cache/）
    repo: https://github.com/mysql/mysql-server
    branch: "8.0"          # 可选，默认 main
```

### 源码获取

- 配置了 `dir`：直接读本地文件
- 配置了 `repo`：首次使用时 `git clone --depth 1` 到 `~/.opendb/source-cache/<repo-name>/`，后续 `git pull`
- 都没配：不做源码分析，LLM 基于自身知识分析
- `dir` 优先于 `repo`

## 6. 安全设计

### OS 命令白名单

所有 OS 命令必须经过 `internal/os/exec.go` 白名单：
- `perf` — 堆栈采集
- `ps` — 进程发现
- `pstack` — 备用堆栈快照（perf 不可用时）
- `git` — 源码仓库 clone/pull

不在白名单的命令一律拒绝。

### 权限要求

- `perf record` 需要 root 或 `CAP_SYS_ADMIN`
- 如果权限不足，返回明确提示："需要 root 权限或 CAP_SYS_ADMIN capability"
- 不尝试 sudo，不绕过权限

### 采集安全

- 采集时长硬上限 10 秒，超过直接拒绝
- 采集频率 perf -F 99（每秒 99 次采样），对生产环境影响极小
- Sentinel 自动采集间隔不小于 60 秒（防止连续触发）

## 7. LLM 分析 Prompt 设计

当 LLM 需要分析 trace 数据时，注入以下结构：

```
## OS 堆栈采集结果

采集时间: 2026-04-06 14:23:15
目标进程: mysqld (PID 12345)
采集时长: 3 秒
采样频率: 99 Hz

### Top 20 热点函数

| # | 函数 | 采样数 | 占比 |
|---|------|--------|------|
| 1 | ha_innodb::write_row | 1523 | 18.2% |
| 2 | lock_wait_timeout_thread | 892 | 10.6% |
| ... |

### 折叠栈帧（Top 调用链）

mysqld;ha_innodb::write_row;row_insert_for_mysql;lock_table 1523
mysqld;lock_wait_timeout_thread;os_event_wait_low 892
...

### 源码片段（如有）

#### ha_innodb::write_row (storage/innobase/handler/ha_innodb.cc:8234)
```cpp
int ha_innodb::write_row(uchar* record) {
    // ... 关键逻辑
}
```

请从数据库源码层面分析：
1. 哪些函数调用链消耗了最多 CPU 或造成了最多等待
2. 这些热点函数在数据库内核中的作用是什么
3. 根因是什么（锁争用、I/O 等待、算法低效等）
4. 可操作的优化方案（参数调整、SQL 改写、架构优化）
```

## 8. 输出格式

### 终端输出（人看）

```
OS 堆栈分析 (mysqld PID 12345, 3s, 99Hz)

  Top 热点函数:
  ┌────┬──────────────────────────────┬────────┬───────┐
  │ #  │ 函数                          │ 采样   │ 占比  │
  ├────┼──────────────────────────────┼────────┼───────┤
  │ 1  │ ha_innodb::write_row          │ 1523   │ 18.2% │
  │ 2  │ lock_wait_timeout_thread      │ 892    │ 10.6% │
  │ ...│                              │        │       │
  └────┴──────────────────────────────┴────────┴───────┘

  火焰图: ~/.opendb/trace/flame-20260406-142315.svg

  [LLM 分析结论...]
```

### Data 输出（LLM 消费）

`Result.Data` 返回 `TraceResult` 结构体，engine 序列化为 JSON 供 LLM 使用。

## 9. 错误场景

| 场景 | 处理 |
|------|------|
| 不在宿主机 | "trace 功能需要 OpenDB 部署在数据库宿主机上" |
| perf 未安装 | "需要安装 linux-tools: apt install linux-tools-$(uname -r)" |
| 权限不足 | "需要 root 权限或设置 CAP_SYS_ADMIN" |
| 数据库进程未找到 | "未找到 mysqld 进程，请确认数据库正在运行" |
| 源码目录不存在 | 跳过源码分析，LLM 基于自身知识分析 |
| 采集失败 | 返回 perf 错误信息，建议 pstack 作为降级方案 |

## 10. 文件清单

```
新增:
  internal/os/exec.go                          # OS 命令白名单执行器
  internal/trace/collector.go                  # 堆栈采集 + 栈帧折叠 + SVG 生成
  internal/trace/source.go                     # 源码 grep 查找
  internal/trace/hostcheck.go                  # 宿主机检测
  internal/trace/types.go                      # TraceResult / HotFunc 等类型定义
  internal/trace/flamegraph.go                 # 纯 Go 火焰图 SVG 生成
  internal/trace/collapse.go                   # 纯 Go 栈帧折叠（替代 stackcollapse-perf.pl）
  internal/mysql/skill/monitor/trace.go        # MySQL trace skill
  internal/postgres/skill/monitor/trace.go     # PostgreSQL trace skill
  internal/oracle/skill/monitor/trace.go       # Oracle trace skill（无源码分析）
  internal/opengauss/skill/monitor/trace.go    # OpenGauss trace skill（暂不实现，占位）

修改:
  internal/config/config.go                    # 新增 trace 配置段
  internal/mysql/register.go                   # 注册 MySQL trace skill
  internal/postgres/register.go                # 注册 PG trace skill
  internal/oracle/register.go                  # 注册 Oracle trace skill
  internal/opengauss/register.go               # 注册 OG trace skill
  internal/oracle/sentinel/report.go           # BurstReport 新增 TraceResult 字段
```

## 11. 实现优先级

1. **P0**: `os/exec.go` + `trace/collector.go` + `trace/hostcheck.go` + MySQL trace skill
2. **P1**: PG trace skill + `trace/source.go` + 源码分析
3. **P2**: Sentinel 联动 + Engine tool 注册（LLM 主动调用）
4. **P3**: Oracle trace skill（无源码分析）
5. **P4**: OG trace skill（暂不实现）

## 12. 平台限制

- 仅 Linux 支持（perf 是 Linux 专有工具）
- macOS / Windows 上 `/trace` 返回"仅支持 Linux 平台"
- 编译不受影响（通过 build tags 或运行时检测 `runtime.GOOS`）
