# /wdranalyze 设计方案

> 状态：待评审
> 范围：opendb (main) + 同步 dbaa / linkdb
> 关联：
>   - 现有 `/wdr` 仅列快照 + 指引（`internal/opengauss/skill/query/wdr.go`，约 100 行）
>   - 复用 `/sqlfetch`（v1.1.28+）+ `/sqltune --quick`（v1.1.31）做 Top SQL 深度优化
>   - 关联设计文档：`docs/sqltune/plan-llm-sqltune-routing-fix.md`

---

## 1. 背景与动机

### 1.1 客户痛点

`gs_wdr` / `WDR` 报告是 openGauss / GaussDB 的官方诊断输出，对应 Oracle AWR。一份典型 WDR：
- 大小：300 KB – 5 MB（HTML 或文本）
- 包含 20+ 章节：Time Model / Wait Events / TopSQL × 多视角 / IO / Memory / Locks / Replication / Settings
- DBA 通常要花 30 分钟以上才能读完 + 找出关键问题

**痛点**：
1. 信息密度低 — 90% 内容是 "normal" 噪声，10% 才是关键信号
2. 跨章节关联难看出 — buffer hit ratio 低 ↔ shared_buffers 配置 ↔ Top SQL IO 大三者要一起看
3. TopSQL 给了但**没给优化方案** — DBA 还要单独对每条 SQL 跑 EXPLAIN + 思考
4. 一份 WDR 抓不到结构性问题 — 单点 outlier vs 长期趋势难区分

### 1.2 opendb 已具备的能力（直接复用）

| 能力 | 来自 | 用途 |
|---|---|---|
| `/sqlfetch` | v1.1.28+ | 拿 SQL_ID → 可 EXPLAIN 的 SQL（占位符替换 + schema 补全 + 截断检测）|
| `/sqltune --quick` | v1.1.31 | 5 维度优化方案 + EXPLAIN cost 验证，30-90s 出报告 |
| memory fingerprint | v1.1.30 | 相同 SQL family 的诊断可跨会话/跨模型复用 |
| `/sentinel` + Rule Engine | 已有 | 实时异常检测（不是 WDR 维度但可参考规则设计）|
| LLM 编排（autonomous mode）| 已有 | 把规则 flag + WDR 摘要交给 LLM 综合 |

### 1.3 新能力定位

**`/wdranalyze`** ≠ `/sqltune` ≠ `/health`：

| 工具 | 范围 | 时间维度 | 输出 |
|---|---|---|---|
| `/health` | 实时状态 | 当前一个截面 | 健康指标 + 立即告警 |
| `/sqltune` | 单 SQL | 当前 EXPLAIN | 5 维度优化方案 |
| **`/wdranalyze`** | **整个工作负载** | **过去一段时间窗（如 1h / 24h）** | **风险清单 + Top SQL 全套优化 + 配置建议** |

`/wdranalyze` 是**长时段全维度复盘**，回答"过去 1 小时数据库整体状态如何，关键问题在哪，怎么改"。

---

## 2. 目标与验收

### 2.1 必达功能

- [ ] 命令：`/wdranalyze [模式] [参数]`
  - `/wdranalyze latest` — 用最近两个 snapshot
  - `/wdranalyze <snap_id_start> <snap_id_end>` — 指定快照对
  - `/wdranalyze last1h` / `last24h` / `last7d` — 时间窗
  - `/wdranalyze file <path>` — 解析已有的 WDR HTML/text
- [ ] **生成 + 分析**：能调 `dbe_perf.generate_wdr_report()` 拉一份 WDR 然后分析
- [ ] **结构化解析**：把 WDR 拆成 JSON 节点（Time Model / Wait / TopSQL / IO / Memory / Settings / Locks）
- [ ] **规则层异常检测**：20+ rule 覆盖常见问题（缓冲池命中率、IO 等待、锁等待、配置偏离）
- [ ] **风险分级**：🔴 严重 / 🟡 警告 / 🟢 提示
- [ ] **TopSQL 深度优化**：对前 N 条（默认 5）自动调 `/sqlfetch` + `/sqltune --quick`，把 5 维度方案嵌入到 WDR 报告里
- [ ] **配置调优建议**：基于 wait 占比 + 资源使用，给出 GUC 参数调整建议（shared_buffers / work_mem / bgwriter_delay 等）
- [ ] **输出**：单一 markdown 报告（不切片），含执行路线表（P0/P1/P2）
- [ ] **持久化**：报告存到 `~/.opendb/wdr_reports/<timestamp>-<window>.md`，便于历史回看

