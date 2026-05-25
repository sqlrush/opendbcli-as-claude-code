---
name: alert-templates
description: 48个指标的告警描述设计 + 9大场景的监控数据模板（按场景定制，不用通用模板）
type: project
---

# 告警描述 + 监控数据模板设计

## 核心原则

- Sentinel/规则引擎只陈述事实，不出结论（不用"性能异常"等模糊词）
- 标题叫"监控数据"不叫"异常监控数据"
- 告警描述格式：中文名+English术语+带单位数值+触发原因
- 每个场景只展示和该场景相关的数据块，不相关的不展示
- 用户是专业DBA，能看懂原始监控指标

## 告警描述模板（按触发策略）

| 策略 | 触发原因描述格式 | 示例 |
|------|----------------|------|
| T1 | `3σ阈值{threshold}` | `活跃会话Active Sessions 2→15个 (5s内, 3σ阈值8.3)` |
| T2 | `上限{threshold}` | `OS负载OS Load 0.8→12.5 (10s内, 上限{threshold})` |
| T3 | `持续上升 ... 连续{N}个窗口` | `逻辑读速率Logical Reads 持续上升 1.2万→3.8万/s (30s内, 连续3个窗口)` |
| T4 | `加速增长` | `物理读速率Physical Reads 500→8500/s (10s内, 加速增长)` |
| T5 | `复合条件 + 关联指标说明` | `Redo空间等待 0→15次 (30s内, 3σ阈值3.0, 同时Log Switch冲高)` |
| T6 | `红线{threshold}%` | `临时表空间Temp使用率 40%→100% (5s内, 红线95%)` |
| T7 | `窗口均值偏移 >2σ` | `网络往返延迟SQL*Net Round-trip 窗口均值偏移 200→1800μs (30s内, >2σ)` |
| T8 | `下限{floor}` | `Buffer Cache命中率 99.2%→78.5% (30s内, 下限90%)` |
| T9 | `较基线下降{pct}%` | `提交速度TPS 200→12/s (10s内, 较基线下降94%)` |
| 立即触发 | `立即触发` | `死锁Deadlock 0→2次 (立即触发)` |

## 48个指标告警显示名

### 会话/负载 Session Load

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 1 | active_sessions | 活跃会话Active Sessions | 个 | T1+T2 |
| 2 | cpu_sessions | 活跃会话On CPU | 个 | T1+T2 |
| 3 | io_sessions | 活跃会话I/O Wait | 个 | T1+T2 |
| 4 | lock_sessions | 活跃会话Lock Wait | 个 | T1+T2 |
| 5 | long_sql | 活跃会话Long SQL(>30s) | 个 | T1+T2 |
| 6 | total_sessions | 总会话数Total Sessions | 个 | T6 |
| 7 | pq_sessions | 并行查询会话PQ Sessions | 个 | T1+T4 |
| 8 | session_creation_rate | 新建会话速率Session Creation | /s | T1+T4 |
| 9 | background_wait | 后台进程等待Background Wait | 个 | T1 |
| 10 | os_load | OS负载OS Load | — | T1+T2 |

### 吞吐量 Throughput

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 11 | redo_rate | Redo生成速率Redo Rate | KB/s | T1+T2 |
| 12 | hard_parse_rate | 硬解析速率Hard Parse | /s | T1+T2 |
| 13 | physical_read_rate | 物理读速率Physical Reads | /s | T1+T4 |
| 14 | logical_read_rate | 逻辑读速率Logical Reads | /s | T3 |
| 15 | commit_rate | 提交速度TPS | /s | T9 |

### 等待/延迟 Wait Latency

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 16 | log_file_sync_avg_us | 日志同步延迟Log File Sync | μs | T1+T2 |
| 17 | db_file_seq_read_avg_us | 单块读延迟db file sequential read | μs | T1+T2 |
| 18 | db_file_scat_read_avg_us | 多块读延迟db file scattered read | μs | T1+T2 |
| 19 | enqueue_wait_time_ms | 队列等待Enqueue Wait | ms | T1+T4 |
| 20 | latch_free_rate | Latch命中率Latch Hit Ratio | % | T8 |
| 21 | network_round_trip_us | 网络往返延迟SQL*Net Round-trip | μs | T7 |

