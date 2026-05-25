# SQL Advisor + 连接会话诊断 设计规格

## 背景

Rule Engine 在"运行时故障诊断"（锁/容量/解析/参数）已达 70-80% 成熟度，但两个领域存在结构性缺失：

- **SQL 计划分析**: 25 条 SQL tuning 规则因数据不足+触发不对全部不生效，4 条 SQL perf 规则触发但诊断太泛（~15 分万金油）
- **连接/会话管理**: 规则存在但 `buildLiveReport()` 没采集所需数据（v$resource_limit、idle session 分布），0 分

## 设计目标

1. 新建独立 SQL Advisor 子系统，达到 DBA 手工分析水平
2. 增强连接/会话诊断，激活已有规则 + 新增 2 条
3. 四库插件化架构，Oracle 先行，MySQL/PG/OG 按模式复制
4. 25 条旧 SQL tuning 规则在 Advisor 开发时一次性吸收删除

---

## 一、SQL Advisor

### 1.1 架构

```
┌─────────────────────────────────────────────────────┐
│                    入口层                             │
│                                                      │
│  /sqladvisor {sql_id}     用户手动指定               │
│  /sqladvisor "SQL文本"    文本匹配 → v$sql 找 sql_id │
│  /sqladvisor              自动取 rule 标记的问题 SQL  │
│  /rule live               sentinel 告警 → 自动调用   │
└──────────────────────┬──────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────┐
│              SQL Advisor Engine                       │
│                                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────┐ │
│  │Data Collector│  │ Plan Analyzer │  │ Report Gen │ │
│  │              │  │              │  │            │ │
│  │ v$sql        │  │ 执行计划树   │  │ 问题清单   │ │
│  │ v$sql_plan   │  │ 7个Analyzer  │  │ 修复建议   │ │
│  │ v$sql_plan_  │  │   逐个扫描   │  │ 原生SQL    │ │
│  │  statistics  │  │   计划树     │  │ 数据不足   │ │
│  │ DBA_TAB_STATS│  │              │  │  → 引导    │ │
│  │ DBA_IND_COLS │  │              │  │            │ │
│  └─────────────┘  └──────────────┘  └────────────┘ │
└──────────────────────┬──────────────────────────────┘
                       │
            ┌──────────┴──────────┐
            ▼                     ▼
   独立 SQL 诊断报告        Rule Engine 内嵌
   (/sqladvisor 调用)      (sentinel 触发时)
```

### 1.2 数据自适应策略

1. 先用 `v$sql_plan`（零配置，有估算行数 E-Rows + 谓词）
2. 尝试查 `v$sql_plan_statistics_all`，有数据则用（实际行数 A-Rows）
3. 如果 A-Rows 不可用且分析结果不够确定，输出引导建议：

```
── 数据深度 ──
  当前: v$sql_plan (估算行数)
  建议: ALTER SYSTEM SET STATISTICS_LEVEL=ALL 后重新执行该 SQL，
        再次运行 /sqladvisor {sql_id} 可获得实际行数对比分析
```

### 1.3 七个分析维度

| # | Analyzer | 检查内容 | 优化建议输出 |
|---|----------|---------|-------------|
| 1 | access_path | 全扫大表、INDEX SKIP SCAN、笛卡尔积 | CREATE INDEX ... |
| 2 | predicate | 隐式类型转换、函数调用索引失效、LIKE '%前缀' | 改写 WHERE 条件 |
| 3 | join | NL 驱动表行数过多、HASH JOIN 溢出、SORT MERGE 不必要 | 收集统计信息 / USE_HASH hint |
| 4 | statistics | 过期(>30天)、缺直方图、num_rows 偏差 | DBMS_STATS.GATHER_TABLE_STATS |
| 5 | plan_stability | 多 plan_hash_value、当前非最优、ACS 抖动 | DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE |
| 6 | resource | buffer_gets/rows 异常高、排序溢出、PGA 不足 | 改写 SQL 减少排序 |
| 7 | rewrite | 不必要 ORDER BY、SELECT *、缺分区裁剪条件 | 改写 SQL 片段 |

