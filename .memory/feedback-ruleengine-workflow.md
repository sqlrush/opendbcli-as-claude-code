---
name: 规则引擎开发流程
description: opendb规则引擎的规则必须先在ailinkdb/data中生成规则数据，再基于数据生成代码，切勿直接写代码
type: feedback
---

opendb 规则引擎的开发流程：

1. **先生成规则数据** → https://github.com/sqlrush/ailinkdb/tree/main/data
2. **再基于规则数据生成代码** → opendb/internal/ruleengine/

**Why:** 规则数据是由 Opus 4.6（或 AilinkDB 的 AI 大脑）审核生成的，保证规则质量。代码只是数据的编码形式。直接写代码会绕过质量审核。

**How to apply:**
- 有新规则需求 → 先让 Opus 4.6 把规则数据写入 ailinkdb/data
- 基于 ailinkdb/data 中的规则数据生成 opendb ruleengine Go 代码
- 绝不直接在 opendb 中手写规则代码
- ailinkdb 仓库: https://github.com/sqlrush/ailinkdb