### 内存/缓存 Memory Cache

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 22 | buffer_cache_hit_pct | Buffer Cache命中率 | % | T8 |
| 23 | library_cache_hit_pct | Library Cache命中率 | % | T8 |
| 24 | pga_used_pct | PGA使用率 | % | T6 |
| 25 | shared_pool_free_pct | Shared Pool空闲率 | % | T6+T8 |

### 存储/容量 Storage

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 26 | tablespace_used_pct | 表空间Tablespace使用率 | % | T6 |
| 27 | temp_used_pct | 临时表空间Temp使用率 | % | T6 |
| 28 | undo_used_pct | Undo表空间使用率 | % | T6 |
| 29 | fra_used_pct | 快速恢复区FRA使用率 | % | T6 |
| 30 | asm_diskgroup_used_pct | ASM磁盘组使用率 | % | T6 |

### Redo/归档 Redo Archive

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 31 | log_switch_rate | 日志切换频率Log Switch | 次/h | T1+T2 |
| 32 | archive_lag_sec | 归档延迟Archive Lag | 秒 | T1+T6 |
| 33 | standby_apply_lag_sec | 备库同步延迟Standby Lag | 秒 | T1+T6 |
| 34 | redo_log_space_wait | Redo空间等待Redo Log Space Wait | 次 | T1+T5 |

### 锁/并发 Lock Concurrency

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 35 | blocking_chains | 阻塞链Blocking Chains | 层 | T1+T2 |
| 36 | enqueue_deadlocks | 死锁Deadlock | 次 | T1 立即 |
| 37 | mutex_wait_sessions | Mutex等待会话 | 个 | T1 |
| 38 | row_lock_wait_time_ms | 行锁等待Row Lock Wait | ms | T1+T4 |

### SQL性能 SQL Performance

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 39 | top_sql_elapsed_drift | Top SQL耗时偏移Elapsed Drift | 倍 | T7 |
| 40 | full_scan_rate | 全表扫描速率Full Table Scan | /s | T3 |
| 41 | plan_change_count | 执行计划变更Plan Change | 次 | T1 |
| 42 | sql_throttle_count | SQL限流Resource Manager Throttle | 次 | T1 |

### 系统/模式 System Pattern

| # | 指标 | 告警显示名 | 单位 | 策略 |
|---|------|-----------|------|------|
| 43 | instance_status | 实例状态Instance Status | — | T5 立即 |
| 44 | dataguard_status | DataGuard状态 | — | T5 |
| 45 | alert_log_ora_errors | 告警日志ORA错误Alert Log | 条 | T1+T4 |
| 46 | job_failure_rate | 作业失败率Job Failure | % | T1 |
| 47 | resource_limit_pct | 资源限制使用率Resource Limit | % | T6 |
| 48 | checkpoint_not_complete | Checkpoint未完成 | 次 | T1 |

## 监控数据模板（按场景定制）

### 数据块定义

| 块 | 名称 | 说明 |
|----|------|------|
| A | 触发信息 | 指标+值变化+阈值+持续时间（所有场景必有） |
| B | 等待事件 Top 5 | 按等待时间占比排序+柱状图 |
| C | Top SQL | sql_id + 耗时 + 并发 + 等待事件 |
| D | SQL文本 | Top SQL 的文本摘要 |
| E | 阻塞链 | 阻塞树: 持有者SID→等待者数量+SQL |
| F | 关键指标 | 场景相关指标的 avg/max/trend |
| G | 空间明细 | 表空间/temp/undo/FRA 各自用量 |
| H | 相关参数 | 场景相关的 DB 参数值 |

### 1. 会话/负载 session_load

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| active/cpu/io 突增 | A + F + B + C + D | — |
| lock_sessions 突增 | A + F + E + C + D | 等待事件Top5（就是锁等待，无信息量） |
| long_sql 突增 | A + F + C + D | — |
| total_sessions 容量 | A + F + 会话分布(status/machine/program) | Top SQL |
| pq_sessions 突增 | A + F + C(标注并行度) + D | — |
| session_creation 突增 | A + F + 连接来源(machine/program) | Top SQL |
| background_wait | A + F + B | Top SQL（用户SQL无关） |
| os_load | A + F(CPU核数 vs 负载) | 等待事件、Top SQL |

