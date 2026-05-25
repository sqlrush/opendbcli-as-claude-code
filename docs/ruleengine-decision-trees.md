# 规则引擎决策树目录

> 来源: ailinkdb 训练数据 150 个 Oracle 文件全量扫描 + DBA 专家知识
> 范围: Oracle（当前），MySQL/PostgreSQL（后续）
> 状态: 全量扫描完成，待实现

## 数据来源统计

| 扫描组 | 文件数 | 会话数 | 内容 |
|--------|--------|--------|------|
| Perf batch (batch2-14) | 14 | ~120 | 等待事件/SQL调优/AWR/内存/IO/并发/统计/复杂SQL |
| Oracle QA + v3 | 3 | ~53 | 通用 DBA 问答 |
| A 系列 (HA) | 18 | ~235 | Data Guard/RAC/GoldenGate/ASM/RMAN/Flashback |
| B 系列 (性能) | 11 | ~55 | SPM/CBO/内存/IO/并行/AWR/ASH/等待/Undo/Latch/In-Memory |
| C 系列 (运维) | 13 | ~65 | CDB/PDB/安全/分区/空间/调度/网络/升级/RM/Redo/用户/DP/表空间 |
| D 系列 (新特性) | 8 | ~40 | 19c/21c/自动化/Exadata/MV/索引/Vault/Text |
| E 系列 (反幻觉) | 8 | ~45 | v$/DBA_视图/参数/DBMS_包/常见错误 |
| F 系列 (进阶) | 10 | ~50 | PL/SQL/触发器/异常/集合/动态SQL/批量/LOB/AQ/Pipe/XMLDB |
| G 系列 (高级管理) | 7 | ~35 | 诊断包/AWR/ADDM/调度/审计/Resource Manager/自动任务 |
| H 系列 (高级场景) | 9 | ~45 | 分析函数/层次/DML/正则/子查询/CTE/PIVOT/安全/OLTP |
| SQL 系列 | 33 | ~120 | 执行计划分析/JOIN优化/子查询/分区/并行/统计偏斜/TEMP溢出/窗口 |
| EMG 系列 (故障) | 14 | ~70 | ORA-600/4031/1555/空间满/RAC/DG/Hang/损坏/CPU/IO/内存/恢复/演练/复盘 |
| **合计** | **~150** | **~890+** | |

## 规则引擎分层架构

```
Layer 0: 实时诊断规则 (BurstReport 静态分析, 60 个核心场景)
         └─ 分析 sentinel 采集的数据，无需额外查询，毫秒级响应

Layer 1: 深度诊断规则 (主动查询 + 决策树, ~200 个场景)
         └─ 通过 skill 执行额外 SQL 查询，秒级响应

Layer 2: 知识库规则 (专家经验, ~600+ 个场景)
         └─ HA/运维/新特性/SQL调优/PL/SQL 等专业领域知识
         └─ 用于 LLM 增强 或 离线报告生成
```

## Layer 0: 核心诊断规则 (60 个场景)

| 类别 | 场景数 | 说明 |
|------|--------|------|
| A. 等待事件诊断 | 15 | Top 等待事件逐一诊断 |
| B. SQL性能诊断 | 8 | 执行计划/解析/统计信息 |
| C. 内存诊断 | 6 | SGA/PGA/Buffer Cache |
| D. I/O与存储诊断 | 5 | 物理读写/存储延迟 |
| E. 锁与并发诊断 | 6 | 行锁/死锁/Latch/Mutex |
| F. Redo与归档诊断 | 4 | Redo/归档/检查点 |
| G. 空间诊断 | 5 | 表空间/TEMP/Undo/FRA/ASM |
| H. Undo诊断 | 3 | ORA-1555/段争用/保留 |
| I. 故障应急诊断 | 5 | ORA-600/Hang/损坏/DG |
| J. 连接与会话诊断 | 3 | 连接耗尽/长事务/泄漏 |
| **合计** | **60** | **首期实现** |

---

## A. 等待事件诊断 (15 个场景)

### A01. db file sequential read (单块随机读)

**触发**: I/O 等待中 sequential read 排名靠前且 avg wait > 10ms
**数据**: /waits, /topsql, /sga (buffer cache), /sql

```
db file sequential read 等待冲高
├─ avg wait > 20ms?
│   ├─ 是 → 存储子系统慢（SSD应<5ms, HDD应<10ms）
│   │   ├─ 检查存储延迟 (iostat)
│   │   └─ 考虑 Smart Flash Cache / 升级存储
│   └─ 否 → 逻辑读过多导致物理读多
├─ Buffer Cache 命中率 < 95%?
│   ├─ 是 → 增大 db_cache_size
│   │   └─ V$DB_CACHE_ADVICE 评估效果
│   └─ 否 → 特定 SQL 的索引回表过多
├─ 定位 Top SQL (ASH 中该等待最多的 SQL)
│   ├─ 单 SQL 占比 > 50% → /explain 检查执行计划
│   │   ├─ 索引回表多 → 考虑覆盖索引
│   │   └─ 索引选择性差 → /indexadvise
│   └─ SQL 分散 → 存储子系统问题
└─ 阈值: SSD < 5ms 正常, HDD < 10ms 正常, > 20ms 需处理
```

### A02. db file scattered read (多块全扫描读)

**触发**: scattered read 等待占比高（OLTP 中不应排前列）
**数据**: /waits, /topsql, /explain

```
db file scattered read 等待冲高
├─ 系统类型?
│   ├─ DW/报表 → scattered read 是正常主力事件
│   └─ OLTP → 不应排名靠前，存在不合理全表扫描
├─ 定位 Top SQL
│   ├─ 有 WHERE 条件但走全表 → 缺少索引
│   │   └─ /indexadvise 或 /explain 分析
│   ├─ 无 WHERE 或大范围 → 确认业务是否需要全量
│   │   └─ 考虑并行/分区/压缩/Direct Path Read
│   └─ 统计信息过期导致优化器选错 → 收集统计信息
├─ avg wait > 20ms?
│   ├─ 是 → 存储慢 + db_file_multiblock_read_count 评估
│   └─ 否 → 问题在 SQL 层面
└─ 阈值: OLTP 中占比 > 20% DB Time 需关注
```

### A03. log file sync (提交等待)

**触发**: Commit 类等待 > 20% 或 log file sync avg > 5ms
**数据**: /waits, /redo

