# 09-附 — PromptProfile 详解：从 2914 行重复代码到 520 行零重复

## 一、当前 OpenDB：4 套代码，82% 重复

### 代码行数统计

```
oracle/agent/                    mysql/agent/                   postgres/agent/
├── prompts.go    (143行)        ├── prompt.go    (131行)        ├── prompt.go    (140行)
├── prompt_loop.go (404行)       │                               │
├── loop.go       (400行)        ├── loop.go      (366行)        ├── loop.go      (371行)
├── diagnose.go   (374行)        ├── diagnose.go  (265行)        ├── diagnose.go  (320行)
─────────────────────           ─────────────────────           ─────────────────────
合计: 1321 行                    合计: 762 行                    合计: 831 行

三套总计: 2914 行
```

### 实际重复程度

**prompts.go / prompt.go 对比 — 11 条规则中 9 条完全相同：**

```
Oracle: "你是 OpenDB 数据库诊断专家。你的任务是分析 Oracle 数据库性能异常..."
MySQL:  "你是 OpenDB 数据库诊断专家。你的任务是分析 MySQL (InnoDB) 数据库性能异常..."
PG:     "你是 OpenDB 数据库诊断专家。你的任务是分析 PostgreSQL 数据库性能异常..."
                                                    ^^^^^^^^^^^^
                                                    只有 DB 名不同

Oracle: "禁止从 SQL 文本中推断对象存在 — v$sql 中的 SQL..."
MySQL:  "禁止从 SQL 文本中推断对象存在 — performance_schema 中的 SQL..."
PG:     "禁止从 SQL 文本中推断对象存在 — pg_stat_statements 中的 SQL..."
                                         ^^^^^^^^^^^^^^^
                                         只有视图名不同
```

**loop.go 对比 — 逻辑 100% 相同：**

```
主循环: 相同
executeTool: 相同
formatTable: 相同
buildFilteredTools: 相同
唯一区别: MySQL/PG 多了 truncateResult(3000字)，Oracle 没有
```

**diagnose.go 对比 — 逻辑 100% 相同：**

```
Diagnose(): 相同
runPlaybook(): 相同
runAssist(): 相同
runAuto(): 相同
runWithLoop(): 相同
streamChat(): 相同
readOnlyFilter(): 相同
```

**结论：2914 行中约 2400 行（82%）是复制粘贴。真正 DB 特定的只有 ~500 行。**

### 带来的问题

```
问题 1: 改一个 bug 要改 3-4 个文件
  例: 收敛引导的提示词需要调整 → 改 oracle/loop.go + mysql/loop.go + postgres/loop.go
  经常漏改某个 DB

问题 2: 加一个特性要改 3-4 个文件
  例: 加重试逻辑 → 3 个 loop.go 都要改
  例: 加上下文压缩 → 3 个 loop.go 都要改

问题 3: DB 特定知识散落
  Oracle: prompts.go + prompt_loop.go 两份不同的提示词
  没有结构化的等待事件速查、诊断路径指引

问题 4: 加新 DB 成本高
  复制整个 agent 目录 → ~700-800 行新代码，80% 是复制
```

---

## 二、新设计：通用 Engine + DB 特定 Profile

```
当前:                                   新:
oracle/agent/ (1321行)                  engine/ (通用，写一次)
mysql/agent/  (762行)                     ├── engine.go     (统一主循环)
postgres/agent/ (831行)                   ├── context/      (上下文管理)
──────────────                            ├── tool/         (工具执行)
2914 行，82% 重复                          ├── retry/        (重试)
                                          └── provider/     (厂商适配)
                                          → ~1300 行，通用代码，写一次

                                        profile/ (DB 特定，只写差异)
                                          ├── oracle.go    (~150行)
                                          ├── mysql.go     (~120行)
                                          ├── postgres.go  (~130行)
                                          └── opengauss.go (~120行)
                                          → ~520 行，零重复
```

### PromptProfile 接口 — Engine 和 DB 之间的契约

```go
type PromptProfile interface {
    Product() string                                    // "oracle" / "mysql" / ...
    SystemPromptRules() string                          // DB 特定系统提示
    ToolRegistry() skill.Registry                       // 可用工具注册表
    ToolFilter(mode DiagnoseMode) func(Skill) bool      // 按模式过滤工具
    ToolUsageHint(skillName string) string               // 工具使用场景提示
    CompressReport(rawReport any) string                 // 诊断报告压缩
    DefaultMaxTurns(mode DiagnoseMode) int               // 默认最大轮次
}
```

### Engine 怎么使用 Profile

