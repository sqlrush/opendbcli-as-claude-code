---
name: architecture-diagnosis-layers
description: 诊断系统三层架构 + 9B/35B 模型能力边界 + 编排式vs自主式策略 + LLM可用工具清单(DB+OS)
type: project
---

# 诊断系统三层架构

## 核心原则

探针层是基座，规则层是兜底，LLM 层才是目标主路径。
但 LLM 层的实现方式取决于模型能力——9B 用编排式，35B+ 用自主式。

**Sentinel 和 /diag 的规则引擎只陈述事实，不出结论。**
- 告警只展示：什么指标、当前值、为什么触发（阈值/策略），用 DBA 能看懂的语言
- 不输出"性能异常"等模糊判断词，不做根因推断
- 所有结论（根因、严重程度、影响范围）统一由 LLM 层给出
- 用户是专业 DBA，能看懂原始监控指标

## Layer 1: 探针层（永远是 OpenDB 做）

- 轻探针 1 SQL/秒，7 项指标
- 3σ 异常检测，多指标优先级触发
- 爆发采集 200ms/帧，最长 30s
- 数据聚合压缩（150 帧 → 结构化摘要）

**这层不变，LLM 做不了也不该做。**

## Layer 2: 规则兜底（无 LLM 时的降级方案）

- classify.go 的规则分类
- 静态展示：触发信息 + 数据摘要 + SQL 文本
- 不给"建议"，不下强"结论"（措辞用"冲高"不用"风暴/瓶颈"）
- 定位：让 DBA 自己看数据判断

**9B 场景下，classify.go 不是偏差而是必要——它替代了 9B 做不好的自主推理。**

## Layer 3: LLM 诊断（有 LLM 时）

实现方式按模型能力分两种策略：

### GuidedStrategy（编排式，9B 适用）

OpenDB 决定"查什么"，Qwen 负责"怎么解读"。每步单轮理解，不依赖多步上下文。

```
Step 1: burst 摘要 → Qwen → 现象总结 + 初步判断
Step 2: OpenDB 自动调 /explain Top SQL → Qwen → 计划是否有问题
Step 3: 如果全表扫描，OpenDB 自动调 /tableinfo → Qwen → 缺什么索引
Step 4: OpenDB 汇总前几步结果 → Qwen → 最终报告 + 可执行建议
```

### AutonomousStrategy（自主式，35B+ 适用）

LLM 自己看数据 → 提假设 → 选工具验证 → 迭代修正 → 综合结论。
输入不含 Classification.Cause，不预设答案。

## 9B vs 35B 能力边界

| 能力 | 9B | 35B-A3B | 70B+ |
|------|-----|---------|------|
| 单轮理解数据 | 强 | 强 | 强 |
| Function Calling | 可用 | 稳定 | 稳定 |
| 2-3 步推理链 | 尚可 | 稳定 | 稳定 |
| 5-10 步链式推理 | 不稳定 | 半自主(3-5步) | 全自主 |
| 中途自我纠正 | 弱 | 可用 | 强 |
| 多线索交叉分析 | 弱 | 尚可 | 强 |

**升级路径**: 9B(GuidedStrategy) → 35B-A3B(AutonomousStrategy, 兜底回退) → 70B+(全自主)

## LLM 可用工具清单

### 数据库侧（已有 skill）

| 工具 | 说明 |
|------|------|
| `/explain <sql_id>` | 执行计划 |
| `/tableinfo <table>` | 表结构、索引、统计信息 |
| `/params <name>` | 数据库参数 |
| `/locks` | 锁信息 |
| `/waits` | 等待事件 |
| `/activesessions` | 活跃会话 |
| `/slowsql` | 慢 SQL |
| `/latches` | Latch 争用 |
| `/mutexes` | Mutex 争用 |
| `/standby` | DataGuard 状态 |
| `/alert` | 告警日志 |
| `/backup` | 备份状态 |
| `/space` | 表空间使用率 |
| `/indexadvise` | 索引建议 |
| `/sql SELECT ...` | 自由查询（ASH/AWR/任意视图） |

### OS 侧（需新增 `/os` skill）

| 命令 | 说明 |
|------|------|
| `iostat -xmt 1 3` | 磁盘 I/O：每设备 await/util/%busy |
| `mpstat -P ALL 1 3` | CPU：每核使用率，看单核打满 |
| `free -m` / `vmstat 1 3` | 内存/Swap：是否在换页 |
| `top -bn1 -o %MEM \| head -20` | 进程级 CPU/MEM 占用 |
| `df -h` | 文件系统使用率 |
| `ss -s` / `netstat -i` | 网络连接数/错误（DG 场景） |
| `tail -100 alert_*.log` | Oracle 告警日志最近 ORA- 错误 |
| `lsnrctl status` | 监听器状态 |
| `asmcmd lsdg` | ASM 磁盘组空间 |
| `dmesg -T \| tail -50` | 内核日志（OOM、磁盘故障） |

安全控制：
- `/os` skill 设为 LevelDangerous，需确认
- agent auto 模式白名单放行只读命令（iostat/mpstat/free/df/top/tail/cat/lsnrctl status）
- 禁止 rm/kill/shutdown 等破坏性操作

## 代码层面

把编排逻辑抽成可配置的 `DiagnoseStrategy` 接口：
- `GuidedStrategy`：9B 用，OpenDB 编排每一步
- `AutonomousStrategy`：35B+ 用，LLM 自主调工具

## 当前状态

- Layer 1（探针层）：已实现，完善
- Layer 2（规则兜底）：已实现，6 类根因 + 7 类待实现
- Layer 3（LLM 诊断）：框架已有（agent loop），待实现 GuidedStrategy + `/os` skill