```
log file sync 等待冲高
├─ avg wait 分析
│   ├─ SSD: 正常 < 1ms, 关注 > 3ms, 严重 > 10ms
│   └─ HDD: 正常 < 5ms, 关注 > 10ms
├─ log file sync vs log file parallel write 差值?
│   ├─ 接近 → 瓶颈在物理 I/O → 优化存储
│   │   ├─ Redo Log 文件迁移到 SSD/NVMe
│   │   └─ 检查 iostat w_await
│   └─ 差值大 → LGWR 调度/CPU 不足/提交过频
│       ├─ 应用层 commit 频率过高 → 批量提交
│       └─ CPU 争用 → 检查 CPU 使用率
├─ Redo Log 切换频率?
│   ├─ < 15分钟/次 → Redo Log 太小，增大到 1-4GB
│   └─ > 15分钟/次 → 大小合理
├─ commit_logging / commit_wait 参数?
│   └─ 非关键数据可用 NOWAIT 降低延迟（有数据风险）
└─ 阈值: avg > 5ms(SSD) / > 10ms(HDD) 需处理
```

### A04. log file parallel write (LGWR 写入)

**触发**: 与 A03 关联，parallel write avg > 10ms
**数据**: /waits, /redo, /params

```
log file parallel write 等待冲高
├─ avg wait > 10ms? → 存储写入慢
│   ├─ Redo Log 文件位置 → 确认在高速存储上
│   ├─ 成员数 > 2 → 减少冗余成员或分散到不同存储
│   └─ iostat 确认写延迟
├─ Redo 生成量 > 50MB/s? → 业务写入量大
│   ├─ 检查是否有大批量 DML 操作
│   └─ 考虑 NOLOGGING（有恢复风险）
├─ Log Switch > 4次/小时?
│   ├─ 是 → 增大 Redo Log 文件
│   │   ├─ < 2GB/hr redo → 1GB 文件
│   │   ├─ 2-8GB/hr → 2GB 文件
│   │   └─ > 8GB/hr → 4GB 文件
│   └─ 否 → 大小合理
└─ 阈值: avg > 10ms 或 Redo > 50MB/s 需处理
```

### A05. buffer busy waits (缓冲区忙等待)

**触发**: buffer busy waits 占比 > 5% DB Time
**数据**: /waits, /topsql, ASH (P3 block class)

```
buffer busy waits 等待冲高
├─ P3 block class 判断
│   ├─ data block (class 1)
│   │   ├─ 集中在索引 → 热块争用
│   │   │   ├─ 右增长索引 → 反转键索引 / HASH 分区
│   │   │   └─ 增大 PCTFREE 分散行
│   │   └─ 集中在表 → 并发 INSERT 同一块
│   │       └─ ASSM + HASH 分区分散
│   ├─ undo header (class 4) → Undo 段不足
│   │   └─ 增大 Undo 表空间触发更多段
│   ├─ undo block (class 8/9) → 一致性读争用
│   │   └─ 增大 Undo 保留时间
│   └─ segment header → ASSM 迁移 (MSSM → ASSM)
├─ ASH block# 集中度 > 50%?
│   ├─ 是 → 单一热块 → 需拆分/分区
│   └─ 否 → 多块均匀争用 → I/O 子系统
└─ 阈值: > 5% DB Time 需处理
```

### A06. cursor: pin S wait on X (游标硬解析争用)

**触发**: 该等待事件排名靠前 或 硬解析率 > 20%
**数据**: /waits, /latches, /sql (v$sql parse stats)

```
cursor: pin S wait on X 等待冲高
├─ 硬解析率 (hard parse / execute)?
│   ├─ > 0.2 → 大量 Literal SQL，未用绑定变量
│   │   ├─ 紧急: cursor_sharing = FORCE（可能次优计划）
│   │   └─ 长期: 推动应用改造使用绑定变量
│   └─ < 0.2 → 非绑定变量问题
├─ version_count > 100 的 SQL 数量?
│   ├─ 多 → 子游标版本膨胀
│   │   ├─ 检查 V$SQL_SHARED_CURSOR 原因
│   │   │   ├─ AUTH_CHECK_MISMATCH → 不同 Schema
│   │   │   ├─ BIND_MISMATCH → 绑定类型不一致
│   │   │   └─ OPTIMIZER_MISMATCH → ACS 问题
│   │   └─ _cursor_obsolete_threshold 降到 100
│   └─ 少 → 游标失效重编译
│       ├─ 频繁 DDL / 统计信息收集 → 错开时间
│       └─ NO_INVALIDATE = AUTO_INVALIDATE
├─ Shared Pool Free < 10%?
│   ├─ 是 → 增大 shared_pool_size
│   └─ 否 → 不是空间问题
└─ 阈值: Hard Parse > 100/s 或 parse_rate > 0.2 需处理
```

### A07. enq: TX - row lock contention (行锁争用)

**触发**: TX 等待排名靠前 或 BlockingChains 存在
**数据**: /locks, /blocktree, /activesessions

```
enq: TX - row lock contention
├─ lock mode 判断 (P1 低 16 位)
│   ├─ mode 6 (Exclusive) → 行级锁冲突（最常见）
│   │   ├─ blocking_session 定位阻塞者
│   │   ├─ 阻塞者 INACTIVE + last_call_et 大?
│   │   │   ├─ 是 → 忘记 COMMIT → 通知应用方或 KILL
│   │   │   └─ 否 → 大事务执行中 → 等待或调优
│   │   └─ 12c+: FINAL_BLOCKING_SESSION 找根源
│   └─ mode 4 (Share) → 三种子场景
│       ├─ ITL 不足 → ALTER TABLE INITRANS 16 + MOVE
│       ├─ 唯一索引冲突 → 两会话插相同键
│       └─ Bitmap 索引 DML → 改为 B-tree
├─ 受害会话数?
│   ├─ >= 10 → 严重，考虑立即 KILL 阻塞源
│   ├─ >= 5 → 高，通知并评估
│   └─ < 5 → 中，监控
└─ 阈值: 受害者 >= 2 触发规则, >= 5 高严重度
```

### A08. enq: TM - contention (表锁争用)

**触发**: TM 等待事件出现
**数据**: /locks, /sql (dba_constraints + dba_ind_columns)

```
enq: TM - contention
├─ 外键列无索引? (60%+ 的 TM 争用原因)
│   ├─ 查 dba_constraints + dba_ind_columns 交叉验证
│   ├─ 确认缺索引 → CREATE INDEX ON 子表(fk_column) ONLINE
│   └─ 加索引后对比 TM 等待下降
├─ DDL 与 DML 冲突?
│   ├─ 是 → 避免高峰期 DDL
│   └─ 否 → 继续排查
├─ 显式 LOCK TABLE?
│   ├─ 是 → 审查应用代码
│   └─ 否 → 继续排查
└─ 风险: MOVE 表后索引变 UNUSABLE，必须立即重建
```

