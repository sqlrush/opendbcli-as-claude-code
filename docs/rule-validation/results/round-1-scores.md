# Round 1 — Oracle Rule Engine 验证评分表

## T001 — 行锁级联：blocker 是空闲会话

### Rule 真实输出
```
根因: 默认 — 通用 TX 争用诊断
严重程度: ■■□□ 中    置信度: 58%

证据链:
1. enq: TX 争用，需进一步区分锁模式

处置建议:
[排查] 1. 从 ASH 获取 TX 锁的详细模式
       > /ash detail event='enq: TX - row lock contention'
```

### Opus 标准诊断
```
Step 1: 检测到 lock_sessions=15, 等待 enq:TX row lock 占 93.8% → 严重锁阻塞
Step 2: 查阻塞链 → root blocker SID=1338, user=TESTUSER, 15个victim
Step 3: 查 blocker 状态 → event='PL/SQL lock timer'(空闲/sleep), 非DB工作
Step 4: 判定: blocker 持有未提交事务在空闲 → kill session + 修复应用提交逻辑

根因: 未提交事务持锁，blocker 处于空闲/应用等待状态
严重程度: CRITICAL (88%会话被阻塞)
建议:
  1. ALTER SYSTEM KILL SESSION '1338,xxx' IMMEDIATE
  2. 修复应用在 DML 后及时 COMMIT
  3. 设置 IDLE_TIME profile 限制
```

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | **8/40** | Rule 输出"通用TX争用"，未识别 blocker 是空闲状态持锁。走到了默认分支。 |
| 修复建议(30) | **5/30** | 只建议"查 ASH"，未给出 kill session、修复应用、设 IDLE_TIME 任何可执行建议 |
| 严重程度(10) | **3/10** | Rule 判定"中"，实际应为 CRITICAL（88%会话阻塞） |
| 排查路径(10) | **4/10** | 采集了阻塞链数据（好），但决策树在 blocker 分析步骤走了默认分支 |
| 完整性(10) | **2/10** | 缺失：blocker event 分析、victim 比例评估、kill 命令、应用修复建议、预防措施 |
| **总分** | **22/100** | |

### Rule 优化方案

#### 问题 1: ORA_086 决策树缺少"blocker 空闲但 status=ACTIVE"分支

**当前逻辑**: `idle_seconds > 300 → RC1(空闲blocker)`, 否则 → RC2(热点行)。SID=1338 的 `last_call_et` 只有几秒（SLEEP 刚开始），所以走了默认。

**需要增加**: 检查 blocker 的**当前等待事件**，而非仅看 idle 时间。

```json
新增 diagnostic_query: "step3b_blocker_event"
SQL: "SELECT s.sid, s.event AS blocker_event,
  CASE WHEN s.event IN ('PL/SQL lock timer','SQL*Net message from client',
    'Streams AQ: waiting for messages in the queue','pipe get')
  OR s.status = 'INACTIVE' THEN 'IDLE_PATTERN'
  ELSE 'WORKING' END AS blocker_activity
FROM v$session s
WHERE s.sid IN (SELECT DISTINCT blocking_session FROM v$session
  WHERE event='enq: TX - row lock contention' AND blocking_session IS NOT NULL)"
```

新增 root_cause RC5:
```json
{
  "id": "RC5",
  "name": "未提交事务持锁(blocker空闲)",
  "probability": "30%",
  "desc": "Blocker 完成 DML 后未提交，处于应用等待/空闲状态"
}
```

在决策树 step4 中增加三路分支:
1. `blocker_activity = 'IDLE_PATTERN'` → RC5, severity=critical, 建议 kill + 修复应用
2. `idle_sec > 300` → RC1, severity=high
3. default → RC2 热点行

#### 问题 2: 缺少 victim 影响评估

**需要增加**: 在决策树开头加 impact 评估步骤:
```json
新增 diagnostic_query: "step0_victim_impact"
SQL: "SELECT COUNT(*) victim_count,
  ROUND(COUNT(*)*100.0/NULLIF((SELECT COUNT(*) FROM v$session WHERE type='USER'),0),1) blocked_pct
FROM v$session WHERE event='enq: TX - row lock contention'"
```