### 2.2 验收测试用例

| 用例 | 输入 | 期望 |
|---|---|---|
| **T1 latest 模式** | `/wdranalyze latest` | 自动用最近两个 snapshot，3-10min 内出报告 |
| **T2 指定快照对** | `/wdranalyze 1234 1245` | 同上 |
| **T3 时间窗** | `/wdranalyze last1h` | 自动找过去 1h 跨越的两个 snapshot |
| **T4 已有 WDR 文件** | `/wdranalyze file /tmp/wdr.html` | 解析 + 分析，不调 og |
| **T5 TopSQL 深度** | 任意模式 | 报告里每条 TopSQL 含 sqltune 5 维度方案，EXPLAIN cost diff 来自真实验证 |
| **T6 规则触发准确** | 构造低缓冲命中率场景 | 报告标红"缓冲命中率 X% < 95%" |
| **T7 同 SQL 历史复用** | 跑两次 `/wdranalyze latest` 同窗口 | 第二次 sqltune 应命中 memory 显著加速 |
| **T8 LLM 关掉** | 设 `active_model: none` | 仍输出规则层报告（无 LLM 综合段），不报错 |
| **T9 大 WDR** | 5MB WDR 文件 | token compression 触发，报告仍正常生成 |
| **T10 损坏 WDR** | 截断的 HTML | 给清晰错误"WDR 解析失败：缺少 X 章节"，不崩 |

---

## 3. 架构与数据流

### 3.1 7 阶段流水线

```
用户: /wdranalyze last1h
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 1: Collect                                    │
│   - 解析参数（latest / time-range / 文件路径）       │
│   - 自动找/确认 snapshot 对                          │
│   - 调 dbe_perf.generate_wdr_report() → 拿 HTML/text │
└────────────────────────────────────────────────────┘
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 2: Parse                                      │
│   - 把 WDR 切成 20+ section 的 JSON 结构             │
│   - 标准化字段（数值/百分比/时间）                    │
│   - 抽取所有 SQL_ID / hash + 对应 stats              │
└────────────────────────────────────────────────────┘
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 3: Rule Engine（确定性，快速）                  │
│   - 20+ rule 跑过每个 section                       │
│   - 产出 flag 列表：{severity, category, evidence}   │
│   - 不依赖 LLM，确保失败情况下也有输出                │
└────────────────────────────────────────────────────┘
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 4: TopSQL Drill-Down（并行）                   │
│   - 对前 N（默认 5）个 TopSQL：                       │
│     ├─ /sqlfetch <SQL_ID> → SQL 文本                │
│     ├─ /sqltune --quick → 5 维度优化方案            │
│     └─ 缓存到 result                                │
│   - 并行执行，5 个 SQL 总耗时 ≈ 单个 sqltune（≤ 90s）│
└────────────────────────────────────────────────────┘
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 5: LLM Synthesis（可选，但强推荐）             │
│   - 输入：结构化 WDR 摘要 + 规则 flag + TopSQL 方案  │
│   - LLM 输出：根因因果链 + 风险评估 + 配置建议       │
│   - max_tokens=4000，避免冗长                       │
│   - 失败 fallback 到规则层渲染（同 sqltune QuickMode）│
└────────────────────────────────────────────────────┘
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 6: Render                                     │
│   - 模板化生成 markdown                              │
│   - 嵌入：Time Model 表 / Wait 图 / TopSQL 优化卡片  │
│   - 末尾执行路线表（P0 立即 / P1 低峰 / P2 可选）     │
└────────────────────────────────────────────────────┘
        ↓
┌────────────────────────────────────────────────────┐
│ Phase 7: Persist + Display                          │
│   - 存到 ~/.opendb/wdr_reports/<ts>-<window>.md     │
│   - 终端展示 summary + 文件路径                      │
│   - memory_write: 跨会话保留诊断结果                 │
└────────────────────────────────────────────────────┘
```

