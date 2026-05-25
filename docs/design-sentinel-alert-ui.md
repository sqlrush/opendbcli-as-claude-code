# Sentinel 实时告警 + 一键诊断 交互设计

**日期**: 2026-03-13
**状态**: 设计完成，待实现

---

## 1. 核心问题

Sentinel 在后台检测到异常后，用户完全不知道 —— 必须手动 `/sentinel status` 才能看到。
需要设计一个**主动通知 + 一键诊断**的交互流程。

## 2. 用户体验流程

### 2.1 告警推送 (非阻断)

用户正常使用 opendb，sentinel 在后台默默监控。当检测到异常时，在 REPL 中插入告警条：

```
opendb > SELECT * FROM employees WHERE dept = 'IT'

┌─ 结果 (12 行) ──────────────────────────────────┐
│ EMP_ID  NAME       SALARY   HIRE_DATE            │
│ 1001    张三       15000    2024-01-15           │
│ ...                                              │
└──────────────────────────────────────────────────┘

⚠ [13:32:21] 性能异常: 活跃会话突增 12→28 (3σ=18)
  根因: 锁等待阻塞 (70%) | 峰值: 28 | 持续: 11s
  阻塞链: SID 1002→3个等待, SID 1142→3个等待
  输入 /diag 一键 AI 诊断

opendb > _

(用户继续正常输入，告警不阻断操作)
```

### 2.2 一键诊断 (/diag)

```
opendb > /diag

⏳ 正在调用 AI 诊断 (qwen3.5:9b)...

┌─ AI 诊断结果 ──────────────────────────────────────┐
│                                                    │
│ ## 根因分析                                        │
│ TX行锁竞争: UPDATE opendb_lock_test 高并发更新     │
│ 同一主键范围，SID 1002/1142 各阻塞 3 个会话        │
│                                                    │
│ ## 紧急措施                                        │
│ ① 查阻塞链:                                       │
│   SELECT sid, serial#, blocking_session             │
│   FROM v$session WHERE event LIKE '%TX%';           │
│ ② kill 阻塞源: ALTER SYSTEM KILL SESSION '1002,#'; │
│                                                    │
│ ## 长期修复                                        │
│ ① 缩小事务粒度，减少锁持有时间                     │
│ ② 添加索引: CREATE INDEX ... ON opendb_lock_test   │
│                                                    │
│ 按 F 查看完整报告 | 按 1~3 执行推荐 SQL            │
└────────────────────────────────────────────────────┘

opendb > _
```

## 3. 告警条格式

三行结构：一行摘要 + 一行详情 + 一行操作提示

```
⚠ [时间] 性能异常: {触发原因} {基线}→{当前} (阈值={threshold})
  根因: {分类} ({置信度}%) | 峰值: {peak} | 持续: {duration}s
  输入 /diag 一键 AI 诊断
```

## 4. /diag 快捷命令

```
/diag           → 自动取 sentinel 最近一次报告，playbook 模式
/diag -m assist → assist 模式 (3轮，可调用工具)
/diag "为什么慢" → 带自定义问题
```

## 5. 默认开启策略

Sentinel 登录后默认开启，使用**轻量探针模式**降低开销：

### 5.1 两级采集

| 模式 | 频率 | SQL 数量 | 用途 |
|------|------|---------|------|
| 轻探针 (日常) | 每 1s | 1 条 | `SELECT COUNT(*) FROM v$session WHERE STATUS='ACTIVE' AND TYPE='USER'` |
| 完整采集 (触发后) | 每 200ms | ~10 条 | 完整 Collect()，拿会话/等待/阻塞链/SQL 详情 |

### 5.2 负担评估

- 全是 v$ 内存视图查询，不走磁盘 I/O
- `v$session` COUNT 通常 < 1ms
- Oracle AWR 自身每秒内部采样量远超此量
- 轻探针模式：**1 条 SQL/秒，开销可忽略**

### 5.3 配置项

```yaml
# ~/.opendb/config.yaml
sentinel:
  auto_start: true         # 登录后自动启动 (默认 true)
  probe_interval: 1s       # 轻探针间隔 (默认 1s)
  sigma_threshold: 3       # 触发阈值 (默认 3σ)
  cooldown: 5m             # 冷却时间 (默认 5 分钟)
  burst_interval: 200ms    # burst 采集间隔
  burst_max_duration: 30s  # burst 最长持续时间
```

## 6. 技术实现

### 6.1 告警推送机制

```
sentinel 检测到异常
  → OnReport 回调
  → 写入 alertCh (chan AlertEvent)
  → REPL 主循环在读键盘的同时 select alertCh
  → 收到告警 → 在当前光标下方插入告警条
  → 用户输入不受影响
```

### 6.2 REPL 主循环改造

```go
// 当前: 阻塞读键盘
key := readKey()

// 改造后: select 键盘 + 告警
select {
case key := <-keyCh:
    handleKey(key)
case alert := <-alertCh:
    renderAlert(alert)  // 插入告警条，不影响输入行
}
```

