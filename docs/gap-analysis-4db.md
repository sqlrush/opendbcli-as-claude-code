# 四库差距分析：Oracle vs MySQL vs PostgreSQL vs OpenGauss

> 生成时间: 2026-03-21 | 基于代码逐 Skill 深入扫描 + 测试服务器真实输出验证

## 一、逐 Skill 交互差距（核心）

### 监控类

| Skill | Oracle 交互 | MySQL | PG | OG | 差距 | 优先级 |
|-------|------------|-------|-----|-----|------|--------|
| /sessions | ResultTable + Rendered + Summary | ✅ 一致 | ✅ 一致 | ✅ 一致 | 无 | — |
| /activesessions | Panel + 状态图标 + "/kill" 提示 | ✅ Panel | ✅ Panel | ✅ Panel | 无 | — |
| /waits | Panel + 等待类分布 + **主要瓶颈分析** + dominant hint | Panel 但**缺瓶颈分析** | Panel 但**缺瓶颈分析** | 同PG | Oracle 有 waitClassHint 和 dominant 分析，三库缺 | P0 |
| /locks | ResultTable + 空结果友好提示 | ✅ 一致 | ✅ 一致 | ✅ 一致 | 无 | — |
| /blocktree | **树形可视化** (🔒 ├─ └─ │) + kill 建议 | **平表格，无树形** | **平表格，无树形** | 同PG | Oracle 树形 vs 三库平表格，体验差距极大 | **P0** |
| /health | 4+ Panel sections + Overall + Alerts + ✓/⚠ 图标 | Panel 但缺 Overall/Alerts | Panel 但缺 Overall/Alerts | 同PG | 三库缺 Overall 汇总行和 Alerts 段 | P1 |
| /dbtop | 完整实时面板 | **无数据输出** | 待验证 | 待验证 | MySQL dbtop 完全不工作 | **P0** |
| /os | Panel 格式 | Panel (5 sections) | Panel (2 sections) | 同PG | 基本一致 | — |
| /resource | Panel 格式 | 基本一致 | 基本一致 | 同PG | 无 | — |
| /segments | ResultTable | ✅ 一致 | ✅ 一致 | ✅ 一致 | 无 | — |
| /users | ResultText 格式化 | ResultTable 原始 | ResultTable 原始 | 同PG | 三库缺格式化 | P1 |
| /indexhealth | Panel | 基本一致 | 基本一致 | 同PG | 无 | — |
| /perfsnap | Panel (7 calls) | Panel (5 calls) | Panel (少) | 同PG | PG/OG 较简 | P2 |

### 查询类

| Skill | Oracle 交互 | MySQL | PG | OG | 差距 | 优先级 |
|-------|------------|-------|-----|-----|------|--------|
| /slowsql | Panel + **FTS 检测 Plan 列** + 底部提示 | Panel 但**无 FTS 检测** | Panel 但**无 FTS** | 同PG | 三库缺 FTS 检测（MySQL/PG 无 v$sql_plan 等价物，可考虑跳过） | P1 |
| /topsql | Panel + **FTS 检测** + **7 种排序键** | Panel 但**无排序键** | Panel 但**无排序键** | 同PG | 三库缺 7 种排序 (el/ae/lr/pr/al/ap/ex) | **P0** |
| /explain | Panel + **FTS 行标红** ← ⚠ FTS | Panel + FTS 标红 ✅ | Panel + SeqScan 标红 ✅ | 同PG | ✅ 已对齐 | — |
| /sql | ResultTable | ✅ 一致 | ✅ 一致 | ✅ 一致 | 无 | — |
| /ash | ResultTable | ✅ 一致 | ✅ 一致 | ✅ 一致 | 无 | — |
| /tableinfo | 正常 | **无输出 bug** | 待验证 | 待验证 | MySQL tableinfo 功能不可用 | **P0** |

### 管理类