### 3.2 关键决策点

| 决策 | 选项 | 选了哪个 + 为什么 |
|---|---|---|
| WDR 来源 | (A) 调 og API 实时生成 / (B) 用户提供已有报告 / (C) 两者都支持 | **C** — 客户场景多样：有时 og 在内网无法远程触发，需要 DBA 提供 HTML |
| 解析方式 | (A) 正则提取 / (B) HTML parser 全解析 / (C) JSON 化结构 | **C** — 先 parse → 转 JSON → 后续 rule/LLM 都对 JSON 操作。鲁棒且可单测 |
| TopSQL 范围 | (A) 仅按 elapsed_time / (B) 按多视角合集 (elapsed/IO/exec/CPU) | **B** — 但取 union 去重，每条只 sqltune 一次 |
| TopSQL 数量 | 3 / 5 / 10 | **5 默认**，`--top-n N` 可调。5 个 sqltune 并行 ≈ 单条耗时 |
| LLM 综合层 | 必需 / 可选 | **可选**，规则层独立可用。`active_model: none` 时降级 |
| memory 集成 | 跨会话复用 sqltune 结果 / 不集成 | **集成** — 同 SQL_ID 在 1 周内再 wdranalyze 自动用上次 sqltune 输出 |
| 报告持久化 | 临时显示 / 存文件 | **存文件** — DBA 习惯导出报告分享给同事 |

---

## 4. 模块结构与代码组织

```
internal/opengauss/skill/query/wdranalyze_skill.go   (新, ~250 行)
    ↓ 调
internal/opengauss/wdranalyze/                       (新包)
  collector.go       Phase 1: 调 dbe_perf.generate_wdr_report
  parser.go          Phase 2: WDR HTML/text → JSON
  rules.go           Phase 3: 规则引擎 + flag 生成
  topsql.go          Phase 4: 并行调 sqlfetch + sqltune
  synthesizer.go     Phase 5: LLM prompt 构造 + 输出解析
  renderer.go        Phase 6: markdown 模板渲染
  store.go           Phase 7: 文件持久化 + memory 集成
  types.go           共享类型定义
```

复用：
- `internal/opengauss/sqltuner/` 整个包（PlanCollector / Substituter / Tuner）
- `internal/engine/memory/` fingerprint + Find / WriteWithSQL

---

## 5. 关键数据结构

```go
// WDR 解析后的结构化形式
type WDRReport struct {
    Header     ReportHeader      // 实例信息 + 窗口
    TimeModel  TimeModelStats    // DB Time / CPU / Wait 分布
    Waits      []WaitEvent       // Top 等待事件
    TopSQLs    []TopSQLEntry     // Top SQL（多视角合并去重）
    IO         IOStats           // IO 统计
    Memory     MemoryStats       // 内存使用
    Locks      LockStats         // 锁等待
    Replication ReplicationStats // 主备
    Settings   map[string]string // 关键 GUC 参数
    Raw        string            // 原始报告（备查）
}

type TopSQLEntry struct {
    SQLID         string
    Source        []string  // ["elapsed", "io", "exec_count"] - 来自哪些视角
    Calls         int64
    AvgTimeMS     float64
    TotalTimeMS   float64
    AvgIO         int64
    Rows          int64
    QueryPrefix   string    // 前 100 字符
}

// 规则层输出
type Finding struct {
    Severity     Severity   // Critical / Warning / Info
    Category     string     // "buffer" / "wait" / "sql" / "config" / "lock"
    Title        string
    Evidence     []string   // 数值证据
    Suggestion   string
    EvidenceData map[string]any // 给 LLM 用的结构化数据
}

// Top SQL 优化结果
type SQLTuneResult struct {
    SQLID         string
    FullSQL       string                  // 来自 sqlfetch
    Plan          *sqltuner.PlanInfo
    Candidates    []sqltuner.Candidate    // 来自 sqltune
    BestSpeedup   float64
    Fingerprint   memory.Fingerprint
    FromMemory    bool                    // 是否命中历史 memory
}

// 完整分析结果（最终 render 用）
type WDRAnalysis struct {
    Report        *WDRReport
    Findings      []Finding
    SQLTunes      []SQLTuneResult
    LLMSynthesis  string        // LLM 综合段，可为空
    GeneratedAt   time.Time
    Duration      time.Duration
}
```