### A09. enq: HW - contention (HWM 扩展争用)

**触发**: HW 等待出现，ASH 中占比 > 10%
**数据**: /waits, /sql (dba_segments allocation_type)

```
enq: HW - contention
├─ Direct Path INSERT 造成?
│   ├─ 是 → INSERT APPEND 扩展 HWM
│   │   └─ 并发 > 50 个 INSERT 会话 → HASH 分区分散
│   └─ 否 → 常规 INSERT 扩展
├─ 表空间分配方式?
│   ├─ UNIFORM → 改为 AUTOALLOCATE
│   └─ AUTOALLOCATE → 正常
├─ 并发 INSERT 会话数 > 50?
│   ├─ 是 → HASH 分区 (8-32 个分区)
│   └─ 否 → 问题不严重
└─ 阈值: ASH > 10% 且并发 INSERT > 50 需处理
```

### A10. library cache lock/pin (库缓存争用)

**触发**: library cache lock/pin 等待出现
**数据**: /latches, /waits, /sql (v$librarycache)

```
library cache lock / library cache pin
├─ 区分层级
│   ├─ library cache lock → DDL 与 DML/查询并发
│   │   └─ 避免业务高峰期 DDL
│   ├─ library cache pin → 硬解析需要编译游标
│   │   └─ 减少硬解析（绑定变量）
│   └─ 两者组合 → 系统性解析压力
├─ 硬解析率 > 5%?
│   ├─ 是 → 绑定变量/cursor_sharing
│   └─ 否 → 特定对象频繁失效
├─ RELOADS > 1000/hour?
│   ├─ 是 → 对象频繁失效
│   │   └─ NO_INVALIDATE = AUTO_INVALIDATE
│   └─ 否 → 正常
└─ 阈值: parse_calls/executions > 0.3 或 RELOADS > 1000/hr
```

### A11. gc buffer busy (RAC 全局缓存争用)

**触发**: gc buffer busy acquire/release 等待出现（RAC 环境）
**数据**: /waits, /sql (v$cr_block_server)

```
gc buffer busy (RAC)
├─ 互联网络延迟?
│   ├─ gc block receive time > 1ms → 网络问题
│   │   └─ 检查互联交换机/带宽
│   └─ < 1ms → 不是网络问题
├─ 热块类型?
│   ├─ data block → 数据热块
│   │   └─ HASH 分区或 Service Affinity 分散
│   ├─ index leaf → 索引叶块热点
│   │   └─ 反转键索引或 HASH 分区索引
│   └─ undo header → DRM 频率问题
├─ gc buffer busy > 15% DB Time?
│   ├─ 是 → 严重，需立即处理
│   └─ 否 → 监控趋势
└─ 阈值: gc block receive > 1ms 或 gc busy > 15% DB Time
```

### A12. read by other session (会话间读等待)

**触发**: read by other session 等待出现
**数据**: /waits, /sga

```
read by other session
├─ 存储基线: db file sequential read avg > 10ms?
│   ├─ 是 → 存储慢，多个会话排队读同一块
│   └─ 否 → 并发读同一数据块
├─ Buffer Cache Hit < 90%?
│   ├─ 是 → Cache 太小，增大 db_cache_size
│   └─ 否 → 特定块被频繁淘汰又读入
├─ 并发扫描同一大表?
│   ├─ 是 → CACHE/NOCACHE 控制 + 分时调度
│   └─ 否 → 热点数据块 → KEEP pool
└─ 阈值: Buffer Cache Hit < 90% 或 avg wait > 10ms
```

### A13. db file parallel write (DBWR 瓶颈)

**触发**: DBWR 相关等待出现
**数据**: /waits, /params

```
db file parallel write (DBWR)
├─ 存储写延迟 > 10ms?
│   ├─ 是 → 存储子系统写入慢
│   └─ 否 → DBWR 配置问题
├─ free buffer waits 出现?
│   ├─ 是 → Buffer Cache 太小或脏块刷太慢
│   └─ 否 → 不严重
├─ 异步 IO 启用?
│   ├─ 否 → filesystemio_options = SETALL
│   └─ 是 → 正常
├─ 检查点频率?
│   ├─ > 10次/hour → MTTR 设置过小
│   └─ 正常
└─ 阈值: avg > 10ms 或 free buffer waits 出现
```

### A14. direct path read/write (直接路径 I/O)

**触发**: direct path read 等待占比高
**数据**: /waits, /topsql, /params

```
direct path read/write
├─ 并行查询触发?
│   ├─ 是 → 正常行为，优化 DOP
│   └─ 否 → 串行 direct path read (11g+)
├─ 表大小 vs _small_table_threshold?
│   ├─ 表 > 5x threshold → 几乎一定走 direct path
│   │   └─ 如需走 Buffer Cache → 调大 threshold 或 CACHE hint
│   └─ 表在 1-5x threshold → 取决于缓存比例
├─ direct path read 前 checkpoint 开销?
│   └─ 脏块多 → checkpoint 耗时 → 考虑提前刷脏
└─ 阈值: _small_table_threshold 默认约 Buffer Cache 2%
```

### A15. free buffer waits (空闲缓冲区等待)

**触发**: free buffer waits 等待出现
**数据**: /waits, /sga, /params

```
free buffer waits
├─ Buffer Cache 太小?
│   ├─ Hit Ratio < 95% → 增大 db_cache_size
│   └─ > 95% → DBWR 刷脏太慢
├─ DBWR 写入慢?
│   ├─ db file parallel write > 10ms → 存储慢
│   └─ 否 → DBWR 进程不足
│       └─ db_writer_processes 增加（不超过 CPU 数）
├─ 异步 IO?
│   ├─ 未启用 → filesystemio_options = SETALL
│   └─ 已启用 → 存储层面瓶颈
└─ 阈值: free buffer waits 出现即需关注
```

---

## B. SQL 性能诊断 (8 个场景)

### B01. SQL 执行计划漂移

**触发**: 单 SQL elapsed 突增但并发不高，plan_hash_value 变化
**数据**: /explain, /topsql, /sql (dba_hist_sqlstat)
**需要新 skill**: `/ash` (v$active_session_history 聚合查询)