### 1.4 吸收旧规则对照表

每个 Analyzer 开发完成时，必须覆盖对应旧规则的检查逻辑并删除旧规则。

| Analyzer | 吸收的旧规则 (rules_sql_tuning.go) | 数量 |
|----------|-----------------------------------|------|
| access_path | full_scan_large_table, missing_index, function_based_index_needed, composite_index_order, index_skip_scan_slow, cartesian_join | 6 |
| predicate | （新能力，无旧规则） | 0 |
| join | nl_high_starts, hash_join_spill, sort_merge_inefficient | 3 |
| statistics | stale_table_stats, no_histogram, dynamic_sampling_low, extended_stats_needed | 4 |
| plan_stability | spm_baseline, sql_profile_drift, adaptive_plan_flapping, bind_variable_peeking | 4 |
| resource | pq_skew_detected, pq_dop_downgrade, pq_resource_exhausted | 3 |
| rewrite | unnesting_blocked, view_merging_blocked, partition_pruning_failed, unused_index, invisible_index_test | 5 |
| **合计** | | **25** |

**纪律**: 不允许两套并存上线。7 个 Analyzer 全部完成 = 25 条旧规则全部删除。

### 1.5 与 Rule Engine 集成

4 条 SQL perf 规则 (WD025/WD021/WD022/WD023) 改为**信号触发器**：

```
WD025 检测到全扫 → 调 SQL Advisor → 把 Findings 注入证据链
WD021 检测到计划漂移 → 调 SQL Advisor → 同上
WD022 检测到排序溢出 → 调 SQL Advisor → 同上
WD023 检测到热点SQL → 调 SQL Advisor → 同上
```

Rule Engine 消费 SQL Advisor 的 `SQLReport`，通过 `FindingsFunc` / `ActionsFunc` 动态生成诊断输出。

### 1.6 输出报告格式

```
═══ SQL Advisor 诊断报告 ═══

SQL ID: 8rd2y271f37v8
SQL 文本: SELECT * FROM orders o JOIN items i ON ...
执行次数: 12,345    平均耗时: 3.2s    逻辑读: 892,341/次

── 执行计划 ──
  Id | Operation                | Name       | Rows  | Cost | Filter
  0  | SELECT STATEMENT         |            |       | 8923 |
  1  |  NESTED LOOPS            |            | 50000 | 8923 |
  2  |   TABLE ACCESS FULL      | ORDERS     | 50000 | 4521 | STATUS='pending'
  3  |   TABLE ACCESS BY ROWID  | ITEMS      | 1     | 1    |
  4  |    INDEX UNIQUE SCAN     | PK_ITEMS   | 1     | 0    |

── 问题诊断（3 个问题）──

  ⚠ P1: 全表扫描大表 — ORDERS (2.3GB, 1200万行)
     WHERE status='pending' 选择度 0.3%，应走索引
     ➜ CREATE INDEX idx_orders_status ON orders(status);
     ➜ 预期: 逻辑读从 892K 降至 ~3K

  ⚠ P2: NL Join 驱动表行数过多 — 50000 行驱动 NESTED LOOP
     被驱动表 ITEMS 访问 50000 次，应改 HASH JOIN
     ➜ EXEC DBMS_STATS.GATHER_TABLE_STATS('APP','ORDERS');
     ➜ 或 SELECT /*+ USE_HASH(i) */ ...

  ℹ P3: 统计信息过期 — ORDERS 表 last_analyzed = 45 天前
     ➜ EXEC DBMS_STATS.GATHER_TABLE_STATS('APP','ORDERS',
            method_opt=>'FOR ALL COLUMNS SIZE AUTO');

── 数据深度 ──
  当前: v$sql_plan (估算行数)
  建议: ALTER SYSTEM SET STATISTICS_LEVEL=ALL 后重新运行可获得 A-Rows 对比
```

### 1.7 数据采集 SQL