---

## 6. 规则集（Phase 3）

20+ 规则，按 category 分组。每条规则：
- `id` (唯一)
- `severity_threshold` (Critical / Warning / Info)
- `condition` (函数，输入 WDRReport，输出是否触发 + 证据)
- `suggestion_template` (建议文案模板)

| 类别 | rule_id | 触发条件 | 严重度 | 建议 |
|---|---|---|---|---|
| buffer | buffer_hit_low | shared buffer hit < 95% | 🟡 / 🔴 (< 90%) | "shared_buffers 可能不足，建议 +50%" |
| buffer | local_hit_low | local buffer hit < 90% | 🟡 | "work_mem / sort 性能不佳" |
| wait | top_wait_dominant | 单一 wait event > 30% DB Time | 🔴 | "{wait_name} 是主要瓶颈" |
| wait | io_wait_high | io wait > 25% DB Time | 🟡 | "IO 子系统饱和，检查存储" |
| wait | lock_wait_high | lock wait > 10% DB Time | 🔴 | "存在大量锁竞争" |
| wait | network_wait_high | network wait > 15% DB Time | 🟡 | "客户端往返过多或带宽不足" |
| sql | top_sql_avg_slow | TopSQL avg_time > 1s | 🟡 | "候选 sqltune 优化" |
| sql | top_sql_total_dominant | 单 SQL 总耗时 > 30% DB Time | 🔴 | "{sql_id} 占用主要资源" |
| sql | top_sql_exec_count_high | 单 SQL exec > 100K | 🟡 | "执行频率过高，考虑结果缓存" |
| sql | hard_parse_ratio | hard_parse / total > 20% | 🟡 | "硬解析比例偏高，缺乏 prepared statement" |
| io | read_write_imbalance | reads / writes > 100 | 🟡 | "读密集型，考虑读副本" |
| io | wal_write_high | WAL writes > N MB/s | 🟡 | "WAL 流量大，检查 commit 频率" |
| memory | gs_total_memory_pressure | used / max > 85% | 🔴 | "总内存压力大" |
| memory | session_memory_outlier | 单会话 > 1GB | 🟡 | "找出大内存会话" |
| lock | lwlock_wait_high | lwlock_wait_time > N ms | 🟡 | "内部锁竞争" |
| lock | deadlock_count | deadlock_count > 0 | 🔴 | "发生死锁 N 次" |
| replication | replication_lag_high | lag > 60s | 🔴 | "主备延迟严重" |
| config | shared_buffers_low | shared_buffers / total_ram < 25% | 🟡 | "shared_buffers 配置偏低" |
| config | work_mem_excess | work_mem > 256MB | 🟡 | "work_mem 过大可能导致 OOM" |
| config | autovacuum_off | autovacuum = off | 🔴 | "autovacuum 已关闭，bloat 风险" |
| general | uptime_short | 启动 < 1h | 🟢 | "实例刚启动，统计未稳定" |

规则配置文件路径：`internal/opengauss/wdranalyze/rules.go`（Go 代码）+ 可选 `~/.opendb/wdranalyze-rules.yaml` 覆盖阈值。

---

## 7. TopSQL 深度优化集成（Phase 4）