分支: `blocked_pct > 50` → severity 提升到 critical

#### 问题 3: 缺少可执行的修复建议

RC5 的 actions 应包含:
```json
[
  {"type":"urgent","desc":"Kill 空闲 blocker","sql":"ALTER SYSTEM KILL SESSION '{sid},{serial#}' IMMEDIATE;"},
  {"type":"fix","desc":"修复应用在 DML 后及时 COMMIT"},
  {"type":"preventive","desc":"设置 IDLE_TIME 资源限制","sql":"ALTER PROFILE DEFAULT LIMIT IDLE_TIME 10;"}
]
```

#### 涉及修改文件
1. `internal/oracle/ruleengine/rules_json/ORA_086_row_lock_contention.json` — 增加 step3b_blocker_event, RC5, 三路分支
2. `internal/oracle/ruleengine/rules_json/ORA_085_lock_diagnosis_general.json` — 同样增加 blocker event 检查
3. `internal/oracle/sentinel/types.go` — BlockingChain 增加 RootOwnEvent/RootStatus 字段

---

---

## T002 — 行锁级联：blocker 在跑慢 SQL

### Rule 真实输出
```
根因: 默认 — 通用 TX 争用诊断
严重程度: ■■□□ 中    置信度: 58%
证据链: enq: TX 争用，需进一步区分锁模式
建议: /ash detail event='enq: TX - row lock contention'
```

### 现场数据
- 18 sessions 活跃, 93.8% 等待 "enq: TX - row lock contention"
- SID=960 blocker, ACTIVE, SQL_ID=353h2z3j2tf3t (PL/SQL block doing FTS), 15 victims
- Top SQL: SQL_ID=9msvy6zjmvrdu 67.5s wait, 15 concurrent

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 8/40 | "通用TX争用"，未识别 blocker 是 ACTIVE 状态运行慢 SQL |
| 修复建议(30) | 5/30 | 只建议查 ASH，未给出 kill session、优化 SQL、锁超时建议 |
| 严重程度(10) | 3/10 | "中"，实际 93.8% 阻塞应为 HIGH/CRITICAL |
| 排查路径(10) | 5/10 | 采集了阻塞链（好），但未深入分析 blocker 的 SQL |
| 完整性(10) | 2/10 | 缺失：blocker SQL 分析、kill 命令、SQL 优化、锁超时建议 |
| **总分** | **23/100** | |

### Rule 优化方案
- 同 T001：ORA_085/086 需要增加 blocker 状态分析分支
- 额外：当 blocker status=ACTIVE 且有 SQL_ID → 查 SQL 执行计划，建议优化
- 区分 blocker 空闲（→ kill）vs blocker 跑慢 SQL（→ 优化 SQL）

---

## T005 — 死锁频率判断 (5对交叉更新)

### Rule 真实输出
```
根因: 默认 — 通用 TX 争用诊断
严重程度: ■■□□ 中    置信度: 58%
证据链: enq: TX 争用，需进一步区分锁模式
建议: /ash detail event='enq: TX - row lock contention'
```

### 现场数据
- 12 sessions 活跃, 90.9% 等待 "enq: TX - row lock contention"
- 两组互锁链: SID=2473 ↔ SID=1523
- 频繁触发 ORA-00060 死锁

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 5/40 | 未识别死锁模式，走了默认TX争用分支 |
| 修复建议(30) | 3/30 | 只建议查ASH，未给出死锁处理建议 |
| 严重程度(10) | 3/10 | "中"，90.9%阻塞+频繁死锁应为HIGH |
| 排查路径(10) | 4/10 | 采集了阻塞链但未识别循环依赖 |
| 完整性(10) | 2/10 | 缺失：enqueue_deadlocks 统计、ORA-00060 检测、频率分析、重试逻辑建议 |
| **总分** | **17/100** | |

### Rule 优化方案
- **新增死锁检测**: 查 v$sysstat WHERE name='enqueue_deadlocks'，值>0 触发死锁诊断
- **互锁链识别**: 当阻塞链存在循环（A blocks B 且 B blocks A）→ 判定死锁
- **频率分析**: 根据 delta enqueue_deadlocks 判断频率 → 低频建议重试逻辑，高频建议统一访问顺序
- **新增规则**: ORA_xxx_deadlock_detection.json

