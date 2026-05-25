---
name: LLM输出原始SQL而非/命令
description: LLM诊断建议暂时给原始SQL，不强制映射到opendb /命令
type: feedback
---

LLM 诊断输出的修复建议暂时使用原始 SQL，不强制使用 opendb /命令格式。

**Why:** /命令覆盖率不够，LLM 容易给出格式错误的命令（如 `/resize TEMP add 2G` 缺少文件路径参数），导致执行失败。Skill 层也不应为了兼容 LLM 输出而做语法自适应。

**How to apply:**
- prompt 中不再禁止 SQL，要求 LLM 给具体可执行的 SQL
- 以后 skill 覆盖率够了再切回 /命令优先
- 不在 skill 层做参数自动推导（如 auto-generate file path）