```go
func (a *Analyzer) tuneTopSQLs(ctx context.Context, topSQLs []TopSQLEntry, topN int) []SQLTuneResult {
    if topN > len(topSQLs) { topN = len(topSQLs) }
    
    results := make([]SQLTuneResult, topN)
    var wg sync.WaitGroup
    
    for i := 0; i < topN; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            entry := topSQLs[idx]
            
            // 1. /sqlfetch <SQL_ID>: 拿带字面量 + schema 的可 EXPLAIN SQL
            fullSQL, schema, err := a.sqlfetch.Resolve(ctx, entry.SQLID)
            if err != nil { 
                results[idx] = SQLTuneResult{SQLID: entry.SQLID, Error: err.Error()}
                return 
            }
            
            // 2. fingerprint 查 memory: 同 SQL 最近是否调过 sqltune
            fp := memory.ComputeFingerprint(fullSQL)
            if cached := a.memStore.FindByFingerprint(fp); cached != nil {
                results[idx] = SQLTuneResult{
                    SQLID: entry.SQLID, FullSQL: fullSQL,
                    FromMemory: true, Candidates: cached.Candidates,
                }
                return
            }
            
            // 3. /sqltune --quick: 30-90s 出 5 维度方案
            report, err := a.tuner.Tune(ctx, sqltuner.TuneOptions{
                SQL: fullSQL, Verify: true,
                QuickMode: true, SkipUpgrade: true,
            })
            // ... 转换为 SQLTuneResult, 写 memory ...
        }(i)
    }
    wg.Wait()
    return results
}
```

并行做 5 个 sqltune，整体耗时 ≈ 单条最慢（90s），不是 5 × 90s = 450s。

---

## 8. LLM 综合层 Prompt 设计（Phase 5）

```go
systemPrompt := `你是 openGauss/GaussDB 性能诊断专家。基于下面的 WDR 结构化数据和规则引擎已经触发的 flag，写一段精炼的根因因果链 + 配置调优建议。

输出格式：
## 工作负载特征
[一两句话总结：读密集 / 写密集 / OLTP / OLAP / 混合]

## 关键瓶颈
[基于 wait 占比 + Top SQL，指出 2-3 个核心瓶颈，每个一句话]

## 配置调优
[2-5 条 GUC 调整建议，每条附 EVIDENCE: "因为 X 现状 Y" 的逻辑链]

## 综合评估
[一段话：当前状态打分 + 1-2 周行动建议]

约束：
- 引用的所有数值必须来自下方 WDR 数据，禁止虚构
- 配置建议必须有 evidence 链路（不是"shared_buffers 调大"，要"shared_buffers 当前 4GB，DB Time 中 buffer wait 占 25% → 建议提升至 8GB"）
- 不重复 TopSQL 优化建议（那部分已由 sqltune 单独输出）
- 不超过 600 字
`

userMsg := buildUserMessage(report, findings, sqlTunes)
// userMsg 含: 报告窗口 / Time Model 表 / Top 5 wait 占比 / 配置摘要 / 触发的 flags 列表
// 不传 raw WDR 全文（太大）
```

---

## 9. 输出格式（最终 markdown 模板）

```markdown
# WDR 分析报告

> 报告窗口: 2026-05-16 10:00 ~ 11:00 (1h)
> 实例: GaussDB · 82.4.89.165 (instance_id=1)
> 生成耗时: 2m 47s
> 报告文件: ~/.opendb/wdr_reports/20260516-100000-1h.md

## 工作负载特征
[LLM 综合段]

## 风险全景

| 严重度 | 数量 | 类别分布 |
|---|---|---|
| 🔴 严重 | 2 | wait × 1, config × 1 |
| 🟡 警告 | 4 | sql × 2, io × 1, buffer × 1 |
| 🟢 提示 | 3 | general × 3 |

### 🔴 严重 (2)

#### 1. 等待事件 lock_wait_acquire 占 DB Time 45.2%
**证据**:
- DB Time: 8h 15min (= 29,700s)
- Top wait: lock_wait_acquire = 13,418s (45.2%)
- 涉及表: customers (P1), orders (P2)
**建议**: 见 Top SQL #1 (重写消除全表锁) + 索引方案

#### 2. autovacuum 已关闭
**证据**: `autovacuum = off` in pg_settings
**建议**: 立即开启 `ALTER SYSTEM SET autovacuum = on; SELECT pg_reload_conf();`

### 🟡 警告 (4)
[... 类似格式]

### 🟢 提示 (3)
[... 略]

## Top SQL 优化建议 (5/8)

### #1 SQL_ID 1234567890 — 占总耗时 32.1%
- **统计**: 234 calls · 平均 17.7s · 总 4,142s
- **特征**: 5 表 JOIN + TO_CHAR 函数包列 + NOT IN 反模式

**sqltune 5 维度方案**（实测 cost 21,936 → 1,000-2,000，**~22× 提升**）：

```sql
-- 方案 1（P0 立即）: SQL 重写
SELECT c.name, p.product_name, SUM(...)
FROM customers c JOIN orders o ON ... JOIN order_items oi ON ...
WHERE o.order_date >= '2024-01-15' AND o.order_date < '2024-01-16'  -- 替代 TO_CHAR
  AND NOT EXISTS (SELECT 1 FROM shipments WHERE order_id = o.order_id AND status = 'cancelled')