---

## T010 — HW 争用 (30路并发INSERT)

### Rule 真实输出
```
根因: 并发 DML <= 20 — 中等争用
严重程度: ■■■□ 高    置信度: 68%
证据链: ITL 槽位不足，默认 INITRANS 1-2 无法满足并发需求
建议: 增大 INITRANS 到 8
关联: enq: TX mode 4 — 唯一键冲突诊断 (下游症状)
```

### 现场数据
- 32 sessions 活跃, 93.5% 等待 "enq: TX - contention"（从 HW contention 演变）
- SID=2473 阻塞 29 个
- 30 路并发 INSERT 到新表（小 INITIAL extent）
- 实际根因: undo segment extension + HW contention

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 12/40 | 识别到并发争用（好），但误诊为 ITL 争用。实际是 undo/HW 争用 |
| 修复建议(30) | 8/30 | INITRANS 建议对 HW 争用无效，应建议增大 undo/调整 ASSM |
| 严重程度(10) | 7/10 | 正确判定为"高" |
| 排查路径(10) | 5/10 | 识别了阻塞模式，但走错分支（ITL 而非 HW） |
| 完整性(10) | 3/10 | 缺失：HW 分析、undo 大小检查、ASSM vs MSSM、批量提交建议 |
| **总分** | **35/100** | |

### Rule 优化方案
- **enq: HW - contention 需要独立规则**: 不应被归入 TX 争用分支
- **新增 HW 诊断**: 检查表空间 ASSM 设置、undo 大小、segment 扩展频率
- **区分 TX contention 子类型**: enq: TX mode 4 (share) = undo 争用，非唯一键冲突
- **建议**: 增大 undo_tablespace、使用 ASSM、分散 INSERT 目标、批量提交

---

*（后续场景 T006-T100 评分待模拟后补充）*

---

## T004 — 多层阻塞链 (A→B→C + 15 victims)

### Rule 真实输出
```
根因: 默认 — 通用 TX 争用诊断
严重程度: 中    置信度: 58%
证据链: enq: TX 争用，需进一步区分锁模式
建议: /ash detail event='enq: TX - row lock contention'
```

### 现场数据
- 3层链: SID=3(root,PL/SQL lock timer) → SID=572 → SID=957 → 15 victims
- Wait: enq: TX - row lock contention 94.4%

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 8/40 | 未识别多层链，未定位 root blocker，走默认分支 |
| 修复建议(30) | 5/30 | 只建议查 ASH，未给出 kill root blocker 建议 |
| 严重程度(10) | 3/10 | 判定"中"，实际 17/20 会话阻塞应为 CRITICAL |
| 排查路径(10) | 4/10 | 采集了阻塞链（好），但没解析多层链关系 |
| 完整性(10) | 2/10 | 缺失：链深度分析、root blocker定位、kill命令 |
| **总分** | **22/100** | |

### Rule 优化方案
- **ORA_085/086 需要增加链深度分析**: 当 blocking_chains 有多层时，递归找到 root (blocking_session IS NULL 的那个)
- **需要区分 root blocker 的层级**: 给出"kill SID=3 可解除整条链"的建议

---

## T009 — Sequence 争用 (NOCACHE)

### Rule 真实输出
```
根因: RC3/RC4: shared_pool 或并发登录
严重程度: 中    置信度: 70%
证据链: 已排除 RC2(频繁DDL), 根因 shared_pool 过小导致字典缓存被换出
建议: 增大 shared_pool_size
```