### 2. 吞吐量 throughput

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| redo_rate 突增 | A + F(redo相关) + C + D | — |
| hard_parse 突增 | A + F(parse相关) + C + D | — |
| physical_read 加速 | A + F + B + C + D | — |
| logical_read 持续上升 | A + F(趋势,连续窗口值) + C + D | — |
| commit_rate 骤降(T9) | A + F(tps趋势+active趋势) | Top SQL（可能是业务侧） |

### 3. 等待/延迟 wait_latency

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| log_file_sync 延迟 | A + F(redo指标) + B + H(log_buffer,redo log大小) | — |
| db_file_seq/scat_read | A + F(IO指标) + B + C + D | — |
| enqueue_wait 加速 | A + F + E + C | — |
| latch_free_rate 跌破 | A + F(latch+hard_parse) + B | — |
| network_round_trip 偏移 | A + F | SQL（不是SQL的问题） |

### 4. 内存/缓存 memory_cache

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| Buffer Cache命中率下跌 | A + F(physical_read趋势) + H(db_cache_size) + C + D | 等待事件 |
| Library Cache命中率下跌 | A + F(hard_parse趋势) + H(shared_pool_size) | 等待事件、Top SQL |
| PGA使用率 | A + F + H(pga_aggregate_target) | Top SQL |
| Shared Pool空闲率 | A + F(hard_parse,library_cache) + H(shared_pool_size) | — |

### 5. 存储/容量 storage

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| 表空间Tablespace | A + G(各表空间用量+自动扩展+大对象Top3) | 等待事件、Top SQL、SQL文本 |
| 临时表空间Temp | A + G(temp用量) + Temp消耗会话Top3(SID+操作+SQL) | 等待事件 |
| Undo表空间 | A + G(undo用量+retention) + 长事务列表 | 等待事件、Top SQL |
| FRA使用率 | A + G(FRA用量+归档/闪回日志占比) | 等待事件、Top SQL |
| ASM磁盘组 | A + G(各diskgroup用量) | 等待事件、Top SQL |

### 6. Redo/归档 redo_archive

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| 日志切换频率 | A + F(redo_rate,log_switch趋势) + H(redo log组数/大小) + C + D | — |
| 归档延迟 | A + F(archive_lag趋势) + H(archive_dest配置) | SQL |
| 备库同步延迟 | A + F(apply_lag趋势) + H(DG配置) | SQL |
| Redo空间等待(T5复合) | A + F(redo_rate+log_switch) + H(redo log大小) | — |

### 7. 锁/并发 lock_concurrency

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| 阻塞链 | A + E(完整阻塞树+持有者SQL) | 等待事件Top5 |
| 死锁Deadlock | A + E(死锁双方SID+SQL) | 等待事件Top5 |
| Mutex等待 | A + F(mutex名称+争用次数) + B | — |
| 行锁等待 | A + E(阻塞树+持有者SQL) + 被阻塞会话数 | 等待事件Top5 |

### 8. SQL性能 sql_performance

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| Top SQL耗时偏移 | A + C(按偏移倍数排序) + D | — |
| 全表扫描速率上升 | A + C(标注全表扫描SQL) + D | — |
| 执行计划变更 | A + C(标注plan_hash变化+耗时变化) + D | — |
| SQL限流 | A + C(被限流SQL) + H(resource_manager配置) | — |

### 9. 系统/模式 system_pattern

| 子场景 | 展示块 | 不展示 |
|--------|-------|--------|
| 实例状态变更 | A + 实例状态详情(OPEN→RESTRICTED等) | 全部SQL相关 |
| DataGuard状态 | A + DG状态(role,sync_status,gap) | 全部SQL相关 |
| ORA错误 | A + 最近ORA错误列表(时间+错误号+描述) | 等待事件、Top SQL |
| 作业失败 | A + 失败作业列表(job_name,error,last_run) | 等待事件、Top SQL |
| 资源限制 | A + 资源使用详情(processes,sessions的current/limit) | Top SQL |
| Checkpoint未完成 | A + F(redo相关) + H(redo log大小) | — |