```go
// Engine 主循环中 — 不关心是 Oracle 还是 MySQL

func (e *Engine) Run(ctx, input) {
    // 构建系统提示 = 通用基座 + DB 特定规则
    systemPrompt := e.contextBuilder.identityAndRules()  // 通用 ~3000字
                  + e.profile.SystemPromptRules()         // DB 特定
                  + modeModifier(input.Mode)              // 模式修饰

    // 构建工具列表 = 从 Profile 获取并过滤
    allSkills := e.profile.ToolRegistry().All()
    filter := e.profile.ToolFilter(input.Mode)
    for _, skill := range allSkills {
        if !filter(skill) { continue }
        // 工具描述增强
        hint := e.profile.ToolUsageHint(skill.Name())
        tools = append(tools, ToolSchema{
            Name:        skill.Name(),
            Description: skill.Description() + "\n使用场景: " + hint,
            InputSchema: skill.ParamsSchema(),
        })
    }

    // 后续主循环...完全通用，不关心 DB 类型
}
```

---

## 三、各 DB Profile 写什么内容

### Oracle Profile（~150 行）

```
SystemPromptRules() ~80行:
  ├─ 对象引用规则
  │   - 对象名默认大写
  │   - ISEQ$$_ 序列处理
  │   - CDB/PDB 环境区分
  │
  ├─ 等待事件速查表（16个）
  │   - db file sequential read → 单块I/O
  │   - enq: TX - row lock → 行锁争用
  │   - log file sync → redo 写入
  │   - cursor: pin S wait on X → 硬解析争用
  │   ...
  │
  ├─ 关键视图参考（10个）
  │   - v$session, v$sql, v$sql_plan, v$lock...
  │
  ├─ 参数修改注意
  │   - MEMORY vs SPFILE scope
  │   - 隐含参数（_ 开头）谨慎
  │
  └─ 常见 ORA 错误（6个）
      - ORA-01555 → undosess + params
      - ORA-04031 → sga + params
      - ORA-01652 → tempsess + space

ToolUsageHint() ~25行:
  21 个 Oracle 工具的场景提示
  activesessions → "诊断入口：查看当前活跃会话和等待事件分布"
  waits → "诊断入口：查看非idle等待事件排名，定位瓶颈方向"
  explain → "深度分析：查看SQL执行计划，需要sql_id参数"
  blocktree → "锁深度分析：查看完整阻塞链（谁阻塞了谁）"
  ...

ToolFilter() ~15行
DefaultMaxTurns() ~10行
CompressReport() ~20行 (桥接现有 sentinel.CompressReport)
```

### MySQL Profile（~120 行）

```
SystemPromptRules() ~60行:
  ├─ 对象引用规则
  │   - lower_case_table_names 影响大小写
  │   - 反引号引用特殊标识符
  │
  ├─ InnoDB 等待事件（8个）
  │   - Waiting for table metadata lock → DDL被DML阻塞
  │   - innodb_lock_wait → InnoDB行锁
  │   - Waiting for table flush → FLUSH等待
  │   ...
  │
  ├─ 关键视图
  │   - information_schema.PROCESSLIST
  │   - performance_schema.events_waits_*
  │   - sys.innodb_lock_waits
  │
  └─ 参数修改注意
      - SESSION vs GLOBAL vs SET PERSIST(8.0+)

ToolUsageHint() ~20行:
  MySQL 独有工具：bufferpool, replication, innodb, binlog, deadlock
  共有工具复用相同场景描述

ToolFilter() ~15行
DefaultMaxTurns() ~10行
CompressReport() ~15行
```

### PostgreSQL Profile（~130 行）

```
SystemPromptRules() ~70行:
  ├─ 对象引用规则
  │   - 默认小写，双引号保留大小写
  │
  ├─ PG 等待事件（10个）
  │   - LWLock:BufferContent → 共享缓冲区争用
  │   - Lock:transactionid → 行锁
  │   - IO:DataFileRead → 数据文件读取
  │   ...
  │
  ├─ 关键视图
  │   - pg_stat_activity, pg_stat_statements
  │   - pg_locks, pg_stat_user_tables
  │
  ├─ MVCC 特有知识（PG 独有！）
  │   - 死元组和 VACUUM
  │   - 长事务阻止 VACUUM
  │   - XID wraparound 风险
  │
  └─ 参数修改注意
      - ALTER SYSTEM + SELECT pg_reload_conf()
      - 部分参数需要重启

ToolUsageHint() ~22行:
  PG 独有工具：sharedbufs, wal, vacuum, xid, bloat, longtx, slots, cancel
  PG 有 kill + cancel 两个终止方式

ToolFilter() ~15行 (cancel 也是操作类)
DefaultMaxTurns() ~10行
CompressReport() ~15行
```