### 现场数据
- 30 sessions 活跃, 96.8% 等待 "row cache lock"
- SQL_ID=2r5ndmbf6h3ht (NEXTVAL), 30 并发
- 阻塞链: SID=1715 阻塞 28 个

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 10/40 | 识别到 row cache lock（好），但误判为 shared_pool 问题。实际是 sequence NOCACHE 导致 dc_sequences 争用 |
| 修复建议(30) | 5/30 | "增大 shared_pool" 不对，正确是 ALTER SEQUENCE xxx CACHE 1000 |
| 严重程度(10) | 5/10 | "中"偏低，96.8%阻塞应为 HIGH |
| 排查路径(10) | 6/10 | 走了 row cache lock 规则链（对），但在 dc_sequences vs dc_objects 分支判断错误 |
| 完整性(10) | 4/10 | 缺失：sequence CACHE 值检查、具体 sequence 名称定位 |
| **总分** | **30/100** | |

### Rule 优化方案
- **ORA_194 (row_cache_lock) 需要增加 dc_sequences 特定分支**:
  - 查 V$ROWCACHE 定位是哪个 cache 争用（dc_sequences / dc_objects / dc_users）
  - 如果是 dc_sequences → 查 DBA_SEQUENCES WHERE CACHE_SIZE <= 20 → 建议增大 CACHE
  - 新增 diagnostic_query: `SELECT sequence_name, cache_size FROM user_sequences WHERE cache_size <= 20`
  - 新增 root_cause: "Sequence NOCACHE/低CACHE 导致 row cache lock"

---

## T020 — 硬解析风暴

### Rule 真实输出
```
根因: subpool分布正常
严重程度: 低    置信度: 55%
证据链: shared pool subpool分布均匀
```

### 现场数据
- 12 sessions 活跃, 90.9% 等待 "latch: shared pool"
- hard_parse=36062, parse_total=1009452
- 等待事件: latch: shared pool 9个 + library cache: bucket mutex X 1个

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 5/40 | 完全漏诊！识别到 latch: shared pool 等待但判定"正常"。未关联到硬解析是根因 |
| 修复建议(30) | 0/30 | 无任何修复建议 |
| 严重程度(10) | 2/10 | 判定"低"，实际 90.9% 会话阻塞应为 HIGH |
| 排查路径(10) | 3/10 | 查了 subpool 分布（合理步骤之一），但漏了检查 hard_parse_rate |
| 完整性(10) | 2/10 | 缺失：hard_parse 率检查、V$SQL 绑定变量分析、CURSOR_SHARING 建议 |
| **总分** | **12/100** | |

### Rule 优化方案
- **ORA_186 (latch contention) 需要增加 hard_parse 关联检查**:
  - 当 latch='shared pool' 时，必须检查 hard_parse_rate
  - 新增分支: `hard_parse_rate > 100/s → 根因是硬解析, 非 shared_pool 大小问题`
  - 新增 diagnostic_query: `SELECT name, value FROM v$sysstat WHERE name LIKE 'parse count%'`
  - 新增建议: "应用层使用绑定变量" + "临时 ALTER SYSTEM SET CURSOR_SHARING=FORCE"
- **ORA_119 (hard_parse) 规则需要被 latch:shared pool 等待事件触发**:
  - 当前 ORA_119 只通过 signal=metric(hard_parse_rate) 触发
  - 需要增加 signal: wait_event("latch: shared pool") 也能触发 ORA_119

---

## T011 — Sequence 争用 (CACHE=20 + 40并发INSERT)

### Rule 真实输出
```
根因: 序列 CACHE 过小或 NOCACHE 导致 SQ 争用
严重程度: 高    置信度: 78%
证据链:
1. enq: SQ - contention 在单实例下通常由 CACHE 值过小引起
2. 每次 CACHE 用完需更新 seq$，引发 row cache lock 和 SQ enqueue
3. 高并发 INSERT 使用 NOCACHE 或小 CACHE 序列时尤为严重
建议:
[紧急] 查看争用序列并增大 CACHE
[修复] 查看 ASH 确认争用序列对象
[预防] 考虑 UUID 或应用层生成 ID
```

### 现场数据
- 42 sessions 活跃, 97.6% 等待 "enq: SQ - contention"
- SID=1336 阻塞 39 个

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 38/40 | 准确识别 SQ 争用 + CACHE 过小根因 |
| 修复建议(30) | 25/30 | 给出查序列SQL+增大CACHE+替代方案，但未直接给 ALTER SEQUENCE 的 SQL |
| 严重程度(10) | 8/10 | "高"基本准确（97.6%阻塞可以更高到 CRITICAL） |
| 排查路径(10) | 9/10 | 完整：ASH确认→序列定位→修复，步骤合理 |
| 完整性(10) | 8/10 | 关联了 row cache lock 和 RAC 场景，较完整 |
| **总分** | **88/100** | **Rule 在 SQ 争用场景表现优秀** |