```
SQL 突然变慢
├─ plan_hash_value 变了?
│   ├─ 是 → 执行计划漂移
│   │   ├─ 最近收集统计信息? → 锁定旧计划或调整统计
│   │   ├─ 绑定变量 ACS 切换? → 检查 is_bind_sensitive
│   │   └─ 修复优先级:
│   │       ├─ 1. SPM Baseline 锁定好计划
│   │       ├─ 2. SQL Profile
│   │       ├─ 3. SQL Patch
│   │       └─ 4. 重新收集统计信息
│   └─ 否 → 计划没变
│       ├─ 数据量增长? → 需要新索引或分区
│       ├─ 锁等待? → /locks
│       └─ I/O 变慢? → /waits
└─ 阈值: elapsed_time > 2x 历史均值
```

### B02. 全表扫描冲高

**触发**: OLTP 系统中 scattered read 占比高
**数据**: /topsql, /explain, /indexadvise

```
全表扫描冲高
├─ Top SQL 中全表扫描的 SQL?
│   ├─ 有 WHERE 条件 → 缺少索引
│   │   └─ /indexadvise 分析
│   ├─ 统计信息过期 → 优化器误判
│   │   └─ DBMS_STATS.GATHER_TABLE_STATS
│   └─ Hint 强制全扫描 → 审查 SQL
├─ 表很小 (< 1000 行)?
│   └─ 全表扫描可能比索引快 → 正常
└─ 阈值: OLTP 中 scattered read > 20% DB Time
```

### B03. HASH JOIN 内存不足

**触发**: PGA 工作区 multipass 操作
**数据**: /pga, /explain, /topsql

```
HASH JOIN 内存不足
├─ 执行计划中 Mem 列显示 MULTIPASS?
│   ├─ 是 → PGA 不够
│   │   ├─ OLTP: PGA = 20% 可用内存
│   │   ├─ DSS: PGA = 50% 可用内存
│   │   └─ 单个 SQL 太大 → SQL Patch 限制
│   └─ 否 (OPTIMAL) → 正常
├─ build side 选择正确?
│   ├─ 小表应为 build side
│   └─ 优化器选错 → SWAP_JOIN_INPUTS hint
├─ 两表 > 10GB?
│   └─ 考虑并行 HASH JOIN (DOP = CPU/2)
└─ 阈值: multipass 操作 → 10-100x 性能下降
```

### B04. 硬解析冲高

**触发**: hard parse count > 100/s 或 parse_ratio > 0.2
**数据**: /latches, /health, /sql (v$sysstat)

```
硬解析冲高
├─ Literal SQL (未用绑定变量)?
│   ├─ 大量相同模式不同字面值 → 确认
│   │   ├─ 紧急: cursor_sharing = FORCE
│   │   └─ 长期: 应用层改造
│   └─ 否 → 其他原因
├─ 对象频繁失效?
│   ├─ DDL/统计信息收集 → 错开高峰
│   └─ NO_INVALIDATE = AUTO_INVALIDATE
├─ Shared Pool 不足?
│   ├─ Free < 10% → 增大 shared_pool_size
│   └─ 碎片严重 → 4K chunks 占比 > 50% → flush + resize
└─ 阈值: hard parse > 100/s; parse_ratio > 0.2; SP Free < 10%
```

### B05. 绑定变量窥视 / ACS 问题

**触发**: 同一 SQL 执行时间波动大
**数据**: /explain, /sql (v$sql bind_sensitive)
**需要新 skill**: `/cursor` (游标统计: version_count, bind_sensitive, ACS)

```
绑定变量窥视问题
├─ SQL 是否 bind_sensitive?
│   ├─ 是 → ACS (Adaptive Cursor Sharing) 启用
│   │   ├─ 子游标过多 (> 20) → ACS 产生太多计划
│   │   │   └─ SQL Profile / SPM Baseline 锁定
│   │   └─ 合理数量 → 正常 ACS 行为
│   └─ 否 → 传统窥视
│       ├─ 首次执行绑定值非典型 → 执行计划不适合多数情况
│       └─ 统计信息有直方图 → 优化器才会考虑绑定值
├─ 修复策略
│   ├─ SPM Baseline 锁定好计划
│   ├─ SQL Profile (force_match = TRUE)
│   └─ 12.2+: optimizer_adaptive_statistics = FALSE
└─ 阈值: child cursor > 20 需检查 ACS
```

### B06. 统计信息过期

**触发**: E-Rows vs A-Rows 差 > 10x
**数据**: /explain, /tableinfo, /sql (dba_tab_statistics)
**需要新 skill**: `/stats` (统计信息状态: 表/索引 stale, last_analyzed, histogram)

```
统计信息过期
├─ E-Rows vs A-Rows 差异?
│   ├─ > 100x → 几乎确定统计问题
│   ├─ > 10x → 很可能统计问题
│   └─ < 10x → 统计信息 OK, 排查其他原因
├─ 表上次收集时间?
│   ├─ > 7 天 + stale = YES → 需要收集
│   │   └─ DBMS_STATS.GATHER_TABLE_STATS(..., cascade=>TRUE)
│   └─ 近期刚收集 → 可能需要直方图
│       ├─ 数据倾斜 + 无直方图 → METHOD_OPT 指定
│       └─ NDV <= 254 → Frequency 直方图
├─ 紧急恢复?
│   └─ DBMS_STATS.RESTORE_TABLE_STATS (默认保留 31 天)
└─ 阈值: E/A > 10x; stale > 7 天; 直方图: NDV <= 254 用 Frequency
```

### B07. 子游标版本膨胀

**触发**: version_count > 100
**数据**: /sql (v$sqlarea, v$sql_shared_cursor)

```
子游标版本膨胀
├─ version_count > 100 的 SQL
│   ├─ V$SQL_SHARED_CURSOR 找原因
│   │   ├─ AUTH_CHECK_MISMATCH → 不同 Schema 执行
│   │   ├─ BIND_MISMATCH → 绑定类型不一致
│   │   ├─ OPTIMIZER_MISMATCH → ACS 生成过多计划
│   │   └─ 其他 → Oracle Bug 可能
│   └─ 修复
│       ├─ _cursor_obsolete_threshold = 100 (默认 8192)
│       ├─ DBMS_SHARED_POOL.PURGE 清除问题 SQL
│       └─ Bug → 打 One-off Patch
├─ 紧急缓解
│   └─ PURGE('address,hash_value', 'C')
└─ 阈值: version_count > 100 异常; > 1000 严重
```

### B08. 并行查询数据倾斜

**触发**: PX 操作中某 Slave 耗时远大于其他
**数据**: /topsql, /sql (v$pq_tqstat)