| Skill | Oracle 交互 | MySQL | PG | OG | 差距 | 优先级 |
|-------|------------|-------|-----|-----|------|--------|
| /kill | Panel 两步确认 + 会话详情 | ✅ Panel 确认 | ✅ Panel 确认 + cancel | ✅ 同PG | ✅ 已对齐 | — |
| /alter | Panel + 可读格式 (3G) + SPFILE 提示 | Panel + 可读格式 ✅ | Panel + context 提示 ✅ | ✅ 同PG | ✅ 已对齐 | — |
| /space | Panel + 进度条 + ⚠ 告警 | 待验证 | 待验证 | 待验证 | 需真机验证 | P1 |
| /params | 参数搜索 | 待验证 | 待验证 | 待验证 | 需真机验证 | P1 |
| /alert | 告警日志 | 待验证 | 待验证 | 待验证 | 需真机验证 | P1 |
| /backup | 备份状态 | 待验证 | 待验证 | 待验证 | 需真机验证 | P1 |
| /jobs | 作业状态 | 待验证 | 待验证 | 待验证 | 需真机验证 | P1 |

### AI 类

| Skill | Oracle 交互 | MySQL | PG | OG | 差距 | 优先级 |
|-------|------------|-------|-----|-----|------|--------|
| /diag | 星星闪烁 + 流式 markdown + strategy/playbook/loop | **无星星闪烁** + 流式无格式化 + 基础 agent | 同MySQL | 同MySQL | LLM 路径三库严重缺失 | **P0** |
| /sentinel | 7301 行 / 48 指标 / 9 种算法 / burst 分析 | 1673 行 / ~15 指标 / 基础 | 同MySQL | 同MySQL | 三库 Sentinel 能力仅 Oracle 的 23% | P2 |
| /rule | 28878 行规则引擎 | **未实现** (0 行) | **未实现** | **未实现** | 三库完全无规则引擎 | P2 |

## 二、P0 级差距清单（必须修复）

| # | 差距 | 影响范围 | 具体描述 |
|---|------|---------|---------|
| 1 | /blocktree 无树形可视化 | MySQL/PG/OG | Oracle 用 🔒├─└─│ 树形展示阻塞链，三库只返回平表格 |
| 2 | /dbtop MySQL 无数据 | MySQL | collector 不工作，需排查 SQL 或注册问题 |
| 3 | /tableinfo MySQL 无输出 | MySQL | `/tableinfo mydb.users` 返回空 |
| 4 | /topsql 三库缺排序键 | MySQL/PG/OG | Oracle 支持 7 种排序(el/ae/lr/pr/al/ap/ex)，三库只有默认排序 |
| 5 | /waits 三库缺瓶颈分析 | MySQL/PG/OG | Oracle 有 waitClassHint + dominant 分析，三库只有分布数据 |
| 6 | /diag LLM 路径三库缺失 | MySQL/PG/OG | 无星星闪烁、无流式 markdown 格式化、diagSkill 实例不匹配 |

## 三、P1 级差距清单

| # | 差距 | 影响范围 |
|---|------|---------|
| 7 | /health 缺 Overall + Alerts | MySQL/PG/OG |
| 8 | /users 缺格式化 | MySQL/PG/OG |
| 9 | /space /params /alert /backup /jobs | 需真机验证三库输出 |
| 10 | /slowsql /topsql FTS 检测 | MySQL/PG/OG（但无等价数据源，可能需跳过） |

## 四、P2 级差距清单（长期）

| # | 差距 | 说明 |
|---|------|------|
| 11 | RuleEngine 三库未实现 | 28878 行代码，需基于 ailinkdb/data 生成 |
| 12 | Sentinel 三库简化 | 指标数 15 vs 48，缺 burst 分析 |
| 13 | Agent 编排简化 | 缺 strategy/playbook/loop (3500+ 行) |

## 五、Oracle 独有 Skill（三库无等价）

/awr, /planhistory, /pga, /sga, /redo, /fra, /asm, /latches, /mutexes, /sortusage, /tempsess, /undosess, /standby, /resize, /ora, /ora_kb

## 六、各库独有 Skill

- **MySQL**: /binlog, /bufferpool, /deadlock, /innodb, /replication, /myerr
- **PostgreSQL**: /bloat, /extensions, /longtx, /slots, /sharedbufs, /vacuum, /wal, /xid, /cancel, /pgerr
- **OpenGauss**: /gsmem, /respool, /sessionmem, /sqlcount, /ogerr（扩展 PG）
