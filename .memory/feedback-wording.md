---
name: feedback-wording
description: 诊断根因描述措辞规范：禁用"问题/风暴/瓶颈/抖动"等定性用词，统一用"xxx冲高"，需精细化评估才能升级措辞
type: feedback
---

诊断根因描述的用词原则：

## 禁用词 → 替代词
- "问题" → 不用（如"问题SQL"→"SQL并发冲高"）
- "风暴" → "冲高"（如"硬解析风暴"→"硬解析冲高"）
- "瓶颈" → "冲高"（如"Redo瓶颈"→"Redo冲高"）
- "抖动" → "冲高"（如"I/O抖动"→"I/O冲高"）

## 核心原则
- 仅超过阈值 ≠ 数据库已到极限，不能用定性/夸张用词
- 需要**精细化评估策略**才能升级措辞（如对比容量上限、持续时间、影响范围）
- DBA 对措辞极其敏感，夸大描述会降低工具可信度

## 当前根因命名
| RootCauseType | 显示名 |
|---|---|
| CauseBadSQL | SQL并发冲高 |
| CauseIOSubsystem | 存储I/O冲高 |
| CauseLatchStorm | Latch争用冲高 |
| CauseRedoBottleneck | Redo冲高 |
| CauseLockContention | 锁等待阻塞 |
| CauseTrafficStorm | 流量冲高 |