```
并行查询数据倾斜
├─ V$PX_SESSTAT 各 Slave physical reads 差异?
│   ├─ > 3x → 存在数据倾斜
│   │   ├─ Hybrid Hash Distribution (12c+) 自动处理
│   │   ├─ PQ_DISTRIBUTE hint 手动控制
│   │   └─ PQ_SKEW hint (12.2+)
│   └─ < 3x → 正常
├─ PX Slave 使用率 > 80%?
│   └─ 是 → 减小 parallel_max_servers 或限制 DOP
├─ 长期方案
│   └─ HASH 分区分散热点数据
└─ 阈值: physical reads 差异 > 3x; PX 使用率 > 80%
```

---

## C. 内存诊断 (6 个场景)

### C01. Buffer Cache 命中率低

**触发**: hit ratio < 95% (OLTP) 或 < 85% (DW)
**数据**: /sga, /health, /params

```
Buffer Cache 命中率低
├─ V$DB_CACHE_ADVICE 评估?
│   ├─ 加倍后 read_factor 下降 > 10% → 增大有效
│   └─ 下降 < 5% → 不是 Cache 大小问题
├─ KEEP pool 配置?
│   ├─ 热点小表 → ALTER TABLE STORAGE(BUFFER_POOL KEEP)
│   └─ 大表批量扫描 → RECYCLE pool
├─ SGA > 物理内存 60%?
│   ├─ 是 → 已到上限, 优化 SQL 减少逻辑读
│   └─ 否 → 可以增大
├─ HugePages 配置?
│   ├─ SGA > 8GB + Linux → 必须配 HugePages
│   └─ 未配 → 页表开销大(32GB SGA = 64MB/进程)
└─ 阈值: OLTP < 95%; DW < 85%; read_factor > 10%
```

### C02. Shared Pool 碎片 / ORA-4031

**触发**: ORA-4031 错误 或 Shared Pool Free < 10%
**数据**: /sga, /health, /sql (v$sgastat)

```
Shared Pool 碎片 / ORA-4031
├─ Free Memory < 10%?
│   ├─ 是 → 空间不足
│   │   ├─ 增大 shared_pool_size
│   │   └─ 设最小值保护: shared_pool_size 下限
│   └─ 否 → 碎片问题
├─ 4K 以下碎片 > 50%?
│   ├─ 是 → 碎片严重
│   │   ├─ Literal SQL → cursor_sharing
│   │   └─ 频繁换出 → DBMS_SHARED_POOL.KEEP 固定重要对象
│   └─ 否 → 大对象挤占
├─ Reserved Pool 配置?
│   └─ shared_pool_reserved_size = shared_pool_size * 5-10%
└─ 阈值: Free < 10%; 碎片 > 50%; ORA-4031 = 紧急
```

### C03. PGA 工作区不足

**触发**: PGA cache hit < 90% 或 overalloc_count > 0
**数据**: /pga, /params

```
PGA 工作区不足
├─ V$PGA_TARGET_ADVICE 评估
│   ├─ estd_overalloc_count > 0 → 严重不足
│   │   └─ 必须增大 PGA_AGGREGATE_TARGET
│   └─ cache hit < 90% → 不足
│       └─ 按 ADVICE 建议值调整
├─ 个别 SQL multipass?
│   └─ SQL Patch 限制单 SQL 内存使用
├─ 配比建议
│   ├─ OLTP: PGA = 20% 可用内存
│   ├─ DSS: PGA = 50% 可用内存
│   └─ 混合: 30-40%
└─ 阈值: cache hit < 90%; overalloc > 0 = 严重
```

### C04. SGA 组件配比失衡

**触发**: 某组件频繁 resize 或大小不合理
**数据**: /sga, /params, /sql (v$sga_resize_ops)

```
SGA 组件配比失衡
├─ 使用 AMM 还是 ASMM?
│   ├─ AMM (memory_target > 0)
│   │   ├─ SGA > 8GB + Linux → 必须改 ASMM + HugePages
│   │   └─ 小系统 → AMM 可接受
│   └─ ASMM (memory_target = 0)
│       └─ 检查各组件最小值是否设置
├─ 频繁 resize (v$sga_resize_ops)?
│   ├─ 是 → 自动调整来回切换
│   │   └─ 设 db_cache_size / shared_pool_size 最小值
│   └─ 否 → 正常
├─ Buffer Cache vs Shared Pool 比例?
│   ├─ OLTP: Buffer Cache 60-70%, Shared Pool 15-20%
│   └─ 混合: Buffer Cache 50-60%, Shared Pool 20-25%
└─ 阈值: resize > 10次/天 不正常
```

### C05. Large Pool 不足

**触发**: large pool free < 10% 或相关等待
**数据**: /sga, /params

```
Large Pool 不足
├─ 使用场景
│   ├─ Shared Server → UGA * sessions * 1.5
│   ├─ RMAN → channels * 32MB
│   └─ PX message pool → 需要
├─ 配比: large_pool_size = 需求总和 * 1.3
└─ 阈值: free < 10%
```

### C06. Result Cache 争用

**触发**: Result Cache Latch 等待出现
**数据**: /sga, /waits, /params

```
Result Cache 争用
├─ result_cache_mode = FORCE?
│   ├─ 是 → 改为 MANUAL (只缓存标记的查询)
│   └─ 否 → 特定查询问题
├─ 高频 DML 表上启用 RC?
│   ├─ DML > 10000/天 → 不适合 RC
│   └─ DML < 1000/天 → 适合
├─ RAC 环境?
│   └─ 高频 DML + RAC → RC Latch 争用严重 → 禁用
├─ result_cache_max_size?
│   └─ 建议 Shared Pool 2-5%
└─ 阈值: DML > 10000/天 不适合; FORCE 模式不推荐
```

---

## D. I/O 与存储诊断 (5 个场景)

### D01. 存储延迟冲高

**触发**: 多种 I/O 等待事件 avg wait 同时升高
**数据**: /waits, /params
**需要新 skill**: `/os` (OS 级指标: iostat/mpstat/free)

```
存储延迟冲高
├─ sequential read + scattered read avg 同时升高?
│   ├─ 是 → 存储子系统问题
│   │   ├─ SSD: > 5ms 异常
│   │   ├─ HDD: > 20ms 异常
│   │   └─ 检查 OS iostat
│   └─ 否 → 单类型升高 → 参考 A01/A02
├─ 异步 IO 启用?
│   ├─ filesystemio_options != SETALL → 设为 SETALL
│   └─ 已设置 → 存储层面问题
├─ 备份/RMAN 抢 I/O?
│   └─ 调整备份时间或限制 I/O 带宽
└─ 阈值: SSD > 5ms; HDD > 20ms
```

