---
name: LLM诊断输出优先用/命令
description: LLM生成的诊断建议必须优先使用opendb的/命令而非原始SQL，缺失的命令记录为gap
type: feedback
---

## 核心原则

LLM 诊断输出（紧急措施、根因修复）中涉及数据库交互的操作：

1. **优先使用 opendb 的 `/命令`**（skill），而非提供原始 SQL
2. 如果对应的 `/命令` 暂时不具备该功能，opendb 记录为 skill gap，后续持续完善
3. 目标：LLM 尽可能通过 opendb 的 skill 体系完成全部诊断工作

## 示例对照

| 操作 | ❌ 当前（原始SQL） | ✅ 目标（/命令优先） |
|------|-------------------|---------------------|
| 查临时空间会话 | `SELECT ... FROM v$sort_usage ...` | `/tempsessions`（待开发） |
| 终止会话 | `ALTER SYSTEM KILL SESSION ...` | `/kill <sid>`（已有） |
| 查临时文件 | `SELECT ... FROM dba_temp_files` | `/space temp`（需增强） |
| 扩容表空间 | `ALTER DATABASE TEMPFILE ... RESIZE` | 无对应skill，提供SQL + 记录gap |
| 查参数 | `SHOW PARAMETER ...` | `/params <name>`（已有） |
| 改参数 | `ALTER SYSTEM SET ...` | `/alter`（待开发） |

## 输出格式

LLM 建议中应同时提供：
- `/命令` 方式（优先展示）
- 原始 SQL（作为补充或 skill 不存在时的fallback）
