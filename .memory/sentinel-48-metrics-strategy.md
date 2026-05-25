---
name: sentinel-48-metrics-strategy
description: 哨兵 48 项指标检测策略表 — 9 大类指标、9 种检测算法(T1-T9)、分层探针频率、软值/硬顶定义
type: project
---

# Sentinel 48 项指标检测策略

## 触发条件（三重条件 AND）

**Path A（趋势异常）**: current > mean + 3σ AND current ≥ 软绝对值(soft abs min) AND 连续 3 秒
**Path B（硬顶触发）**: current ≥ 硬顶(hard ceiling) AND 连续 3 秒

- 软绝对值(soft abs min): 噪声过滤阈值，低于此值无论 3σ 如何都视为正常，随 CPU 核数缩放
- 硬顶(hard ceiling) = 2 × 软绝对值: 绝对危险阈值，不考虑基线趋势直接触发
- 连续计数器在正常样本时重置为 0

## 9 种检测策略类型

| 代号 | 名称 | 算法 |
|------|------|------|
| T1 | 阈值检测 | 3σ + 软绝对值 + 持续3s (Path A) |
| T2 | 硬顶触发 | 硬顶 + 持续3s (Path B) |
| T3 | 趋势检测 | 滑动窗口回归斜率 > 阈值 + 持续 N 个窗口 |
| T4 | 加速度检测 | 二阶导数 > 0 + 当前值已超 soft abs |
| T5 | 复合条件 | 多指标联合：指标A AND 指标B 同时超标 |
| T6 | 容量预警 | 使用率 > 85%（黄）/ 95%（红）|
| T7 | 偏移检测 | 新窗口均值 vs 旧窗口均值，偏移 > 2σ |
| T8 | 回归检测 | 命中率/效率指标跌破阈值 + 持续5s |
| T9 | 缺失检测 | 基线速率骤降 > 80% + 持续5s |

## 分层探针频率

| 层级 | 频率 | 指标类别 |
|------|------|----------|
| Fast | 1s | 会话/负载、锁/并发 |
| Medium | 10s | 吞吐/速率、等待/延迟、SQL性能 |
| Slow | 30s | 内存/缓存、存储/容量、Redo/归档、系统/模式 |

## 完整 48 指标表

### 1. 会话/负载 (10 项, Fast 1s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| active_sessions | V$SESSION status='ACTIVE' AND type='USER' 非idle | T1+T2 | 3σ+soft(CPU×1.5,min 8)+3s / hard=2×soft |
| cpu_sessions | V$SESSION ON CPU 会话数 | T1+T2 | 3σ+soft(CPU×0.75,min 4)+3s / hard=2×soft |
| io_wait_sessions | V$SESSION IO 等待会话数 | T1+T2 | 3σ+soft(CPU×0.5,min 4)+3s / hard=2×soft |
| lock_wait_sessions | V$SESSION 锁等待会话数 | T1+T2 | 3σ+soft(3)+3s / hard=6 |
| long_sql_sessions | V$SESSION 运行时间>30s(可配置)的会话数 | T1+T2 | 3σ+soft(2)+3s / hard=4 |
| total_sessions | V$SESSION 总会话数 | T6 | 使用率>85%黄/95%红 (对比 PROCESSES 参数) |
| pq_sessions | V$PX_SESSION 并行查询会话数 | T1+T4 | 3σ+soft(CPU×0.5)+3s / 加速度检测 |
| session_creation_rate | 每秒新建会话数(delta logons cumulative) | T1+T4 | 3σ+soft(10/s)+3s / 加速度检测 |
| background_wait | V$SESSION 后台进程等待数 | T1 | 3σ+soft(5)+3s |
| os_load | V$OSSTAT LOAD (OS负载) | T1+T2 | 3σ+soft(CPU×2)+3s / hard=CPU×4 |

### 2. 吞吐/速率 (5 项, Medium 10s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| redo_rate_kbps | V$SYSSTAT 'redo size' delta/interval (KB/s) | T1+T2 | 3σ+soft(5000)+3s / hard=10000 |
| hard_parse_rate | V$SYSSTAT 'parse count (hard)' delta/interval (/s) | T1+T2 | 3σ+soft(CPU×3,min 20)+3s / hard=2×soft |
| physical_read_rate | V$SYSSTAT 'physical reads' delta/interval (/s) | T1+T4 | 3σ+soft(1000)+3s / 加速度检测 |
| logical_read_rate | V$SYSSTAT 'session logical reads' delta/interval (/s) | T3 | 趋势检测：斜率>阈值+持续3窗口 |
| commit_rate | V$SYSSTAT 'user commits' delta/interval (/s) | T9 | 缺失检测：骤降>80%+持续5s |

### 3. 等待/延迟 (6 项, Medium 10s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| log_file_sync_avg_us | V$EVENT_HISTOGRAM 'log file sync' 均值 | T1+T2 | 3σ+soft(5000μs)+3s / hard=10000μs |
| db_file_seq_read_avg_us | V$EVENT_HISTOGRAM 'db file sequential read' 均值 | T1+T2 | 3σ+soft(10000μs)+3s / hard=20000μs |
| db_file_scat_read_avg_us | V$EVENT_HISTOGRAM 'db file scattered read' 均值 | T1+T2 | 3σ+soft(15000μs)+3s / hard=30000μs |
| enqueue_wait_time_ms | V$SYSSTAT 'enqueue waits' delta × avg_wait | T1+T4 | 3σ+soft(500ms)+3s / 加速度检测 |
| latch_free_rate | V$LATCH gets vs misses 比率 | T8 | 回归检测：miss率>5%+持续5s |
| network_round_trip_us | V$SESSION_EVENT 'SQL*Net message from client' 均值 | T7 | 偏移检测：新窗口均值偏移>2σ |