### D02. ASM 磁盘组不均衡

**触发**: ASM 磁盘使用率方差 > 5%
**数据**: /asm

```
ASM 磁盘组不均衡
├─ 磁盘使用率方差 > 5%?
│   ├─ 是 → rebalance
│   │   ├─ 业务时段: POWER 1-2
│   │   └─ 维护窗口: POWER 8-11
│   └─ 否 → 均衡正常
├─ 磁盘大小差异 > 20%?
│   └─ 替换为相同大小磁盘
├─ 单盘延迟?
│   ├─ HDD > 20ms → 替换
│   └─ SSD > 5ms → 替换
└─ 阈值: 方差 > 5%; 大小差 > 20%
```

### D03. 数据文件自动扩展问题

**触发**: 表空间 > 85% 或自动扩展配置不当
**数据**: /space, /sql (dba_data_files autoextensible)
**需要新 skill**: `/datafile` (数据文件状态: autoextend/maxsize/HWM)

```
数据文件自动扩展
├─ 表空间 > 85%?
│   ├─ HWM waste (data_pct < 30%)? → 表空间碎片
│   │   ├─ < 10GB ASSM → SHRINK SPACE
│   │   ├─ > 10GB 12c+ → MOVE ONLINE
│   │   └─ > 50GB → CTAS 重建
│   └─ 真的快满 → 扩容
├─ AUTOEXTEND NEXT < 100MB?
│   └─ 太小 → 增大到 512MB-1GB
├─ 已达 MAXSIZE?
│   └─ 增大 MAXSIZE 或添加新文件
└─ 阈值: > 85% 告警; > 95% 紧急; data_pct < 30% 碎片
```

### D04. Redo Log 切换过频

**触发**: Log Switch > 4次/小时
**数据**: /redo, /params

```
Redo Log 切换过频
├─ 切换频率?
│   ├─ > 4次/小时 → 需要增大文件
│   │   ├─ Redo < 2GB/hr → 1GB 文件
│   │   ├─ 2-8GB/hr → 2GB 文件
│   │   └─ > 8GB/hr → 4GB 文件
│   └─ 2-4次/小时 → 边界，评估
├─ checkpoint incomplete?
│   ├─ 是 → Group 数不够 (4-6 组)
│   └─ 否 → 正常
├─ archiving needed?
│   └─ 检查归档目标空间
└─ 阈值: 切换 > 4/hr; 理想 15-20 分钟/次
```

### D05. 异步 IO 未启用

**触发**: filesystemio_options != SETALL (非 ASM)
**数据**: /params

```
异步 IO 未启用
├─ ASM 环境?
│   ├─ 是 → 自动启用, 无需设置
│   └─ 否 → 文件系统
├─ filesystemio_options?
│   ├─ SETALL → 正确
│   ├─ 其他 → 改为 SETALL
│   └─ 需要: libaio 已安装 + aio-max-nr >= 1048576
├─ 验证: V$DATAFILE ASYNCH_IO = 'ASYNC_ON'
└─ 阈值: 非 SETALL 即需修正
```

---

## E. 锁与并发诊断 (6 个场景)

### E01. 行锁阻塞链

→ 与 A07 合并，见 A07

### E02. 死锁

**触发**: trace 文件中 deadlock detected 或 deadlock 计数增加
**数据**: /alert, /locks, /sql (trace 文件)

```
死锁诊断
├─ 死锁类型 (trace 文件)?
│   ├─ TM-TM (表锁) → 外键无索引 (最常见)
│   │   └─ 加索引
│   ├─ TX-TX mode 4 (行锁交叉) → DML 顺序不一致
│   │   └─ 统一操作顺序
│   └─ TX-TX mode 6 (ITL) → INITRANS 不足
│       └─ 增大 INITRANS
├─ 频率?
│   ├─ > 10/天 → 紧急
│   ├─ 1-10/天 → 需排查
│   └─ < 1/天 → 重试机制即可
└─ 阈值: > 10/天 紧急; 外键无索引占 60%+
```

### E03. ITL 不足

**触发**: ITL waits > 10000/天
**数据**: /waits, /sql (dba_tables INITRANS)

```
ITL 不足
├─ ITL waits 频率?
│   ├─ > 10000/天 → 紧急
│   └─ < 10000/天 → 评估
├─ 当前 INITRANS?
│   ├─ 表: 1-2 → 增到 8-16
│   ├─ 索引: 2 → rebuild 到 16
│   └─ 并发 > 20/块 → INITRANS = 并发 * 80%
├─ MSSM → 迁移到 ASSM
└─ 阈值: > 10000/天 紧急; INITRANS 默认 1-2
```

### E04. Sequence 缓存不足

**触发**: row cache lock 等待出现
**数据**: /waits, /sql (dba_sequences)

```
Sequence 缓存不足
├─ row cache lock 等待中 sequence?
│   ├─ 是 → CACHE 太小
│   │   └─ ALTER SEQUENCE ... CACHE 1000+
│   └─ 否 → 其他 row cache 争用
├─ NOORDER + RAC?
│   └─ NOORDER 性能更好, ORDER 保证顺序
└─ 阈值: CACHE 默认 20 → 建议 1000+
```

### E05. 热块争用 (CBC Latch)

**触发**: latch: cache buffers chains 占 > 10% DB Time
**数据**: /latches, /sql (ASH P1RAW → V$BH)

```
热块争用 (CBC Latch)
├─ sleeps/gets > 1%?
│   ├─ 是 → 争用频繁
│   └─ 否 → 正常范围
├─ 热块类型?
│   ├─ 索引根块/分支块 → Hash 分区索引 / 反转键
│   ├─ 表热点块 → 减少逻辑读 / PCTFREE 分散
│   └─ Undo Header → Undo 段争用方案
├─ 根本方案: 找 buffer_gets 最高的 Top SQL
│   └─ 优化 SQL 减少逻辑读
├─ 风险: 反转键索引不支持范围扫描
└─ 阈值: > 10% DB Time; sleeps/gets > 1%
```

### E06. Mutex 争用

**触发**: cursor: mutex X/S 等待出现
**数据**: /mutexes, /sql (v$mutex_sleep_history)

```
Mutex 争用
├─ 等待类型?
│   ├─ cursor: pin S wait on X → 硬解析阻塞软解析 (→ A06)
│   ├─ cursor: mutex S → V$SQL 访问冲突
│   ├─ cursor: mutex X → 子游标版本过多 (→ B07)
│   ├─ library cache: mutex X → DDL 冲突
│   └─ cursor: pin S → 短暂等待, 大量才需关注
├─ 紧急缓解
│   └─ DBMS_SHARED_POOL.PURGE 清除问题 SQL
└─ 阈值: 排进 Top 5 Events 需处理
```