GROUP BY ... LIMIT 100;

-- 方案 2（P1 低峰）: 索引补全
CREATE INDEX CONCURRENTLY idx_orders_order_date ON sqltune_demo.orders(order_date);
CREATE INDEX CONCURRENTLY idx_order_items_oid_pid ON sqltune_demo.order_items(order_id, product_id);
```

### #2 SQL_ID ... — 占总耗时 18.7%
[类似格式]

## 配置调优建议

[LLM 综合段 - 3-5 条 GUC 调整]

## 执行路线（推荐顺序）

| 优先级 | 操作 | 预期收益 | 耗时 | 风险 |
|---|---|---|---|---|
| **P0** | 开 autovacuum | bloat 防扩大 | 5 min | 零 |
| **P0** | Top SQL #1 SQL 重写 | 22× | 5 min | 零 DDL |
| **P1** | 7 条索引 (低峰期) | 综合 30%+ | 30-60 min | 写入开销略增 |
| **P2** | shared_buffers 4G → 8G | buffer hit 95%→99% | 重启需窗口 | 需停机 |

---

> 历史对比: 上次 wdranalyze (2026-05-15 同时段) 严重数 5 → 现在 2，整体趋势改善 ✓
> Top SQL #2 在历史诊断中已识别但未实施，建议本轮纳入 P0
```

---

## 10. 实施分期

| 阶段 | 内容 | 工作量 | 验收 |
|---|---|---|---|
| **M1 骨架** | skill 注册 + Collector + Parser（仅 Time Model / Wait / TopSQL 三个核心 section）+ 简单 markdown 输出 | 2-3 天 | T1/T2 通过，能跑 latest 模式输出基本报告 |
| **M2 规则引擎** | 20+ rules + Findings 结构 + 严重度分级 | 2 天 | T6 通过，规则准确触发 |
| **M3 TopSQL 集成** | Phase 4 并行调 sqlfetch + sqltune + memory 集成 | 2 天 | T5 / T7 通过，TopSQL 含完整 sqltune 方案 |
| **M4 LLM 综合** | Phase 5 + prompt 调优 + token compression | 1-2 天 | T8 / T9 通过，关 LLM 也能用，大 WDR 不爆 |
| **M5 持久化 + 历史对比** | Phase 7 + 跟历史报告做 diff | 1 天 | 末尾出现"历史对比"段 |
| **M6 文档 + 测试** | README + 单测 + 端到端 demo | 1 天 | 跑过 GaussDB 真实 demo |

**总工作量**：约 10-12 天单工程师全栈实施。

---

## 11. 开放问题（先讨论再动手）

1. **WDR 生成的权限要求**：
   - `dbe_perf.generate_wdr_report()` 需要 `SELECT` on 多张系统表
   - 客户的 monitoradmin 角色是否够？要不要文档明确写出所需权限？
   - 建议：MVP 默认假设客户有 monitoradmin，权限不足时给清晰错误

2. **HTML vs Text 报告格式**：
   - `dbe_perf.generate_wdr_report(..., 'html', ...)` 输出 HTML
   - `dbe_perf.generate_wdr_report(..., 'text', ...)` 输出 text
   - 哪个解析更稳？建议都支持，优先 text（解析简单），HTML 当兜底
   - 用户提供文件时根据 file extension 自动判断

3. **TopSQL 数量与 sqltune 总耗时**：
   - 默认 5 个并行：~90s
   - 默认 10 个并行：~120s（受 LLM 并发限制）
   - 客户大概期望全 wdranalyze 控制在 5 分钟内
   - 建议：默认 5，`--top-n` 可调，超过 10 给警告