#### Collector: v$sql 基础信息
```sql
SELECT sql_id, sql_text, executions, elapsed_time, buffer_gets,
       disk_reads, rows_processed, invalidations, plan_hash_value,
       optimizer_cost, module, action
FROM v$sql WHERE sql_id = :1
ORDER BY elapsed_time DESC FETCH FIRST 1 ROWS ONLY
```

#### Collector: v$sql_plan 完整计划树
```sql
SELECT id, parent_id, operation, options, object_owner, object_name,
       cardinality, bytes, cost, filter_predicates, access_predicates,
       partition_start, partition_stop, other_xml
FROM v$sql_plan
WHERE sql_id = :1 AND plan_hash_value = :2
ORDER BY id
```

#### Collector: v$sql_plan_statistics_all（自适应深度）
```sql
SELECT id, operation, options, object_name,
       cardinality AS e_rows, last_output_rows AS a_rows,
       last_starts AS starts, last_cr_buffer_gets, last_disk_reads
FROM v$sql_plan_statistics_all
WHERE sql_id = :1 AND plan_hash_value = :2
ORDER BY id
```

#### Collector: 表统计信息
```sql
SELECT table_name, num_rows, last_analyzed, stale_stats, blocks,
       ROUND((SYSDATE - last_analyzed)) AS days_since_analyzed
FROM dba_tab_statistics
WHERE owner = :1 AND table_name = :2
```

#### Collector: 索引信息
```sql
SELECT i.index_name, i.uniqueness, i.status, i.visibility,
       ic.column_name, ic.column_position,
       i.distinct_keys, i.clustering_factor, i.num_rows
FROM dba_indexes i
JOIN dba_ind_columns ic ON i.index_name = ic.index_name AND i.owner = ic.index_owner
WHERE i.table_owner = :1 AND i.table_name = :2
ORDER BY i.index_name, ic.column_position
```

#### Collector: 多计划对比
```sql
SELECT plan_hash_value, SUM(executions) execs,
       ROUND(SUM(elapsed_time)/GREATEST(SUM(executions),1)/1e6, 3) avg_sec,
       ROUND(SUM(buffer_gets)/GREATEST(SUM(executions),1)) avg_gets
FROM v$sql WHERE sql_id = :1
GROUP BY plan_hash_value ORDER BY avg_sec
```

#### Collector: SQL 文本匹配（/sqladvisor "SQL文本" 模式）
```sql
SELECT sql_id, sql_text, executions, elapsed_time/1e6 total_sec
FROM v$sql
WHERE sql_text LIKE '%' || :1 || '%'
  AND sql_text NOT LIKE '%v$sql%'
ORDER BY elapsed_time DESC
FETCH FIRST 5 ROWS ONLY
```

---

## 二、连接/会话诊断

### 2.1 数据采集（buildLiveReport 新增）

```sql
-- 1. 资源限制使用率
SELECT resource_name, current_utilization, max_utilization, limit_value
FROM v$resource_limit WHERE resource_name IN ('sessions','processes');

-- 2. 会话分布（按来源聚合）
SELECT username, machine, program, status, COUNT(*) cnt
FROM v$session WHERE type='USER'
GROUP BY username, machine, program, status
ORDER BY cnt DESC FETCH FIRST 20 ROWS ONLY;

-- 3. 空闲会话年龄分布
SELECT COUNT(*) total_idle,
       SUM(CASE WHEN (SYSDATE-logon_time)*1440 > 60 THEN 1 ELSE 0 END) idle_gt_1h,
       SUM(CASE WHEN (SYSDATE-logon_time)*1440 > 480 THEN 1 ELSE 0 END) idle_gt_8h
FROM v$session WHERE status='INACTIVE' AND type='USER';

-- 4. IDLE_TIME Profile 检查
SELECT profile, limit AS idle_time_limit
FROM dba_profiles WHERE resource_name='IDLE_TIME' AND limit != 'UNLIMITED';
```

### 2.2 规则