### 6.3 轻量探针

新增 `ProbeCollector`，只查活跃会话数：

```go
type ProbeCollector struct {
    driver db.Driver
}

func (p *ProbeCollector) Probe(ctx context.Context) int {
    row := p.driver.QueryRow(ctx, "SELECT COUNT(*) FROM v$session WHERE STATUS='ACTIVE' AND TYPE='USER'")
    var count int
    row.Scan(&count)
    return count
}
```

Sentinel 日常用 ProbeCollector，触发后切换到完整 Collector。

### 6.4 需要改动的文件

| 文件 | 改动 |
|------|------|
| `internal/sentinel/sentinel.go` | OnReport 回调改为写 channel；新增轻探针模式 |
| `internal/sentinel/probe.go` | 新增：轻量探针，只查活跃会话数 |
| `internal/skill/builtin/ai/sentinel_skill.go` | 暴露 AlertCh 给 REPL；支持自动启动 |
| `internal/ui/repl.go` | 主循环 select alertCh；renderAlert() 插入告警条 |
| `internal/skill/builtin/ai/diagnose_skill.go` | 无参数时自动取最近报告 |
| `internal/config/config.go` | 新增 sentinel 配置项 |
| `cmd/opendb/main.go` | 登录后自动启动 sentinel |

## 7. 轻探针发现异常后的完整策略链

```
每秒: 轻探针 (2 SQL, 7 指标)
  │
  ├─ 正常 → 更新基线，继续
  │
  └─ 任一指标超阈值 → 触发!
        │
        ▼
  ┌─ Burst 突发采集 ─────────────────────────┐
  │ 切换到完整 Collector (~10 SQL/200ms)      │
  │ 采集内容:                                 │
  │   · 全部活跃会话 (SID/SQL_ID/等待事件)     │
  │   · 阻塞链 (blocking_session)             │
  │   · SQL 文本 + 执行计划                   │
  │   · 等待事件 TOP 10                       │
  │   · SGA/PGA/TPS/QPS/Redo                  │
  │                                           │
  │ 持续条件: 活跃会话 > 基线 (最长30s)        │
  │ 恢复条件: 连续5s低于基线 → 结束采集        │
  └───────────────────────────────────────────┘
        │
        ▼
  ┌─ Analyze 规则分析 ───────────────────────┐
  │ 输入: 所有 burst 帧                       │
  │ 输出: 分类 + 置信度                       │
  │                                           │
  │ 6种根因:                                  │
  │   bad_sql       → 某条SQL并发暴涨         │
  │   io_subsystem  → I/O等待占比高            │
  │   latch_storm   → Latch/Mutex争用         │
  │   redo_bottleneck → log file sync等待      │
  │   lock_contention → TX行锁阻塞链          │
  │   traffic_storm → 连接数暴涨              │
  └───────────────────────────────────────────┘
        │
        ▼
  ┌─ Alert 推送到 REPL ─────────────────────┐
  │ ⚠ [13:32:21] 性能异常: 活跃会话突增 12→28│
  │   根因: 锁等待阻塞 (70%) | 峰值: 28      │
  │   输入 /diag 一键 AI 诊断                 │
  └───────────────────────────────────────────┘
        │
        ├─ 用户不理 → 报告缓存，5分钟冷却后恢复监控
        │
        └─ 用户输入 /diag
              │
              ▼
        ┌─ CompressReport ──────────────────┐
        │ BurstReport → ~1000 token 文本     │
        │ 含: 指标摘要/等待分布/TopSQL/阻塞链 │
        └───────────────────────────────────┘
              │
              ▼
        ┌─ Ollama/qwen3.5:9b ──────────────┐
        │ 输入: system prompt + 压缩报告     │
        │ 输出: 根因分析 + 紧急措施 + 长期修复│
        └───────────────────────────────────┘
              │
              ▼
        ┌─ 展示诊断结果 ───────────────────┐
        │ AI分析 + 推荐SQL + 修复建议       │
        │ 按 1~3 可直接执行推荐SQL (TODO)   │
        └───────────────────────────────────┘
```

核心思想：轻探针只负责"发现有没有问题"，不负责"问题是什么"。
确认有问题后才启动重采集拿详细数据，再走规则分类 → 告警 → AI诊断。

## 8. 调用链路

```
登录 opendb
  → auto_start: sentinel.Start() (轻探针模式)
  → 每秒: ProbeCollector.Probe() — 1 条 SQL
  → 检测: active > baseline + 3σ
  → 触发: 切换到 Collector.Collect() (完整采集)
  → Burst: 8帧 × 200ms，收集会话/等待/阻塞链
  → Analyze: 分类根因 (6 种类型)
  → alertCh ← AlertEvent
  → REPL: renderAlert() — 插入 3 行告警条
  → 用户: /diag
  → CompressReport → LLM Prompt → Ollama/qwen3.5:9b
  → renderDiagnosis() — 展示诊断结果
```