### Rule 优化方案
- 小幅改进：直接给出 `ALTER SEQUENCE xxx CACHE 1000 NOORDER;` 的执行 SQL
- 严重程度可根据阻塞比例升级到 CRITICAL

---

## T030 — SELECT FOR UPDATE 争用 (blocker 锁住 100 行后 SLEEP)

### Rule 真实输出
```
根因: 默认 — 通用 TX 争用诊断
严重程度: 中    置信度: 58%
证据链: enq: TX 争用，需进一步区分锁模式
建议: /ash detail event='enq: TX - row lock contention'
```

### 现场数据
- 18 sessions 活跃, 93.8% 等待 "enq: TX - row lock contention"
- SID=14 blocker, PL/SQL lock timer (SLEEP), 15 victims

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 8/40 | 走默认分支，和 T001 相同问题 |
| 修复建议(30) | 5/30 | 只建议查 ASH |
| 严重程度(10) | 3/10 | "中"，应为 HIGH/CRITICAL |
| 排查路径(10) | 4/10 | 采集了阻塞链但未深入分析 blocker |
| 完整性(10) | 2/10 | 缺失：blocker event分析、SELECT FOR UPDATE识别、SKIP LOCKED建议 |
| **总分** | **22/100** | |

### Rule 优化方案
- 同 T001 的优化方案（blocker event 分析分支）
- 额外：如果 blocker SQL 是 SELECT...FOR UPDATE → 建议缩小锁范围 + SKIP LOCKED

---

## T008 — ITL 争用 (INITRANS=1 + 30并发UPDATE不同行)

### Rule 真实输出
```
根因: 默认 — 通用 TX 争用诊断
严重程度: 中    置信度: 58%
证据链: enq: TX 争用，需进一步区分锁模式
关联分析: ITL争用 — 受根因影响的下游症状
次要根因: 无 ITL 等待 (!!实际77.4%是ITL等待!!)
```

### 现场数据
- 32 sessions 活跃
- enq: TX - allocate ITL entry 77.4%（主要等待！）
- enq: TX - row lock contention 19.4%
- 多个阻塞链

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 12/40 | 在关联分析中提到了ITL（好），但判定为"下游症状"而非根因（错）。主根因走了默认TX争用分支。次要根因声称"无ITL等待"与现场数据矛盾（77.4%都是ITL） |
| 修复建议(30) | 5/30 | 只建议查ASH。正确建议应是 ALTER TABLE MOVE INITRANS 10 + REBUILD INDEX |
| 严重程度(10) | 3/10 | "中"，77%阻塞应为HIGH |
| 排查路径(10) | 5/10 | 采集了等待事件分布（好），但决策树分支判断错误 |
| 完整性(10) | 3/10 | 缺失：INITRANS值检查、table结构分析、具体修复SQL |
| **总分** | **28/100** | |

### Rule 优化方案
- **ORA_180 (ITL等待) 的触发条件需要修改**:
  - 当前：依赖 wait_profile 中有 "enq: TX - allocate ITL entry"
  - 问题：rule engine 可能在数据传递时丢失了 ITL 信息，导致 ORA_180 的 "无ITL等待" 判定
  - 需要排查：burst report 是否正确传递了 "allocate ITL entry" 等待事件
- **ORA_085/086 默认分支太容易被命中**:
  - 当 wait_profile 中 "allocate ITL entry" 占比 > 50% 时，应优先触发 ORA_180 而非默认TX争用
  - 修改 resolver 优先级：ITL 规则权重应高于默认 TX 争用
- **新增 diagnostic_query**: `SELECT table_name, ini_trans, max_trans FROM user_tables WHERE ini_trans < 4`

---

## T014 — Cursor Pin S (50并发读热点1行)