| 规则 | 状态 | 触发条件 | 诊断 |
|------|------|---------|------|
| connection_exhaust | 已有，激活 | sessions_used_pct > 85% | 接近上限 → 定位来源 → 建议增大/清理 |
| session_leak | 已有，激活 | idle_gt_1h > total * 50% | 泄漏 → 定位 machine/program → 建议 IDLE_TIME |
| session_storm | **新增** | active_sessions 突增 > 3x baseline | 连接冲高 → 来源分析 → 建议连接池 |
| aborted_connects | **新增** | alert_log 有 ORA-00018/00020 | 连接被拒 → 原因分析 → 扩容或查泄漏 |

---

## 三、目录结构

```
internal/
├── sqladvisor/                    # 公共接口+类型（无 build tag）
│   ├── types.go                   # PlanNode, Finding, Suggestion, SQLReport
│   ├── analyzer.go                # Analyzer 接口定义
│   └── report.go                  # 报告渲染（通用）
│
├── oracle/
│   ├── sqladvisor/                # Oracle 实现（build tag: oracle || full）
│   │   ├── advisor.go             # Analyze(sqlID) 入口
│   │   ├── collector.go           # v$sql, v$sql_plan, DBA_TAB_STATISTICS
│   │   ├── plan_parser.go         # Oracle 执行计划行 → PlanNode 树
│   │   └── analyzers/
│   │       ├── access_path.go     # 维度1: 全扫/索引/笛卡尔积
│   │       ├── predicate.go       # 维度2: 谓词效率/隐式转换
│   │       ├── join.go            # 维度3: 连接方式/驱动表
│   │       ├── statistics.go      # 维度4: 统计信息过期/缺失
│   │       ├── plan_stability.go  # 维度5: 多计划/计划漂移
│   │       ├── resource.go        # 维度6: 资源消耗/排序溢出
│   │       └── rewrite.go         # 维度7: SQL 改写建议
│   ├── skill/query/
│   │   └── sqladvisor_skill.go    # /sqladvisor 技能注册
│   ├── ruleengine/
│   │   └── rules_session.go       # session_storm + aborted_connects
│   └── register.go                # RegisterSkills 中注册 sqladvisor
│
├── mysql/sqladvisor/              # MySQL 实现（后续）
├── postgres/sqladvisor/           # PG 实现（后续）
└── opengauss/sqladvisor/          # OG 实现（后续）
```

### 四库共用 vs 各库独立

| 层 | 共用 | 各库独立 |
|---|------|---------|
| 类型定义 | PlanNode, Finding, Suggestion, SQLReport | - |
| Analyzer 接口 | `Analyzer interface { Analyze(*PlanTree) []Finding }` | - |
| 报告渲染 | 表格/颜色/排版 | - |
| SQL 采集 | - | v$sql_plan / EXPLAIN / pg_stat |
| 计划解析 | - | Oracle 行格式 / MySQL JSON / PG text |
| 分析器实现 | - | DBMS_STATS / ANALYZE TABLE / ANALYZE |
| 优化建议 SQL | - | Oracle 原生 / MySQL 原生 / PG 原生 |

---

## 四、核心数据结构