4. **跟 OG L1/L2 跟踪模式的耦合**：
   - 如果客户 og 没开 `track_stmt_stat_level=L1+`，dbe_perf.statement_history 空 → sqlfetch 拿不到 TopSQL 文本
   - 第一次跑 wdranalyze 自动检测并提示客户开 L1
   - 或者：直接从 WDR 报告里的 TopSQL 章节拿 SQL 文本（WDR 里有归一化 SQL）— **更稳，绕过 dbe_perf 依赖**

5. **HTML 解析复杂度**：
   - WDR HTML 是固定模板（来自 og 官方），不会变化大
   - 用 `goquery` 类库还是手写 regex？
   - 建议：goquery 更稳，加 1 个依赖可接受

6. **历史对比能力是否 MVP 必需**：
   - 价值高但 M5 才到，MVP 可以先不做
   - 如果时间紧可以延后到 v1.2

---

## 12. 风险与降级

| 风险 | 影响 | 降级 |
|---|---|---|
| 客户机 og 版本太老，`generate_wdr_report` 接口不存在 | 完全不能生成 WDR | 报错指引：换用 `gs_wdr` 命令行工具生成 + `/wdranalyze file <path>` 解析 |
| WDR HTML 模板变化（og 升级后）| 解析挂 | parser 加版本探测 + fallback 到 text 格式 |
| sqltune 撞 600s 超时 | TopSQL 缺方案 | Phase 4 改并发但单 sqltune 用 quick mode（已支持）；超时该 SQL 单独 fallback 到"原 SQL + plan summary" |
| LLM 不可用（model=none / 网络断）| 缺综合段 | 走 fallback 渲染（已设计），只输出规则层 + sqltune 结构化 |
| 客户 WDR 报告超大（10+ MB）| LLM 爆 token | Phase 5 加 token compression + 摘要级 prompt |
| dbe_perf.statement_history 是空（客户未开 L1+）| sqlfetch 拿不到完整 SQL | 改从 WDR 报告里的 TopSQL section 拿 SQL（fallback） |

---

## 13. 与现有功能的关系

```
┌──────────────────────────────────────────────────┐
│  /wdranalyze                                      │
│  (新, 工作负载级长时段分析)                          │
│    │                                              │
│    ├─ 调用 /sqlfetch (per TopSQL)                  │
│    ├─ 调用 /sqltune --quick (per TopSQL)           │
│    ├─ 复用 memory fingerprint (跨 wdranalyze 复用) │
│    ├─ 复用 og rule engine 部分思路 (但规则集独立)   │
│    └─ 输出: ~/.opendb/wdr_reports/<ts>.md          │
│                                                   │
│  /wdr (旧, 保留)                                   │
│    └─ 列 snapshot + 指引手动生成                   │
│                                                   │
│  /sqltune (单 SQL 调优)                            │
│  /sentinel (实时异常)                              │
│  /health (实时截面)                                │
└──────────────────────────────────────────────────┘
```

四个工具职责互补，**`/wdranalyze` 是"过去一段时间的整体复盘"**，跟实时类工具配合形成完整诊断能力闭环。

---

## 14. 待用户决断的几个抉择

1. **新 skill 名字**：`/wdranalyze`（描述准）vs `/wdrtune`（与 sqltune 配对）vs `/wdra`（最短）？
2. **MVP 范围**：M1+M2+M3+M4 一起发，还是分两版（M1+M2 先发，M3+M4 后续）？
3. **跟 og 的 `gs_wdr` 命令行工具关系**：opendb 内部生成（dbe_perf API），还是要求客户先用 gs_wdr 生成再 opendb 解析？两者都支持就行？
4. **报告语言**：固定中文 / 跟随用户 locale / 双语？dbaa / linkdb 客户大概率要中文
5. **跟 `/sentinel` 联动**：sentinel 触发告警时是否自动调 wdranalyze？还是 wdranalyze 完全人工触发？
6. **TopSQL 失败处理**：单条 SQL 的 sqltune 失败时，是否阻塞整个 wdranalyze？还是其他 SQL 继续，失败的标 ⚠️ 跳过？建议后者

待你决断后开工。