### Rule 真实输出
```
根因: cache hit < 50% — session cursor cache 不足 (RC3)
严重程度: 中    置信度: 50%
证据链:
1. session cursor cache 命中率低于50%
2. 软解析仍需在 Library Cache 中查找和 pin cursor，增加 mutex 竞争
建议: 增大 session_cached_cursors + 应用使用 PreparedStatement
```

### 现场数据
- 52 sessions 活跃, 98% 等待 "cursor: pin S"
- SQL_ID=f9pbvzjbcmu1n 15并发
- SID=1338 阻塞 48 个
- 实际还有 30 sessions 在 resmgr:cpu quantum（Resource Manager 限流）

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 20/40 | 识别到 cursor pin S + session cursor cache 不足（部分正确）。但未识别到真正根因：50并发读同一行导致的 mutex 争用。cursor cache 是优化方向之一但不是根因 |
| 修复建议(30) | 15/30 | session_cached_cursors 建议有效但不治本。正确应包括：减少并发热点访问、考虑缓存层、SQL结果缓存 |
| 严重程度(10) | 4/10 | "中"偏低，98%阻塞应为HIGH/CRITICAL |
| 排查路径(10) | 6/10 | 走了 cursor: pin S 专用规则（好），但分析深度不够 |
| 完整性(10) | 5/10 | 提到了 PreparedStatement 和关联规则（好），但缺 V$SQL child cursor 分析 |
| **总分** | **50/100** | |

### Rule 优化方案
- **cursor: pin S 规则需要增加热点分析分支**:
  - 当单个 SQL_ID 并发 > 10 且等 cursor: pin S → 不是 cache 问题，是热点 SQL 问题
  - 新增分支：查 V$SQL 该 SQL_ID 的 child_number → 如果只有 1 个 child 说明是共享 cursor 争用
  - 建议：应用层缓存查询结果 / Result Cache / 减少并发访问频率
- **Resource Manager 限流应被识别**:
  - 现场有 30 sessions 等 resmgr:cpu quantum，rule 完全没提到
  - 需要增加 Resource Manager 等待事件的识别规则

---

## T013 — Cursor Pin S (50并发相同SQL)

### Rule 真实输出
```
根因: cache hit < 50% — session cursor cache 不足 (RC3)
严重程度: ■■□□ 中    置信度: 50%
证据链:
1. session cursor cache 命中率低于 50%
2. 软解析仍需在 Library Cache 中查找和 pin cursor，增加 mutex 竞争
3. 关联规则 WE006 (cursor: pin S wait on X) 建议一并排查
建议: 增大 session_cached_cursors 到 200-500 + 使用 PreparedStatement
```

### 现场数据
- 52 sessions 活跃, 80.4% 等待 "cursor: pin S"
- SQL_ID=1tj1gudu9kxz6 23并发
- 17.6% 等待 resmgr:cpu quantum（Resource Manager 限流）
- 多个阻塞链，分散在不同 cursor

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 18/40 | 正确识别 cursor pin + cache miss。但根因是 50 并发同一 SQL 的 mutex 争用，不仅是 cache miss |
| 修复建议(30) | 15/30 | session_cached_cursors 和 PreparedStatement 有效但是辅助手段。缺少：Result Cache、应用层缓存、并发控制 |
| 严重程度(10) | 4/10 | "中"偏低，80.4% 阻塞应为 HIGH |
| 排查路径(10) | 6/10 | 检查了 cursor cache 命中率（好），但未检查 V$SQL 热点并发度 |
| 完整性(10) | 5/10 | 引用了关联规则和诊断 SQL（好）。缺失：热点 SQL 分析、Resource Manager 提及 |
| **总分** | **48/100** | |

### Rule 优化方案
- 同 T014 的优化方案（热点 SQL 分析分支）
- **severity 应根据 affected sessions 比例动态调整**: >50% → HIGH, >80% → CRITICAL
- **Resource Manager 等待事件需要被识别**: resmgr:cpu quantum 17.6% 完全未提及

---

## T012 — Row Cache Lock (30路并发DDL)