```go
// 执行计划节点（树结构，四库共用）
type PlanNode struct {
    ID             int
    ParentID       int
    Operation      string        // TABLE ACCESS, NESTED LOOPS, HASH JOIN...
    Options        string        // FULL, BY INDEX ROWID...
    ObjectName     string
    ObjectOwner    string
    Rows           int64         // E-Rows (优化器估算)
    ActualRows     *int64        // A-Rows (实际，可能nil)
    Starts         *int64        // 启动次数（可能nil）
    Cost           int64
    Bytes          int64
    FilterPred     string
    AccessPred     string
    Children       []*PlanNode
}

// 单条 SQL 的诊断报告
type SQLReport struct {
    SQLID          string
    SQLText        string        // 前 500 字符
    ExecCount      int64
    AvgElapsedSec  float64
    AvgBufferGets  int64
    AvgDiskReads   int64
    AvgRowsProc    int64
    PlanTree       *PlanNode
    Plans          []PlanInfo    // 多计划对比
    Findings       []Finding     // 问题清单（按严重度排序）
    DataDepth      DataDepth     // Basic / Full
    UpgradeHint    string        // 数据不足时的引导
}

type DataDepth int
const (
    DataDepthBasic DataDepth = iota  // v$sql_plan only
    DataDepthFull                     // v$sql_plan_statistics_all
)

// 诊断发现
type Finding struct {
    Severity    string          // P1 / P2 / P3
    Category    string          // access_path / predicate / join / ...
    Summary     string          // 一句话
    Detail      string          // 详细证据
    Suggestions []Suggestion
}

// 优化建议
type Suggestion struct {
    Action      string          // 建议描述
    SQL         string          // 可直接执行的原生 SQL
    Risk        string          // 风险说明
    Impact      string          // 预期效果
}

// Analyzer 接口（各库实现）
type Analyzer interface {
    Name() string
    Analyze(ctx *AnalyzeContext) []Finding
}

// 分析上下文
type AnalyzeContext struct {
    Report      *SQLReport
    TableStats  map[string]*TableStat   // owner.table → stats
    IndexInfo   map[string][]IndexCol   // owner.table → index columns
    PlanTree    *PlanNode
    DataDepth   DataDepth
}
```

---

## 五、分析流程

```
Analyze(sqlID string) *SQLReport
  │
  ├─ 1. Collect
  │     ├─ 查 v$sql 基础信息（执行次数、耗时、逻辑读）
  │     ├─ 查 v$sql_plan 完整计划树
  │     ├─ 尝试 v$sql_plan_statistics_all（有数据→DataDepthFull，无→Basic）
  │     ├─ 从计划树提取涉及的表 → 查 DBA_TAB_STATISTICS
  │     └─ 查涉及表的 DBA_INDEXES + DBA_IND_COLUMNS
  │
  ├─ 2. Parse
  │     └─ 行数据 → PlanNode 树（parent_id 关联）
  │
  ├─ 3. Analyze（7 个 Analyzer 依次执行）
  │     ├─ access_path:  遍历叶节点，全扫大表 + 检查索引是否存在
  │     ├─ predicate:    解析 filter_predicates，检测函数/类型转换
  │     ├─ join:         检查 Join 节点 E-Rows，NL 驱动表行数
  │     ├─ statistics:   对比 last_analyzed 天数，检查直方图
  │     ├─ stability:    多 plan_hash_value 对比 avg_sec
  │     ├─ resource:     buffer_gets / rows_processed 比值
  │     └─ rewrite:      SELECT * 检测，不必要 ORDER BY
  │
  ├─ 4. Rank
  │     └─ 按 Finding.Severity 排序（P1 > P2 > P3）
  │
  └─ 5. Report
        ├─ 生成结构化 SQLReport
        └─ 如 DataDepth=Basic 且有不确定发现 → 设置 UpgradeHint
```

---

## 六、不做的事

- 不改 Rule Engine 核心框架（Trigger/Tree/Resolver）
- 不做 MySQL/PG/OG 的 SQL Advisor（先 Oracle，其他库按模式复制）
- 不做 ASH/AWR 历史分析（只做实时 v$ 视图分析）
- 不做 SQL 自动改写执行（只给建议，不自动执行）

---

## 七、预期效果

| 场景类别 | 当前得分 | 预期得分 |
|---------|---------|---------|
| T016-T019 执行计划漂移/全扫 | ~15 | 60-75 |
| T022/T023/T039 排序/Hash/PGA 溢出 | 0 | 55-65 |
| T025 PL/SQL 逐行处理 | 0 | 50-60 |
| T026 数据倾斜+bind peeking | 50 | 65-75 |
| T027 分区裁剪失败 | ~15 | 60-70 |
| T031-T034 排序/视图/PX/回退 | 0~15 | 50-65 |
| T054 HWM 段空间浪费 | 50 | 65-75 |
| T081 隐式类型转换 | 0 | 70-80 |
| T076-T079/T095 连接/会话 | 0 | 60-70 |