---

## F. Redo 与归档诊断 (4 个场景)

### F01. Redo 生成速率过高

→ 与 A03/A04/D04 关联

### F02. 归档目标空间满

**触发**: archiver hung 或归档目标 > 90%
**数据**: /alert, /fra
**需要新 skill**: `/archive` (归档状态: dest status, gap, space)

```
归档空间满
├─ 数据库已挂起 (DML 卡住)?
│   ├─ 是 → 立即释放空间
│   │   ├─ 有 RMAN 备份 → DELETE ARCHIVELOG COMPLETED BEFORE 'SYSDATE-3'
│   │   └─ 无备份 → 先备份再删或临时切换归档目的地
│   └─ 否 → 有时间安全处理
├─ FRA 满还是自定义目录满?
│   ├─ FRA → 增大 db_recovery_file_dest_size
│   └─ 自定义 → 添加新目的地或清理
├─ 有 Data Guard?
│   ├─ 是 → 删前确认备库已应用 (gap 检查)
│   └─ 否 → 确认 RMAN 备份后删除
├─ 根因
│   ├─ 备份失败 → 过期归档未清理
│   ├─ 业务量突增 → Redo 生成暴增
│   ├─ DG 备库断开 → 归档不敢删
│   └─ retention 过长 → 保留太多天
└─ 阈值: > 90% 告警; 挂起 = 紧急
```

### F03. 检查点不完整

**触发**: alert.log 中 checkpoint not complete
**数据**: /alert, /redo, /params

```
检查点不完整
├─ Redo Log 组数不够?
│   ├─ < 4 组 → 增加到 4-6 组
│   └─ >= 4 组 → 文件太小
├─ MTTR 设置?
│   ├─ fast_start_mttr_target < 60s → 可能太激进
│   └─ 增大允许更长恢复时间
├─ DBWR 写入慢?
│   └─ 参考 A13
└─ 阈值: 出现 checkpoint not complete 即需处理
```

### F04. 归档进程阻塞

**触发**: archiver stuck
**数据**: /alert, /sql (v$archive_dest)

```
归档进程阻塞
├─ 目标空间满? → F02
├─ 网络问题 (remote dest)?
│   └─ 检查 DG 传输
├─ 归档进程挂起?
│   └─ ALTER SYSTEM ARCHIVE LOG ALL
└─ 阈值: 出现即紧急
```

---

## G. 空间诊断 (5 个场景)

### G01. 表空间使用率冲高

**触发**: 使用率 > 85%
**数据**: /space, /sql (dba_tablespace_usage_metrics)

### G02. TEMP 空间不足

**触发**: TEMP 使用率 > 90% 或 ORA-01652
**数据**: /tempsess, /space, /pga

```
TEMP 空间不足
├─ 磁盘排序 > 5%?
│   ├─ 是 → PGA 太小
│   │   └─ PGA < 20% RAM → 增大
│   └─ 否 → 特定大查询
├─ ORA-01652?
│   └─ 找占用最多的会话 → /tempsess
├─ direct path temp 等待?
│   ├─ > 10ms → TEMP 放 SSD
│   └─ 否 → 空间问题
├─ enq: ST contention?
│   └─ 确认 Locally Managed
└─ 阈值: > 90% 或 ORA-01652; 磁盘排序 > 5%
```

### G03. Undo 空间不足

→ 见 H 类

### G04. FRA 空间满

→ 与 F02 关联, /fra 数据

### G05. ASM 磁盘组空间

→ /asm 数据, 与 D02 关联

---

## H. Undo 诊断 (3 个场景)

### H01. ORA-01555 Snapshot Too Old

**触发**: ORA-01555 错误
**数据**: /sql (v$undostat), /undosess
**需要新 skill**: `/undo` (Undo 综合状态: v$undostat, retention, space, tuned_undoretention)

```
ORA-01555 诊断
├─ maxquerylen > undo_retention?
│   ├─ 是 → 长查询是根因
│   │   ├─ 优化查询减少运行时间
│   │   └─ 调大 undo_retention (>= maxquerylen x 1.5)
│   └─ 否 → 继续排查
├─ UNDO 使用率 > 90%?
│   ├─ 是 → 空间不足
│   │   ├─ EXPIRED < 10% → 必须扩容
│   │   └─ EXPIRED > 30% → 空间够, 调 retention
│   └─ 否 → 继续排查
├─ 大批量 DML 同时运行?
│   ├─ 是 → 分批提交 / 错开执行
│   └─ 否 → 继续排查
├─ Fetch Across Commit?
│   ├─ 是 → 修改应用逻辑
│   └─ 否 → 可能是延迟块清除
├─ 环境配比
│   ├─ OLTP → retention 900-1800s
│   ├─ 混合 → 1800-3600s
│   └─ DW → 3600-7200s
└─ 验证: 连续 7 天 SSOLDERRCNT = 0
```

### H02. Undo 段争用

**触发**: undo segment extension 或 buffer busy waits (Undo Header)
**数据**: /undosess, /waits, /sql (v$rollstat)

```
Undo 段争用
├─ 争用类型
│   ├─ undo segment extension → 段频繁扩展
│   ├─ buffer busy waits (Undo Header) → 段头争用
│   └─ buffer busy waits (Undo Block) → 数据块争用
├─ txns_per_seg > 5?
│   ├─ 是 → 段数不足, 增大 Undo 表空间
│   └─ 否 → 段数合理
├─ 大量小事务逐行 COMMIT?
│   └─ 改为批量 COMMIT
├─ 12c+ TEMP_UNDO_ENABLED?
│   └─ TRUE → 临时表 Undo 写 TEMP, 减压
└─ 阈值: txns_per_seg < 5 合理
```

### H03. Undo 保留时间不足

**触发**: unexpiredblkreucnt > 0 或 tuned_undoretention < maxquerylen
**数据**: /sql (v$undostat), /params

```
Undo 保留不足
├─ unexpiredblkreucnt > 0?
│   ├─ 是 → 未过期 Undo 被强制重用
│   │   ├─ RETENTION GUARANTEE → DML 可能失败
│   │   └─ NOGUARANTEE → 调大 retention + 扩空间
│   └─ 否 → 正常
├─ Undo 大小规划
│   └─ peak_blk_per_sec x undo_retention x db_block_size x 1.3
├─ _undo_autotune 启用?
│   ├─ 是 → Oracle 自动调, 可能占过多空间
│   └─ 否 → 严格按参数执行
└─ 阈值: Undo = 计算值 x 1.3 安全系数
```