### Rule 真实输出
```
根因: RC3/RC4: shared_pool 或并发登录
严重程度: ■■□□ 中    置信度: 70%
证据链: 排除 RC1(sequence), shared_pool 过小导致字典缓存被换出
建议: 增大 shared_pool_size
```

### 现场数据
- 32 sessions 活跃: 74.2% enq:TX allocate ITL + 9.7% row cache lock + 6.5% library cache lock
- 30 路并发 CREATE/DROP TABLE 操作

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 15/40 | 识别到 row cache lock + 字典缓存（好），但误判为 shared_pool 不足，实际是并发 DDL 过多 |
| 修复建议(30) | 8/30 | "增大 shared_pool" 不对。应建议减少并发 DDL / 用 GTT / 串行化 DDL |
| 严重程度(10) | 4/10 | "中"，90%+ 阻塞应为 HIGH |
| 排查路径(10) | 6/10 | 排除了 sequence（好），识别 row cache + library cache（好） |
| 完整性(10) | 5/10 | 列出了关联规则，但漏了 ITL 74.2% 和 DDL 模式识别 |
| **总分** | **38/100** | |

---

## T035 — 递归SQL/CPU冲高 (40路 EXECUTE IMMEDIATE)

### Rule 真实输出
```
根因: 检查监控工具会话
严重程度: ■■□□ 中    置信度: 90%
证据链: V$ 视图查询效率正常
次因: 解析比例正常 — 检查特定 SQL 的 mutex 争用 (58%)
```

### 现场数据
- 42 sessions 活跃: 70.7% kksfbc child completion + 22% cursor:pin S wait on X + 2.4% latch:shared pool
- 40 路 EXECUTE IMMEDIATE 不同 literal SQL

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 5/40 | 主诊断完全错误（"监控工具会话"）。次诊断提到 cursor pin 但未识别硬解析根因 |
| 修复建议(30) | 3/30 | "持续关注监控查询性能" 无用。应建议：使用绑定变量、CURSOR_SHARING=FORCE |
| 严重程度(10) | 3/10 | "中"，92.7% 阻塞应为 CRITICAL |
| 排查路径(10) | 4/10 | 次诊断建议查 ASH（有用），但主诊断方向完全错误 |
| 完整性(10) | 2/10 | 缺失：kksfbc 识别、hard_parse 检查、CURSOR_SHARING 建议 |
| **总分** | **17/100** | |

### Rule 优化方案
- **kksfbc child completion 需要被识别**: 这是硬解析的关键等待事件，目前规则引擎完全不认识
- **V$ 视图误判**: 规则引擎把 EXECUTE IMMEDIATE 的递归 SQL 误认为监控查询

---

## T050 — 并发INSERT/Redo争用 (10路bulk INSERT)

### Rule 真实输出
```
根因: 并发 INSERT <= 50 — 调整存储参数
严重程度: ■■■□ 高    置信度: 58%
证据链: HW 争用明显，但并发量中等
建议: 增大 extent 大小 / 使用 UNIFORM ASSM 表空间
```

### 现场数据
- 32 sessions: 45.5% enq:HW + 36.4% enq:TX + 9.1% buffer busy waits

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 10/40 | 识别到 HW 争用（好），但根因分析不够深（undo segment extension） |
| 修复建议(30) | 6/30 | extent 调整部分正确，但缺少批量提交、NOLOGGING、表分区建议 |
| 严重程度(10) | 7/10 | 正确判定为"高" |
| 排查路径(10) | 5/10 | 识别了阻塞模式 |
| 完整性(10) | 4/10 | 缺失：undo 分析、批量提交建议 |
| **总分** | **32/100** | |

---

## T046 — 表空间满 (USERS 95%，空闲时)

### Rule 真实输出
```
(无规则匹配)
```

### 现场数据
- USERS 表空间 95% 满，SYSTEM 97.9%
- 但无活跃用户会话

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 0/40 | 未检测到任何问题 |
| 修复建议(30) | 0/30 | 无输出 |
| 严重程度(10) | 0/10 | 未检测 |
| 排查路径(10) | 0/10 | 无规则触发 |
| 完整性(10) | 0/10 | 无 |
| **总分** | **0/100** | |