### 4. 内存/缓存 (4 项, Slow 30s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| buffer_cache_hit_pct | 1 - (physical reads / logical reads) × 100 | T8 | 回归检测：跌破90%+持续5s |
| library_cache_hit_pct | V$LIBRARYCACHE gethitratio × 100 | T8 | 回归检测：跌破95%+持续5s |
| pga_used_pct | V$PGASTAT 'total PGA allocated' / pga_aggregate_target | T6 | 容量预警：>85%黄/>95%红 |
| shared_pool_free_pct | V$SGASTAT 'free memory' / shared_pool_size | T6+T8 | 容量<15%黄/<5%红 / 骤降>50%回归 |

### 5. 存储/容量 (5 项, Slow 30s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| tablespace_used_pct | DBA_TABLESPACE_USAGE_METRICS used_percent | T6 | 容量>85%黄/>95%红 |
| temp_used_pct | V$TEMP_SPACE_HEADER used / total | T6+T4 | 容量>85%黄 / 加速度检测急增 |
| undo_used_pct | V$UNDOSTAT undoblks / undo_size | T6 | 容量>85%黄/>95%红 |
| fra_used_pct | V$RECOVERY_FILE_DEST space_used / space_limit | T6 | 容量>85%黄/>95%红 |
| asm_diskgroup_used_pct | V$ASM_DISKGROUP (total-free)/total | T6 | 容量>85%黄/>95%红（仅 ASM 环境）|

### 6. Redo/归档 (4 项, Slow 30s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| log_switch_rate | V$LOG_HISTORY 每小时切换次数 | T1+T2 | 3σ+soft(10/h)+3s / hard=20/h |
| archive_lag_sec | V$ARCHIVED_LOG 归档延迟 | T1+T6 | 3σ+soft(60s)+3s / >300s红 |
| standby_apply_lag_sec | V$DATAGUARD_STATS apply lag | T1+T6 | 3σ+soft(120s)+3s / >600s红 |
| redo_log_space_wait | V$SYSSTAT 'redo log space requests' delta | T1+T5 | 3σ+soft(1)+3s / 复合: AND log_switch冲高 |

### 7. 锁/并发 (4 项, Fast 1s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| blocking_chains | V$SESSION blocking_session IS NOT NULL 链深度 | T1+T2 | 3σ+soft(2)+3s / hard=4(深度) |
| enqueue_deadlocks | V$SYSSTAT 'enqueue deadlocks' delta | T1 | 任意>0即刻触发(sustained=1) |
| mutex_wait_sessions | V$MUTEX_SLEEP 等待会话数 | T1 | 3σ+soft(3)+3s |
| row_lock_wait_time_ms | V$SYSSTAT 'enqueue waits' 中 TX 类型 delta × avg | T1+T4 | 3σ+soft(1000ms)+3s / 加速度检测 |

### 8. SQL 性能 (4 项, Medium 10s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| top_sql_elapsed_drift | V$SQL 单条 elapsed_time 突增(与历史对比) | T7 | 偏移检测：新均值/旧均值>3倍 |
| full_scan_rate | V$SQL 全表扫描语句数/总语句数 | T3 | 趋势检测：比率斜率上升+持续3窗口 |
| plan_change_count | V$SQL plan_hash_value 变化计数 | T1 | 3σ+soft(3)+3s |
| sql_throttle_count | V$SQL_MONITOR 被资源管理限制的SQL数 | T1 | 3σ+soft(1)+3s |

### 9. 系统/模式 (6 项, Slow 30s)

| 指标 | 计算方法 | 触发策略 | 触发算法 |
|------|----------|----------|----------|
| instance_status | V$INSTANCE status != 'OPEN' | T5 | 复合：非 OPEN 即触发 |
| dataguard_status | V$DATABASE protection_mode 异常 | T5 | 复合：standby gap 或 mode 变化 |
| alert_log_ora_errors | 告警日志 ORA- 错误频率 | T1+T4 | 3σ+soft(1/min)+3s / 加速度检测 |
| job_failure_rate | DBA_SCHEDULER_JOB_RUN_DETAILS failure 比率 | T1 | 3σ+soft(5%)+3s |
| resource_limit_pct | V$RESOURCE_LIMIT current/max (processes/sessions) | T6 | 容量>85%黄/>95%红 |
| checkpoint_not_complete | 告警日志 "checkpoint not complete" 频率 | T1 | 3σ+soft(1/h)+3s |

## 关键设计决策

1. **long_sql 阈值默认 30s**（可配置），OLAP 系统 10s 以上的 SQL 很正常
2. **硬顶 = 2 × 软值**，所有指标统一规则
3. **连续 3 秒持续**才触发（SustainedCount=3），防止单点毛刺
4. **软值随硬件缩放**: active/cpu/io/hard_parse 等与 CPU 核数相关
5. **enqueue_deadlocks 例外**: 任何死锁立即触发（sustained=1）
6. **commit_rate_drop 使用 T9（缺失检测）**: 不用传统软值/硬顶，而是检测基线速率骤降 >80%