---

## I. 故障应急诊断 (5 个场景)

### I01. ORA-00600 内部错误

**触发**: ORA-00600 出现
**数据**: /alert

```
ORA-00600 排查
├─ 实例崩溃?
│   ├─ 是 → 检查自动恢复 → 联系 Oracle Support
│   └─ 否 → 继续分析
├─ 重复发生?
│   ├─ 是 + 与特定 SQL 关联 → 优化 SQL / 搜 Bug 补丁
│   ├─ 是 + 不关联 SQL → 可能数据损坏或 Bug
│   └─ 否 → 记录监控
├─ 有可用补丁?
│   ├─ 是 → 测试并应用
│   └─ 否 → 提交 SR
├─ 涉及数据损坏?
│   ├─ 是 → RMAN 恢复或 DBMS_REPAIR
│   └─ 否 → Workaround 或等补丁
└─ 参考: MOS Note 153788.1
```

### I02. ORA-07445 内部错误

→ 与 I01 类似，MOS Note 153788.1

### I03. 数据库 Hang

**触发**: 所有会话卡住，无法新建连接
**数据**: /alert, Hanganalyze dump
**需要新 skill**: `/hanganalyze` (触发 hanganalyze dump 并解析)

```
数据库 Hang
├─ 能连上 SQLPLUS?
│   ├─ 是 → 执行 Hanganalyze
│   │   └─ oradebug hanganalyze 3
│   └─ 否 → OS 级别诊断
│       └─ kill -USR2 pmon_pid (强制 trace)
├─ 阻塞链分析
│   ├─ 单一 root blocker → KILL SESSION
│   └─ 循环依赖 → 数据库重启
├─ Library Cache 死锁?
│   └─ DDL + DML 交叉 → 识别并 KILL DDL 会话
└─ 阈值: 出现即最高紧急
```

### I04. 数据文件损坏

**触发**: ORA-01578 block corruption
**数据**: /alert, /backup

```
数据文件损坏
├─ 物理损坏 vs 逻辑损坏?
│   ├─ 物理 → RMAN RECOVER DATAFILE
│   └─ 逻辑 → DBMS_REPAIR 标记跳过
├─ 块级恢复?
│   ├─ RMAN BLOCKRECOVER → 最小影响
│   └─ 全文件恢复 → 影响该文件所有用户
├─ Standby 可用?
│   └─ 从 Standby 复制好块
└─ 阈值: 出现即紧急
```

### I05. Data Guard 同步中断

**触发**: apply lag 增大或 gap 出现
**数据**: /standby, /alert

```
DG 同步中断
├─ transport lag?
│   ├─ 是 → 网络问题或主库归档传输失败
│   └─ 否 → apply 端问题
├─ apply lag?
│   ├─ 持续增大 → MRP 进程问题
│   │   └─ 检查备库 alert.log
│   └─ 偶尔 → 大事务或备库 I/O 慢
├─ gap?
│   ├─ 是 → 缺失的归档 → 从主库传输
│   └─ 否 → 正常追赶
└─ 阈值: lag > 10min 告警; gap 出现 = 紧急
```

---

## J. 连接与会话诊断 (3 个场景)

### J01. 连接数耗尽

**触发**: sessions 逼近 processes 上限 (> 85%)
**数据**: /resource, /sessions, /activesessions

```
连接数耗尽
├─ v$resource_limit sessions 使用率?
│   ├─ > 95% → 紧急
│   ├─ > 85% → 告警
│   └─ < 85% → 正常
├─ 连接泄漏 (同一 program 大量 idle)?
│   ├─ 是 → 修复连接池
│   └─ 否 → 业务真需要
├─ 长期方案
│   ├─ DRCP (Database Resident Connection Pool)
│   ├─ 增大 processes/sessions
│   └─ 优化连接池 min/max
└─ 阈值: > 85% 告警; > 95% 紧急
```

### J02. 长事务

**触发**: 活跃事务运行 > 阈值 (如 30min)
**数据**: /activesessions, /undosess

```
长事务
├─ 是否在等待锁?
│   ├─ 是 → 处理锁问题 (→ A07)
│   └─ 否 → SQL 本身慢
├─ Undo 使用量增长?
│   ├─ 快速增长 → 大批量 DML → 分批提交
│   └─ 稳定 → 查询类长事务
├─ 影响其他会话?
│   ├─ 阻塞其他 → 评估 KILL
│   └─ 不阻塞 → 监控
└─ 阈值: > 30min 关注; > 2hr 需评估
```

### J03. 会话泄漏

**触发**: idle 会话持续增长
**数据**: /sessions, /resource

```
会话泄漏
├─ 按 program/machine 聚合 idle 会话
│   ├─ 某来源大量 idle → 连接池配置问题
│   └─ 分散 → 多个应用问题
├─ idle_time > 阈值?
│   └─ PROFILES 设 IDLE_TIME 限制
├─ 长期方案
│   └─ 连接池 idle_timeout 配置
└─ 阈值: idle 会话持续增长 + 总数逼近上限
```

---

## 新增 Skill 需求

| 新 Skill | 说明 | 涉及场景 | 优先级 |
|----------|------|----------|--------|
| `/ash` | ASH 实时聚合 (v$active_session_history Top SQL/Event/Session) | B01, B05, E05 | P0 |
| `/undo` | Undo 综合状态 (v$undostat, retention, space, tuned_undoretention) | H01-H03 | P0 |
| `/stats` | 统计信息状态 (表/索引 stale, last_analyzed, histogram, E-Rows vs A-Rows) | B06 | P1 |
| `/cursor` | 游标统计 (version_count, bind_sensitive, shared_cursor) | B05, B07, A06 | P1 |
| `/datafile` | 数据文件状态 (autoextend, maxsize, HWM waste) | D03 | P1 |
| `/archive` | 归档状态 (dest status, gap, applied, space) | F02, F04, I05 | P1 |
| `/os` | OS 级指标 (iostat, mpstat, free, df) | D01 | P2 |
| `/hanganalyze` | Hanganalyze dump 触发与解析 | I03 | P2 |

**现有 Skill 可覆盖**: /waits, /topsql, /explain, /sga, /pga, /redo, /fra, /asm, /locks, /blocktree, /latches, /mutexes, /space, /activesessions, /sessions, /resource, /undosess, /tempsess, /sortusage, /params, /health, /alert, /standby, /backup, /indexadvise, /tableinfo, /sql (万能补充)