### Rule 优化方案
- **/rule live 需要独立的容量检查路径**: 即使无活跃会话，也应检查表空间/UNDO/TEMP 使用率
- **建议**: 在实时采集阶段增加 tablespace_used_pct 检查，>95% 直接告警

---

## T015 — DDL Lock Timeout + DML 冲突

### Rule 真实输出
```
根因: 解析比例正常 — 检查特定 SQL 的 mutex 争用 (58%)
次因: 默认 — 通用 TX 争用诊断 (58%)
```

### 现场数据
- 18 sessions: 71.4% cursor:pin S wait on X + 14.3% enq:TX row lock contention
- 1 DML blocker + 15 DDL attempters + DML victims

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 10/40 | 识别到两个症状但未识别 DDL/DML 冲突根因 |
| 修复建议(30) | 5/30 | "查 ASH" 通用建议。应建议：串行化 DDL、维护窗口执行 DDL |
| 严重程度(10) | 3/10 | "中"，85.7% 阻塞应为 HIGH |
| 排查路径(10) | 5/10 | 识别了两个不同问题（好），但未关联到同一根因 |
| 完整性(10) | 3/10 | 缺失：DDL 识别、DDL_LOCK_TIMEOUT 检测、维护窗口建议 |
| **总分** | **26/100** | |

---

## T-LFS — Log File Switch (30路 commit 风暴)

### Rule 真实输出
```
根因: 并发 INSERT <= 50 — 调整存储参数
严重程度: ■■■□ 高    置信度: 58%
证据链: HW 争用明显
建议: 增大 extent / UNIFORM ASSM 表空间
```

### 现场数据
- 32 sessions: **54.8% log file switch completion** + 22.6% enq:HW + 19.4% buffer busy waits
- 30 路 INSERT + COMMIT 到无约束表

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 8/40 | 诊断了 HW 争用但完全忽略了 **log file switch completion (54.8%)** 这个最大等待事件 |
| 修复建议(30) | 5/30 | HW 建议不解决主要问题。应建议：增大 redo log、增加 log group、批量提交 |
| 严重程度(10) | 7/10 | 正确判定"高" |
| 排查路径(10) | 4/10 | 诊断了次要问题，忽略了主要问题 |
| 完整性(10) | 2/10 | 缺失：redo log 分析、log switch 频率、commit 频率分析 |
| **总分** | **26/100** | |

### Rule 优化方案
- **log file switch completion 需要独立规则**: 当前规则引擎完全不识别这个等待事件
- **新增 redo log 诊断**: 检查 log switch 频率 > 30/h → 建议增大 redo log
- **commit 频率分析**: 高 commit rate + log file sync/switch → 建议批量提交

---

## T-CBC — Latch: Cache Buffers Chains (50路读同一行)

### 现场数据
- 52 sessions: 98% cursor: pin S（预期是 CBC latch，实际产生了 cursor pin）

### 评分
- 同 T013: **48/100** — session cursor cache 诊断

---

## T-LIBPIN — Library Cache Pin (编译+执行PL/SQL包)

### Rule 真实输出
```
根因: cache hit < 50% — session cursor cache 不足 (RC3)
严重程度: ■■□□ 中    置信度: 50%
```

### 现场数据
- 31 sessions: 93.3% cursor: pin S + 3.3% library cache pin
- 3 recompilers + 30 executors on test_pkg

### 评分

| 维度 | 得分 | 说明 |
|------|------|------|
| 根因识别(40) | 12/40 | 识别到 cursor pin（好），但根因是 PL/SQL 包被频繁重编译导致 cursor 失效，非 cache 不足 |
| 修复建议(30) | 10/30 | session_cached_cursors 有些帮助，但应建议：停止频繁 recompile、避免运行时 DDL |
| 严重程度(10) | 4/10 | "中"，96.6% 阻塞应为 CRITICAL |
| 排查路径(10) | 6/10 | 走了 cursor pin 路径（对），引用了 library cache lock/pin 规则（好） |
| 完整性(10) | 8/10 | 引用了 WE006 和 WE010 关联规则 |
| **总分** | **40/100** | |
