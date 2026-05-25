# Sentinel 触发策略源码分析

> 基于 `internal/oracle/sentinel/` 源码的完整分析。
> 分析日期: 2026-04-15

## 目录

- [1. 整体架构](#1-整体架构)
- [2. 探针三层频率](#2-探针三层频率)
- [3. T1-T9 九大检测策略](#3-t1-t9-九大检测策略)
- [4. 3σ 阈值计算详解](#4-3σ-阈值计算详解)
- [5. SoftAbsoluteMin 详解](#5-softabsolutemin-详解)
- [6. 抑制与恢复机制](#6-抑制与恢复机制)
- [7. 策略分配总览](#7-策略分配总览)
- [8. 为什么不同指标用不同策略](#8-为什么不同指标用不同策略)

---

## 1. 整体架构

### FSM 状态机

```
                    ┌──────────────┐
                    │   StateIdle  │  收集样本，等 baseline 就绪
                    └──────┬───────┘
                           │ samples >= 10 (MinSamples)
                    ┌──────▼───────┐
              ┌────▶│  StateWatch  │  每 tick 跑 T1~T9 检测
              │     └──────┬───────┘
              │            │ 触发
              │     ┌──────▼───────┐
              │     │  StateBurst  │  200ms 高频采集，最长 30s
              │     └──────┬───────┘
              │            │ burst 结束
              │     ┌──────▼───────┐
              └─────│ StateCooldown│  冷却 5 分钟
                    └──────────────┘
```

**源码**: `sentinel.go:17-37`

- **StateIdle**: 收集样本建立 baseline，样本数 >= MinSamples(10) 后进入 Watch
- **StateWatch**: baseline 就绪，每 tick 运行 T1~T9 检测，发现异常进入 Burst
- **StateBurst**: BurstController 独立运行高频采集（200ms/帧，最长 30s）
- **StateCooldown**: burst 结束后冷却 5 分钟，防止级联触发

### 核心数据结构

**Sentinel**（`sentinel.go:51-77`）:
```go
type Sentinel struct {
    cfg              Config
    probe            ProbeFunc           // 全量采集（burst 模式用）
    probeCollector   *ProbeCollector     // 轻量探针（watch 模式用）
    mediumCollector  *MediumProbeCollector
    slowCollector    *SlowProbeCollector
    detector         *DetectorState      // T3-T9 检测状态
    baseline         Baseline            // 滚动统计
    state            State               // FSM 当前状态
    lastTrigger      time.Time           // 上次触发时间（冷却用）
    sustainedCounts  map[MetricName]int  // 连续超标计数
    suppressed       map[MetricName]float64 // 已触发待恢复的指标
    tickCount        int64               // tick 计数（控制 10/30 探针频率）
}
```

**Baseline**（`types.go:139-148`）:
```go
type Baseline struct {
    Samples   []SentinelSample              // 环形缓冲，最大 Window(60) 个
    Window    int                           // 默认 60 个样本
    AvgActive float64                       // 滚动均值（兼容字段）
    StdActive float64                       // 滚动标准差（兼容字段）
    Ready     bool                          // samples >= MinSamples(10) 时为 true
    Metrics   map[MetricName]*MetricBaseline // 每个指标独立的均值和标准差
}
```

**默认配置**（`types.go:372-395`）:
```
PollInterval:   1s        探针间隔
BaselineWindow: 60        滚动窗口（60 个样本 = 60 秒）
MinSamples:     10        baseline 就绪最小样本数
SigmaThreshold: 3.0       σ 倍数
SustainedCount: 3         连续超标次数
BurstInterval:  200ms     burst 帧间隔
BurstDuration:  30s       burst 最大时长
BurstCalmDelay: 5s        确认恢复延迟
CooldownPeriod: 5min      冷却期
```

### tick 主循环

**源码**: `sentinel.go:172-274`

每个 tick（默认 1 秒）执行：

1. **采集 Fast 层指标**: probeCollector.Probe() → 2 条 SQL
2. **PopulateValues()**: 将 7 个 Fast 指标写入 sample.Values map
3. **条件采集 Medium/Slow**:
   - tickCount % 10 == 0 → mediumCollector.Probe()
   - tickCount % 30 == 0 → slowCollector.Probe()
4. **FSM 状态处理**:
   - StateIdle → pushSample，检查 Ready
   - StateWatch → pushSample → checkSuppressedRecovery → detectAnomaly(T1+T2) → DetectExtended(T3-T9)
   - StateBurst → 什么都不做（BurstController 独立运行）
   - StateCooldown → checkSuppressedRecovery → 检查冷却时间是否到期

---

## 2. 探针三层频率

### Fast 层（1s，每 tick）

**来源**: `probe.go` — 2 条 SQL

| # | 指标 | MetricName | 单位 | SQL 来源 | 计算方式 |
|---|------|-----------|------|---------|---------|
| 1 | 活跃会话 | `active_sessions` | 个 | v$session WHERE ACTIVE AND USER AND 非Idle | COUNT(*) |
| 2 | CPU 会话 | `cpu_sessions` | 个 | 同上 | WAIT_CLASS IS NULL |
| 3 | I/O 等待 | `io_sessions` | 个 | 同上 | WAIT_CLASS IN ('User I/O','System I/O') |
| 4 | 锁等待 | `lock_sessions` | 个 | 同上 | WAIT_CLASS = 'Application' |
| 5 | 慢 SQL | `long_sql` | 个 | 同上 | SQL_EXEC_START > 30s |
| 6 | Redo 生成率 | `redo_rate` | KB/s | v$sysstat 'redo size' | delta / elapsed |
| 7 | 硬解析率 | `hard_parse_rate` | /s | v$sysstat 'parse count (hard)' | delta / elapsed |

**SQL 开销**: 2 条轻量 SQL（1 条 v$session 聚合 + 1 条 v$sysstat 取 2 个累计值做差值）。

**探针 SQL**（`probe.go:21-28`）:
```sql
SELECT
  COUNT(*) AS active,
  SUM(CASE WHEN WAIT_CLASS IS NULL THEN 1 ELSE 0 END) AS on_cpu,
  SUM(CASE WHEN WAIT_CLASS IN ('User I/O','System I/O') THEN 1 ELSE 0 END) AS io_wait,
  SUM(CASE WHEN WAIT_CLASS = 'Application' THEN 1 ELSE 0 END) AS lock_wait,
  SUM(CASE WHEN SQL_EXEC_START IS NOT NULL AND (SYSDATE - SQL_EXEC_START)*86400 > 30 THEN 1 ELSE 0 END) AS long_sql
FROM v$session
WHERE STATUS = 'ACTIVE' AND TYPE = 'USER' AND WAIT_CLASS <> 'Idle'
```

**Sysstat SQL**（`probe.go:31-32`）:
```sql
SELECT name, value FROM v$sysstat
WHERE name IN ('redo size', 'parse count (hard)')
```

### Medium 层（10s，tickCount % 10 == 0）

**来源**: `probe_tier.go` — 5 条 SQL + 6 个 stub

#### 已实现（16 个）

| # | 指标 | MetricName | 单位 | SQL 来源 | 计算方式 |
|---|------|-----------|------|---------|---------|
| 1 | 物理读速率 | `physical_read_rate` | /s | v$sysstat 'physical reads' | delta / elapsed |
| 2 | 逻辑读速率 | `logical_read_rate` | /s | v$sysstat 'session logical reads' | delta / elapsed |
| 3 | 提交速度 TPS | `commit_rate` | /s | v$sysstat 'user commits' | delta / elapsed |
| 4 | 队列等待 | `enqueue_wait_time_ms` | ms | v$sysstat 'enqueue waits' | delta（次数差值） |
| 5 | 死锁 | `enqueue_deadlocks` | 次 | v$sysstat 'enqueue deadlocks' | delta |
| 6 | 新建会话速率 | `session_creation_rate` | /s | v$sysstat 'logons cumulative' | delta / elapsed |
| 7 | 总会话数 | `total_sessions` | 个 | SELECT COUNT(*) FROM v$session WHERE USER | 直接值 |
| 8 | 并行查询会话 | `pq_sessions` | 个 | SELECT COUNT(*) FROM v$px_session | 直接值 |
| 9 | 后台进程等待 | `background_wait` | 个 | v$session WHERE BACKGROUND AND ACTIVE | 直接值 |
| 10 | 阻塞链 | `blocking_chains` | 层 | v$session WHERE BLOCKING_SESSION IS NOT NULL | COUNT |
| 11 | Mutex 等待 | `mutex_wait_sessions` | 个 | v$session WHERE Concurrency AND ACTIVE | COUNT |
| 12 | 日志同步延迟 | `log_file_sync_avg_us` | μs | v$system_event 'log file sync' | deltaMicro / deltaWaits |
| 13 | 单块读延迟 | `db_file_seq_read_avg_us` | μs | v$system_event 'db file sequential read' | deltaMicro / deltaWaits |
| 14 | 多块读延迟 | `db_file_scat_read_avg_us` | μs | v$system_event 'db file scattered read' | deltaMicro / deltaWaits |
| 15 | OS 负载 | `os_load` | - | v$osstat WHERE STAT_NAME='LOAD' | 直接值 |
| 16 | Latch Miss 率 | `latch_free_rate` | % | v$latch SUM(gets), SUM(misses) | misses/gets × 100 |

**Medium SQL**:

```sql
-- mediumSysstatSQL: 吞吐量 delta 指标
SELECT name, value FROM v$sysstat
WHERE name IN ('physical reads', 'session logical reads', 'user commits',
               'enqueue waits', 'enqueue deadlocks', 'logons cumulative')

-- mediumSessionSQL: 会话维度聚合
SELECT
  (SELECT COUNT(*) FROM v$session WHERE TYPE='USER') AS total_sessions,
  (SELECT COUNT(*) FROM v$px_session) AS pq_sessions,
  (SELECT COUNT(*) FROM v$session WHERE TYPE='BACKGROUND' AND STATUS='ACTIVE' AND WAIT_CLASS<>'Idle') AS bg_wait,
  (SELECT COUNT(*) FROM v$session WHERE BLOCKING_SESSION IS NOT NULL) AS blocking_count,
  (SELECT COUNT(*) FROM v$session WHERE STATUS='ACTIVE' AND WAIT_CLASS='Concurrency' AND WAIT_CLASS<>'Idle') AS mutex_wait
FROM dual

-- mediumWaitEventSQL: 等待事件延迟
SELECT event, total_waits, time_waited_micro
FROM v$system_event
WHERE event IN ('log file sync', 'db file sequential read', 'db file scattered read')
AND wait_class <> 'Idle'

-- mediumOSLoadSQL
SELECT VALUE FROM v$osstat WHERE STAT_NAME = 'LOAD'

-- mediumLatchSQL
SELECT SUM(gets) AS total_gets, SUM(misses) AS total_misses FROM v$latch
```

#### 未实现（Stub = 0）

| # | 指标 | MetricName | 单位 | 状态 |
|---|------|-----------|------|------|
| 17 | 行锁等待 | `row_lock_wait_time_ms` | ms | stub = 0 |
| 18 | 网络往返延迟 | `network_round_trip_us` | μs | stub = 0 |
| 19 | Top SQL 耗时偏移 | `top_sql_elapsed_drift` | 倍 | stub = 0 |
| 20 | 全表扫描速率 | `full_scan_rate` | /s | stub = 0 |
| 21 | 执行计划变更 | `plan_change_count` | 次 | stub = 0 |
| 22 | SQL 限流 | `sql_throttle_count` | 次 | stub = 0 |

### Slow 层（30s，tickCount % 30 == 0）

**来源**: `probe_tier.go` — 10 条 SQL + 7 个 stub

#### 已实现（12 个）

| # | 指标 | MetricName | 单位 | SQL 来源 | 计算方式 |
|---|------|-----------|------|---------|---------|
| 1 | Buffer Cache 命中率 | `buffer_cache_hit_pct` | % | v$sysstat physical reads / (db block gets + consistent gets) | (1 - miss_ratio) × 100 |
| 2 | Library Cache 命中率 | `library_cache_hit_pct` | % | v$librarycache WHERE namespace='SQL AREA' | gethitratio × 100 |
| 3 | PGA 使用率 | `pga_used_pct` | % | v$pgastat / v$parameter pga_aggregate_target | used / target × 100 |
| 4 | Shared Pool 空闲率 | `shared_pool_free_pct` | % | v$sgastat WHERE pool='shared pool' | free / total × 100 |
| 5 | 表空间使用率 | `tablespace_used_pct` | % | dba_tablespace_usage_metrics | MAX(used_percent) |
| 6 | 临时表空间使用率 | `temp_used_pct` | % | v$temp_space_header | used / (used+free) × 100 |
| 7 | FRA 使用率 | `fra_used_pct` | % | v$recovery_file_dest | space_used / space_limit × 100 |
| 8 | ASM 磁盘组使用率 | `asm_diskgroup_used_pct` | % | v$asm_diskgroup | MAX((total-free)/total × 100) |
| 9 | 日志切换频率 | `log_switch_rate` | 次/h | v$log_history WHERE last 1 hour | COUNT |
| 10 | 归档延迟 | `archive_lag_sec` | 秒 | v$archived_log WHERE dest_id=1 AND last 1 hour | MAX((SYSDATE-next_time)×86400) |
| 11 | 实例状态 | `instance_status` | - | v$instance | 1.0=OPEN, 0.0=其他 |
| 12 | 资源限制使用率 | `resource_limit_pct` | % | v$resource_limit (sessions/processes) | MAX(current/initial × 100) |

**Slow SQL**:

```sql
-- slowCacheHitSQL
SELECT
  (SELECT 1 - (SUM(CASE WHEN name='physical reads' THEN value END) /
   NULLIF(SUM(CASE WHEN name='db block gets' THEN value END) +
          SUM(CASE WHEN name='consistent gets' THEN value END), 0))
   FROM v$sysstat WHERE name IN ('physical reads','db block gets','consistent gets')) AS buf_hit,
  (SELECT gethitratio FROM v$librarycache WHERE namespace='SQL AREA') AS lib_hit
FROM dual

-- slowPGASharedPoolSQL
SELECT
  (SELECT value FROM v$parameter WHERE name='pga_aggregate_target') AS pga_target,
  (SELECT value FROM v$pgastat WHERE name='total PGA allocated') AS pga_used,
  (SELECT SUM(CASE WHEN name='free memory' THEN bytes ELSE 0 END) FROM v$sgastat WHERE pool='shared pool') AS sp_free,
  (SELECT SUM(bytes) FROM v$sgastat WHERE pool='shared pool') AS sp_total
FROM dual

-- slowTablespaceSQL
SELECT MAX(used_percent) FROM dba_tablespace_usage_metrics

-- slowTempSQL
SELECT NVL((SELECT SUM(bytes_used)*100.0/NULLIF(SUM(bytes_free+bytes_used),0)
            FROM v$temp_space_header), 0) AS temp_pct FROM dual

-- slowFRASQL
SELECT space_used*100.0/NULLIF(space_limit,0) FROM v$recovery_file_dest

-- slowASMSQL
SELECT MAX((total_mb-free_mb)*100.0/NULLIF(total_mb,0)) FROM v$asm_diskgroup

-- slowLogSwitchSQL
SELECT COUNT(*) FROM v$log_history WHERE first_time > SYSDATE - 1/24

-- slowArchiveLagSQL
SELECT NVL(MAX((SYSDATE-next_time)*86400), 0) FROM v$archived_log
WHERE dest_id=1 AND archived='YES' AND next_time > SYSDATE - 1/24

-- slowInstanceSQL
SELECT status FROM v$instance

-- slowResourceLimitSQL
SELECT current_utilization, max_utilization, initial_allocation
FROM v$resource_limit WHERE resource_name IN ('sessions','processes')
```

#### 未实现（Stub = 0）

| # | 指标 | MetricName | 单位 | 状态 |
|---|------|-----------|------|------|
| 13 | Undo 使用率 | `undo_used_pct` | % | stub = 0 |
| 14 | 备库同步延迟 | `standby_apply_lag_sec` | 秒 | stub = 0 |
| 15 | Redo 空间等待 | `redo_log_space_wait` | 次 | stub = 0 |
| 16 | DataGuard 状态 | `dataguard_status` | - | stub = 0 |
| 17 | 告警日志 ORA 错误 | `alert_log_ora_errors` | 条 | stub = 0 |
| 18 | 作业失败率 | `job_failure_rate` | % | stub = 0 |
| 19 | Checkpoint 未完成 | `checkpoint_not_complete` | 次 | stub = 0 |

### 探针汇总

| 层 | 频率 | SQL 条数 | 已实现 | Stub | 总计 |
|----|------|---------|--------|------|------|
| Fast | 1s | 2 | 7 | 0 | **7** |
| Medium | 10s | 5 | 16 | 6 | **22** |
| Slow | 30s | 10 | 12 | 7 | **19** |
| **合计** | | **17** | **35** | **13** | **48** |

48 个指标中 **35 个有真实采集**，**13 个还是 stub = 0**（不会触发任何检测策略）。

---

## 3. T1-T9 九大检测策略

### T1 — 统计阈值（3σ + 软绝对值 + 持续计数）

**源码**: `sentinel.go:343-443`

**算法**:
```
threshold = avg + 3 * stddev
minFloor  = avg + 3                  // 防止 std=0 时阈值等于均值
threshold = max(threshold, minFloor)

触发条件 (pathA):
  current > threshold AND current >= SoftAbsoluteMin
```

**持续计数**（`sustainedCounts`）：连续 3 个 tick（默认 1 秒/tick = 连续 3 秒）超阈值才真正触发。任何一次回落就归零（`sentinel.go:437`: `s.sustainedCounts[metric] = 0`）。

`ImmediateTrigger=true` 的指标（如 deadlock）持续计数 = 1，即一次即触发。

**Fixed 模式备选**（`sentinel.go:393-403`）：
```
TriggerMode = "fixed" 时:
  threshold = FixedThresholds[metric].Absolute  (如 lock_sessions = 5)
  或 threshold = baseline.avg × Multiplier      (如 active × 2.0)
```

**适用指标**: active/CPU/IO/Lock/LongSQL/Redo/HardParse + session_creation_rate/background_wait/blocking_chains/deadlocks/mutex_wait/row_lock_wait/log_switch_rate/archive_lag/plan_change/sql_throttle/checkpoint_not_complete/alert_log_errors/job_failure_rate 等 ~20 个

### T2 — 硬天花板（绝对危险值）

**源码**: `threshold.go:130-132`

**算法**:
```
HardCeiling = 2 × SoftAbsoluteMin
触发条件 (pathB): current >= HardCeiling（不看 baseline）
```

**设计意图**: 当 baseline 本身就很高时（比如长期 active=50），3σ 阈值可能永远不触发。HardCeiling 是兜底 — 不管 baseline 多高，绝对值达到危险水位就触发。

**与 T1 的关系**（`sentinel.go:416-418`）：
```go
if pathA || pathB {
    s.sustainedCounts[metric]++
}
```
T1 和 T2 是 **OR** 关系，任一满足就累加 sustained count，到 3 次就触发。

例如 8 核机器：
- `active_sessions`: soft=12, hard=24
- `cpu_sessions`: soft=6, hard=12
- `hard_parse_rate`: soft=24, hard=48

**适用指标**: 与 T1 共享 — active/CPU/IO/Redo/HardParse/OS_load/log_file_sync/db_file_seq_read/db_file_scat_read/log_switch_rate/blocking_chains 等 ~10 个

### T3 — 趋势检测（线性回归斜率）

**源码**: `detector.go:140-189`

**算法**:
```
取最近 10 个样本（minT3Samples = 10），做最小二乘线性回归
slopeThreshold = 0.5 × stddev

if slope > slopeThreshold:
    sustainedT3[metric]++
else:
    sustainedT3[metric] = 0

if sustainedT3[metric] >= TrendWindows (默认 3):
    触发
```

**线性回归实现**（`detector.go:115-134`）：最小二乘法，计算 `(n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)`。

**设计意图**: 捕捉**缓慢爬升**的趋势。比如逻辑读速率持续上涨，单次采样不超 3σ 但斜率持续为正。

**适用指标**: `logical_read_rate`（TrendWindows=3）、`full_scan_rate`（TrendWindows=3）

### T4 — 加速度检测（二阶导数）

**源码**: `detector.go:197-235`

**算法**:
```
需要 >= 3 个样本（minT4Samples = 3）

accel = v[n-1] - 2*v[n-2] + v[n-3]   // 离散二阶差分

触发条件（全部 AND）:
  accel > stddev                       // 加速度显著
  AND current >= SoftAbsoluteMin       // 绝对值不是噪声
  AND current > avg + stddev           // 已经偏离均值
```

**设计意图**: 捕捉**突然加速**。和 T3 不同，T4 不要求持续上升，而是要求**上升速度在加快**。比如临时表空间使用率不是匀速涨而是越涨越快。

**适用指标**: `pq_sessions`、`session_creation_rate`、`physical_read_rate`、`enqueue_wait_ms`、`row_lock_wait_ms`、`temp_used_pct`、`alert_log_ora_errors`

### T5 — 复合条件（多指标 AND）

**源码**: `detector.go:241-292`

**算法**:
```
两种模式：

1. 特殊处理 — instance_status:
   if current != 1 (非 OPEN): 立即触发

2. 通用复合:
   if 所有 CompoundWith 指标都在 alertState:
       AND current > 0:
           触发
```

**设计意图**: 某些场景需要**多个指标同时异常**才有意义。比如 `redo_log_space_wait` 单独出现可能是误报，但配合 `log_switch_rate` 同时冲高，就确认是 redo 链路的问题。

**适用指标**:
- `redo_log_space_wait` → CompoundWith: `[log_switch_rate]`
- `instance_status` → 特殊逻辑：非 OPEN 即触发（ImmediateTrigger=true）
- `dataguard_status` → T5

### T6 — 容量阈值（百分比水位线）

**源码**: `detector.go:300-327`

**算法**:
```
正向指标（使用率）: current >= CapacityRed → 触发
反向指标（空闲率，InvertCapacity=true）: current <= CapacityRed → 触发

红线触发无需 sustained count，立即触发。
```

**阈值配置表**（`metric.go`）：

| 指标 | Yellow | Red | 方向 |
|------|--------|-----|------|
| `tablespace_used_pct` | 85% | 95% | 正向 |
| `temp_used_pct` | 85% | 95% | 正向 |
| `undo_used_pct` | 85% | 95% | 正向 |
| `fra_used_pct` | 85% | 95% | 正向 |
| `asm_diskgroup_used_pct` | 85% | 95% | 正向 |
| `pga_used_pct` | 85% | 95% | 正向 |
| `total_sessions` | 85% | 95% | 正向 |
| `resource_limit_pct` | 85% | 95% | 正向 |
| `shared_pool_free_pct` | 15% | 5% | **反向**（空闲率低于阈值触发）|
| `archive_lag_sec` | 300s | 600s | 正向（非百分比，绝对值） |
| `standby_apply_lag` | 120s | 600s | 正向（非百分比，绝对值） |

**设计意图**: 容量类指标不需要统计基线，85%/95% 是业界通用的预警/危险水位。

### T7 — 水位跳变（窗口均值对比）

**源码**: `detector.go:332-378`

**算法**:
```
需要 >= 20 个样本（minT7Samples = 20）

将历史窗口对半分: oldHalf / newHalf
计算 oldMean, newMean, oldStd

diff = |newMean - oldMean|

if diff > 2 × oldStd:
    触发
```

**设计意图**: 检测**台阶式跳变**。和 T3（持续上升斜率）不同，T7 不关心方向，而是关心均值发生了**不可逆的偏移**。比如网络往返延迟从稳定 200μs 突然跳到 2000μs 并停在那里。T1 在跳变后基线会逐渐重建，新水位变成"正常"，但 T7 通过对比新旧两段窗口能发现这个跳变。

**适用指标**: `network_round_trip_us`、`top_sql_elapsed_drift`

### T8 — 退化检测（跌穿地板值）

**源码**: `detector.go:384-426`

**算法**:
```
方向判断:
  latch_free_rate: current > Floor → 退化（miss rate 越高越差）
  其他（cache hit, free %）: current < Floor → 退化（越低越差）

if regressed:
    sustainedT8[metric]++
else:
    sustainedT8[metric] = 0

if sustainedT8[metric] >= RegressionSustain (默认 5):
    触发
```

**地板值配置表**:

| 指标 | Floor | Sustain | 含义 |
|------|-------|---------|------|
| `buffer_cache_hit_pct` | 90% | 5 | Buffer Cache 命中率跌破 90% 持续 5 次 |
| `library_cache_hit_pct` | 95% | 5 | Library Cache 命中率跌破 95% 持续 5 次 |
| `shared_pool_free_pct` | 50% | 5 | Shared Pool 空闲跌破 50%（T6 红线是 5%，T8 更早预警）|
| `latch_free_rate` | 5% | 5 | Latch Miss Rate 超过 5% 持续 5 次 |

**设计意图**: 效率指标的退化。和 T6 的"容量快满了"不同，T8 关注的是**性能指标恶化** — Buffer Cache 命中率从 99% 掉到 88%，虽然离"满"还远，但性能已经受影响了。

### T9 — 缺失检测（速率骤降）

**源码**: `detector.go:436-505`

**算法**:
```
前置检查: baseline.avg >= MinBaseline (20 TPS)

dropLimit = avg × (1 - DropThreshold)   // avg × 0.2 = 保留 20%，即下降 80%

if current >= dropLimit:
    sustainedT9 = 0; return nil          // 没有显著下降

// 活跃会话同步检查（防误报）
if CheckActiveSync:
    if active_sessions 也按比例下降 (activeRatio < 0.5):
        // 业务正常低谷，不是故障
        sustainedT9 = 0; return nil

sustainedT9++
if sustainedT9 >= AbsenceSustain (5):
    触发
```

**CheckActiveSync 逻辑**（`detector.go:461-478`）：
- 取 `active_sessions` 的历史均值作为 activeBaseline
- 计算 activeRatio = activeCurrent / activeBaseline
- 如果 activeRatio < 0.5（活跃会话也降了一半以上），说明是业务低谷，不触发

**设计意图**: TPS 骤降是数据库最危险的信号之一 — 可能 Hang 了、可能锁死了。但业务低峰期 TPS 也会自然下降。CheckActiveSync 是关键防误报机制。

**适用指标**: `commit_rate`（DropThreshold=0.8, MinBaseline=20, AbsenceSustain=5, CheckActiveSync=true）

### DetectExtended 入口

**源码**: `detector.go:514-580`

T3-T9 的统一入口函数 `DetectExtended()`：
1. 按 `metricPriorityOrder` 遍历全部 48 个指标
2. 跳过 suppressed 中的指标
3. 跳过 sample.Values 中不存在的指标（未采集的 stub）
4. 对每个指标遍历其 Strategies 列表
5. 跳过 T1/T2（由 sentinel.go 的 detectAnomaly 处理）
6. 按 T3→T4→T5→T6→T7→T8→T9 顺序检查
7. **首个触发即返回**（不会一次触发多个）

---

## 4. 3σ 阈值计算详解

### Baseline 怎么算

**源码**: `sentinel.go:277-335`

- 滚动窗口 60 个样本（60 秒），每秒 push 一个
- 超过 60 个就丢弃最旧的
- 收集满 10 个样本后 `Ready=true`，开始检测
- 每次 push 后重算所有指标的 avg 和 std（**总体标准差**，除以 n 不是 n-1）

```go
// recomputeBaseline: 总体标准差
variance += diff * diff
std = sqrt(variance / n)   // 不是 n-1
```

### 阈值公式

```
threshold = avg + 3.0 × std
minFloor  = avg + 3
threshold = max(threshold, minFloor)
```

**minFloor 的作用**: 当 std ≈ 0 时（比如空闲数据库 active 一直是 0），threshold = avg + 0 = avg，任何微小波动都会触发。minFloor = avg + 3 保证至少要偏离 3 个单位才触发。

### 举例

过去 60 秒 `active_sessions` 均值 5，标准差 2：
- threshold = 5 + 3×2 = **11**
- minFloor = 5 + 3 = 8
- 取 max → **11**

标准差很小时（均值 5，std 0.5）：
- threshold = 5 + 1.5 = 6.5
- minFloor = 5 + 3 = 8
- 取 max → **8**（minFloor 兜底）

空闲数据库（均值 0，std 0）：
- threshold = 0 + 0 = 0
- minFloor = 0 + 3 = 3
- 取 max → **3**

---

## 5. SoftAbsoluteMin 详解

### 作用

3σ 检测的是"相对于自身历史的偏离"，SoftAbsoluteMin 检测的是"绝对值是否够大到值得关注"。两者是 **AND** 关系。

比如一个空闲数据库，active 均值 0，std 0.3：
- 3σ 阈值 = 0 + 0.9 → minFloor = 3 → 阈值 = 3
- 来了 4 个 active，超过阈值 3 ✓
- 但 SoftAbsoluteMin = 12（8核机器），4 < 12 → **不触发**

4 个活跃会话对 8 核机器来说根本不算事。

### 完整阈值表

**源码**: `threshold.go:10-125`

公式: `max(CPU × factor, fixedFloor)`，`HardCeiling = 2 × SoftAbsoluteMin`

CPU 核数从 `V$OSSTAT NUM_CPU_CORES` 查询，未知时用 fixedFloor。

| 指标 | 公式 | 8核 | 32核 | 未知CPU | HardCeiling(8核) |
|------|------|-----|------|---------|-------------------|
| **Session/Load** |
| active_sessions | max(CPU×1.5, 8) | 12 | 48 | 8 | 24 |
| cpu_sessions | max(CPU×0.75, 4) | 6 | 24 | 4 | 12 |
| io_sessions | max(CPU×0.5, 4) | 4 | 16 | 4 | 8 |
| lock_sessions | 固定 3 | 3 | 3 | 3 | 6 |
| long_sql | 固定 2 | 2 | 2 | 2 | 4 |
| os_load | max(CPU×2, 4) | 16 | 64 | 4 | 32 |
| pq_sessions | max(CPU×0.5, 4) | 4 | 16 | 4 | 8 |
| session_creation_rate | 固定 10/s | 10 | 10 | 10 | 20 |
| background_wait | 固定 5 | 5 | 5 | 5 | 10 |
| total_sessions | 0（用 T6） | - | - | - | - |
| **Throughput** |
| redo_rate | 固定 5000 KB/s | 5000 | 5000 | 5000 | 10000 |
| hard_parse_rate | max(CPU×3, 20) | 24 | 96 | 20 | 48 |
| physical_read_rate | 固定 1000/s | 1000 | 1000 | 1000 | 2000 |
| logical_read_rate | 0（用 T3） | - | - | - | - |
| commit_rate | 0（用 T9） | - | - | - | - |
| **Wait/Latency** |
| log_file_sync_avg_us | 固定 5000μs | 5000 | 5000 | 5000 | 10000 |
| db_file_seq_read_avg_us | 固定 10000μs | 10000 | 10000 | 10000 | 20000 |
| db_file_scat_read_avg_us | 固定 15000μs | 15000 | 15000 | 15000 | 30000 |
| enqueue_wait_ms | 固定 500ms | 500 | 500 | 500 | 1000 |
| latch_free_rate | 0（用 T8） | - | - | - | - |
| network_round_trip | 0（用 T7） | - | - | - | - |
| **Memory/Cache** |
| buffer/library/pga/shared_pool | 全部 0 | - | - | - | -（用 T6/T8） |
| **Storage** |
| tablespace/temp/undo/fra/asm | 全部 0 | - | - | - | -（用 T6） |
| **Redo/Archive** |
| log_switch_rate | 固定 10 次/h | 10 | 10 | 10 | 20 |
| archive_lag_sec | 固定 60s | 60 | 60 | 60 | 120 |
| standby_apply_lag | 固定 120s | 120 | 120 | 120 | 240 |
| redo_log_space_wait | 固定 1 | 1 | 1 | 1 | 2 |
| **Lock/Concurrency** |
| blocking_chains | 固定 2 层 | 2 | 2 | 2 | 4 |
| enqueue_deadlocks | 固定 1 次 | 1 | 1 | 1 | 2 |
| mutex_wait | 固定 3 | 3 | 3 | 3 | 6 |
| row_lock_wait_ms | 固定 1000ms | 1000 | 1000 | 1000 | 2000 |
| **SQL Performance** |
| top_sql_drift | 0（用 T7） | - | - | - | - |
| full_scan_rate | 0（用 T3） | - | - | - | - |
| plan_change_count | 固定 3 | 3 | 3 | 3 | 6 |
| sql_throttle | 固定 1 | 1 | 1 | 1 | 2 |
| **System/Pattern** |
| instance_status | 0（用 T5） | - | - | - | - |
| dataguard_status | 0（用 T5） | - | - | - | - |
| alert_log_errors | 固定 1 条/min | 1 | 1 | 1 | 2 |
| job_failure_rate | 0 | - | - | - | - |
| resource_limit_pct | 0（用 T6） | - | - | - | - |
| checkpoint_not_complete | 固定 1 次/h | 1 | 1 | 1 | 2 |

> SoftAbsoluteMin = 0 的指标不走 T1/T2 路径，由其专属策略（T3/T5/T6/T7/T8/T9）处理。

---

## 6. 抑制与恢复机制

### 抑制（Suppression）

**源码**: `sentinel.go:241, 252`

触发后将指标加入 `suppressed` map：
```go
s.suppressed[MetricName(trigger.Metric)] = trigger.Threshold
```

后续 tick 中，`detectAnomaly` 和 `DetectExtended` 都会跳过 suppressed 中的指标，避免同一个指标重复触发 burst。

### 恢复检测

**源码**: `sentinel.go:449-515`

每个 tick（Watch 和 Cooldown 状态）都会运行 `checkSuppressedRecovery()`：

```
对 suppressed 中的每个指标:
  获取当前值 current

  if 退化类指标（lower is worse）:
      // cache hit, free %, commit rate
      recovered = (current >= threshold)
  else:
      // 冲高类指标（higher is worse）
      recovered = (current < threshold × 0.9)

  if recovered:
      delete(suppressed, metric)
```

**退化类判断**（`isLowerIsWorseMetric`）：
- 有 T8 策略 → lower is worse
- InvertCapacity = true → lower is worse
- 有 T9 策略 → lower is worse

**冲高类恢复要求降到阈值的 90% 以下**（而非刚好低于阈值），防止指标在阈值附近反复抖动导致频繁触发。

### Cooldown

burst 结束后进入 StateCooldown（`sentinel.go:548-549`）：
```go
s.state = StateCooldown
s.lastTrigger = time.Now()
```

Cooldown 期间（默认 5 分钟）不运行检测。到期后回到 StateWatch 并清空所有 sustainedCounts。

---

## 7. 策略分配总览

### 每个指标的完整策略配置

**源码**: `metric.go:175-461`

| 指标 | 层 | 策略 | 特殊配置 |
|------|-----|------|---------|
| **Session/Load** |
| active_sessions | Fast | T1, T2 | |
| cpu_sessions | Fast | T1, T2 | |
| io_sessions | Fast | T1, T2 | |
| lock_sessions | Fast | T1, T2 | |
| long_sql | Fast | T1, T2 | |
| total_sessions | Medium | T6 | Yellow=85, Red=95 |
| pq_sessions | Medium | T1, T4 | |
| session_creation_rate | Medium | T1, T4 | |
| background_wait | Medium | T1 | |
| os_load | Medium | T1, T2 | |
| **Throughput** |
| redo_rate | Fast | T1, T2 | |
| hard_parse_rate | Fast | T1, T2 | |
| physical_read_rate | Medium | T1, T4 | |
| logical_read_rate | Medium | T3 | TrendWindows=3 |
| commit_rate | Medium | T9 | Drop=0.8, MinBaseline=20, Sustain=5, CheckActiveSync |
| **Wait/Latency** |
| log_file_sync_avg_us | Medium | T1, T2 | |
| db_file_seq_read_avg_us | Medium | T1, T2 | |
| db_file_scat_read_avg_us | Medium | T1, T2 | |
| enqueue_wait_ms | Medium | T1, T4 | |
| latch_free_rate | Medium | T8 | Floor=5.0, Sustain=5 |
| network_round_trip_us | Medium | T7 | |
| **Memory/Cache** |
| buffer_cache_hit_pct | Slow | T8 | Floor=90.0, Sustain=5 |
| library_cache_hit_pct | Slow | T8 | Floor=95.0, Sustain=5 |
| pga_used_pct | Slow | T6 | Yellow=85, Red=95 |
| shared_pool_free_pct | Slow | T6, T8 | Yellow=15, Red=5, InvertCapacity; Floor=50.0, Sustain=5 |
| **Storage** |
| tablespace_used_pct | Slow | T6 | Yellow=85, Red=95 |
| temp_used_pct | Slow | T6, T4 | Yellow=85, Red=95 |
| undo_used_pct | Slow | T6 | Yellow=85, Red=95 |
| fra_used_pct | Slow | T6 | Yellow=85, Red=95 |
| asm_diskgroup_used_pct | Slow | T6 | Yellow=85, Red=95 |
| **Redo/Archive** |
| log_switch_rate | Slow | T1, T2 | |
| archive_lag_sec | Slow | T1, T6 | Yellow=300, Red=600 |
| standby_apply_lag | Slow | T1, T6 | Yellow=120, Red=600 |
| redo_log_space_wait | Slow | T1, T5 | CompoundWith=[log_switch_rate] |
| **Lock/Concurrency** |
| blocking_chains | Medium | T1, T2 | |
| enqueue_deadlocks | Medium | T1 | ImmediateTrigger=true |
| mutex_wait_sessions | Medium | T1 | |
| row_lock_wait_ms | Medium | T1, T4 | |
| **SQL Performance** |
| top_sql_elapsed_drift | Medium | T7 | |
| full_scan_rate | Medium | T3 | TrendWindows=3 |
| plan_change_count | Medium | T1 | |
| sql_throttle_count | Medium | T1 | |
| **System/Pattern** |
| instance_status | Slow | T5 | ImmediateTrigger=true |
| dataguard_status | Slow | T5 | |
| alert_log_ora_errors | Slow | T1, T4 | |
| job_failure_rate | Slow | T1 | |
| resource_limit_pct | Slow | T6 | Yellow=85, Red=95 |
| checkpoint_not_complete | Slow | T1 | |

### 按策略分类汇总

| 策略 | 指标数 | 典型指标 | 核心逻辑 |
|------|--------|---------|---------|
| T1 | ~20 | active, CPU, IO, redo, hard_parse | 3σ + softMin + 持续3次 |
| T2 | ~10 | active, CPU, IO, redo, OS load | 2×softMin 硬天花板 |
| T3 | 2 | logical_read_rate, full_scan_rate | 线性回归斜率 × 3窗口 |
| T4 | 7 | PQ, session创建, physical read, temp | 二阶导数 > std |
| T5 | 3 | redo_space_wait, instance/DG status | 多指标 AND |
| T6 | 11 | 表空间/temp/undo/FRA/PGA/连接数 | 85%/95% 水位 |
| T7 | 2 | network_roundtrip, top_sql_drift | 窗口均值跳变 > 2σ |
| T8 | 4 | buffer/library cache, shared pool, latch | 跌穿地板值 × 5次 |
| T9 | 1 | commit_rate | 骤降80% + 活跃会话校验 |

### 检测优先级

**源码**: `metric.go:432-460` — `metricPriorityOrder`

```
Fast 层优先:
  active → CPU → IO → Lock → LongSQL → Redo → HardParse

Medium 层次之:
  TotalSessions → PQ → SessionCreation → BackgroundWait → OSLoad
  → PhysicalRead → LogicalRead → CommitRate
  → Blocking → Deadlock → Mutex → RowLock
  → LogFileSync → DbFileSeqRead → DbFileScatRead → EnqueueWait → Latch → Network
  → TopSQLDrift → FullScan → PlanChange → SQLThrottle

Slow 层最后:
  BufferCache → LibraryCache → PGA → SharedPool
  → Tablespace → Temp → Undo → FRA → ASM
  → LogSwitch → ArchiveLag → StandbyLag → RedoSpaceWait
  → Instance → DG → AlertLog → JobFailure → ResourceLimit → Checkpoint
```

---

## 8. 为什么不同指标用不同策略

### 五种基本异常形态

数据库异常有五种基本形态，每种需要不同的数学方法来检测：

| 形态 | 对应策略 | 用其他策略的问题 |
|------|---------|---------------|
| 突然冲高 | T1/T2 | — |
| 缓慢爬升 | T3 | T1 的 baseline 跟着涨，阈值也涨，永远追不上 |
| 加速恶化 | T4 | T3 只看斜率方向，不看斜率是否在变快 |
| 多因子关联 | T5 | T1 单指标触发会误报 |
| 容量水位 | T6 | 不需要 baseline，85%/95% 是行业硬标准 |
| 台阶跳变 | T7 | T1 在跳变后 baseline 重建，新水位变成"正常" |
| 效率退化 | T8 | T1/T6 检测"高"，不检测"低" |
| 速率消失 | T9 | T1 只检测冲高，不检测骤降 |

### 具体例子

**active_sessions 冲高**（T1+T2）— 典型特征是突然跳起来。比如从 5 跳到 50。用 3σ 偏离 + 绝对值兜底就能抓到。

**logical_read_rate 上涨**（T3）— 典型特征是慢慢爬。比如从 1 万/s 用 10 分钟涨到 5 万/s，每秒涨一点，单次采样永远不超 3σ。只有看斜率才能发现趋势。如果用 T1，基线跟着涨，永远抓不到。

**temp_used_pct 暴涨**（T4）— 典型特征是加速。比如前 5 秒涨了 1%，后 5 秒涨了 10%。不是匀速上升（T3 抓不到"越来越快"），而是二阶导数为正。再不触发可能就 100% 了。

**redo_log_space_wait**（T5）— 单独出现可能是误报（偶尔等一下很正常）。只有配合 log_switch_rate 也冲高，才说明 redo 链路真的堵了。必须 AND 两个指标。

**buffer_cache_hit_pct 下降**（T8）— 越低越差，不是越高越差。3σ 检测的是"偏离均值"，但 cache hit 从 99.5% 跌到 88%，绝对偏差不大，3σ 可能不触发。用地板值（90%）直接判断更准。

**commit_rate 骤降**（T9）— TPS 突然归零，但 3σ 检测"异常冲高"，不检测"异常消失"。而且 TPS 下降可能是业务低谷（正常）而不是故障，所以需要 CheckActiveSync 看活跃会话是否同步下降来区分。

### 结论

一个策略做不到的事，换一个思路就做到了。这就是为什么每个指标要选不同的策略组合，而不是用一个通用公式。