### OpenGauss Profile（~120 行）

```
基于 PostgreSQL Profile，增加 OpenGauss 差异:
  - MOT (Memory-Optimized Table) 特有知识
  - gs_ 前缀系统视图
  - WDR 报告（替代 PG 的 pg_stat_statements 扩展）
  - Gauss 特有等待事件
```

---

## 四、工具列表差异：每个 DB 不完全一样

```
                    Oracle              MySQL               PostgreSQL
共有工具(14):        activesessions      activesessions      activesessions
                    sessions            sessions            sessions
                    waits               waits               waits
                    locks               locks               locks
                    blocktree           blocktree           blocktree
                    health              health              health
                    slowsql             slowsql             slowsql
                    topsql              topsql              topsql
                    explain             explain             explain
                    params              params              params
                    space               space               space
                    alert               alert               alert
                    os                  os                  os
                    sql                 sql                 sql

Oracle 独有(13):     latches             —                   —
                    mutexes             —                   —
                    redo                —                   —
                    fra                 —                   —
                    asm                 —                   —
                    pga                 —                   —
                    sga                 —                   —
                    tempsess            —                   —
                    undosess            —                   —
                    sortusage           —                   —
                    resource            —                   —
                    jobs                —                   —
                    planhistory         —                   —

MySQL 独有(5):       —                  bufferpool           —
                    —                  replication          —
                    —                  innodb               —
                    —                  binlog               —
                    —                  deadlock             —

PG 独有(8):          —                  —                   sharedbufs
                    —                  —                   wal
                    —                  —                   replication (PG版)
                    —                  —                   vacuum
                    —                  —                   xid
                    —                  —                   bloat
                    —                  —                   longtx
                    —                  —                   slots

操作类:             kill, alter, resize  kill                kill, cancel

ToolUsageHint 为每个 DB 的独有工具提供场景提示
  → Oracle DBA 看到 Oracle 的工具和场景
  → MySQL DBA 看到 MySQL 的工具和场景
  → 不会看到不属于自己 DB 的工具
```

---

## 五、对标 Claude Code

Claude Code 本身不需要 PromptProfile（只服务代码场景），但有类似的分层机制：

```
Claude Code 的分层:                     OpenDB 新 Engine 的分层:
───────────────                         ──────────────────────

prompts.ts:getSystemPrompt()            ContextBuilder.identityAndRules()
→ 通用行为规范（固定不变）                → 通用行为规范（固定不变）

CLAUDE.md (4级优先级)                    PromptProfile.SystemPromptRules()
→ 项目特定规则（用户配置）                → DB 特定规则（Profile 配置）

tool.prompt()                           PromptProfile.ToolUsageHint()
→ 每个工具动态生成描述                    → 每个工具添加使用场景提示

对应关系:
  Claude Code 的 CLAUDE.md ≈ OpenDB 的 PromptProfile
  都是"在通用基座上叠加领域特定知识"
  Claude Code 按项目分，OpenDB 按数据库类型分
```

---

## 六、完整对比表

| 维度 | 当前 OpenDB | 新 Engine + Profile |
|------|------------|-------------------|
| **总代码量** | 2914 行（3套 agent 目录） | ~1800 行（Engine ~1300 + Profile ~520） |
| **重复率** | 82%（~2400行重复） | 0% |
| **DB 特定代码位置** | 散落在 prompts.go + loop.go + diagnose.go | 集中在一个 Profile 文件 |
| **加新 DB 成本** | 复制整个 agent 目录（~700行） | 写一个 Profile（~120行） |
| **改 bug** | 改 3-4 个文件（每个 DB 一份） | 改 1 个文件（Engine 或 Profile） |
| **加新特性** | 改 3-4 个文件 | 改 1 个文件（Engine） |
| **工具场景提示** | 无（只有工具名列表） | 每个工具有使用场景描述 |
| **DB 特定知识** | 仅对象引用规则 | 等待事件+视图+参数+错误速查 |
| **Oracle prompt_loop.go** | 独立 404 行 | 统一到 Engine，文本模拟作为 fallback |

### 加新 DB 成本对比（以 OpenGauss 为例）

```
当前:
  mkdir internal/opengauss/agent/
  复制 loop.go (371行) + prompt.go (140行) + diagnose.go (320行)
  → 831 行新代码，80% 复制
  → 改 DB 名、改工具列表、改视图名
  → 容易漏改

新:
  创建 profile/opengauss.go
  → 120 行新代码，0% 复制
  → 只写 OpenGauss 和 PG 的差异
  → Engine 一行不改

  节省 85% 代码量
```
