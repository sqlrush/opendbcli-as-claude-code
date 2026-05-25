# AilinkDB JSON 规则规格要求

> OpenDB 规则引擎是**确定性引擎**，无 LLM 参与。所有 decision_tree 的 branch condition 必须可机械评估。

## 一、当前问题

### 1.1 语义条件不可评估

当前 JSON 规则的 decision_tree branch condition 使用语义标记：

```json
{"field": "clear_anomaly", "op": "eq", "value": true}
{"field": "symptom_match_rc1", "op": "eq", "value": true}
{"field": "primary_confirmed", "op": "eq", "value": true}
{"field": "rc2_excluded", "op": "eq", "value": true}
```

这些字段不存在于任何 diagnostic_query 的返回结果中，是给 LLM 做中间推理用的抽象概念。确定性引擎无法评估，只能走 default 分支，8 步链式推理退化为 1 步。

### 1.2 findings 混入非证据内容

部分 PG 规则的 findings 数组中出现了 severity 值：

```json
"findings": ["medium"]
```

findings 应该只包含人类可读的证据描述。

### 1.3 related_rules 引用不存在的规则

- `ORA_005` 和 `ORA_010` 的 related_rules 引用了 `EMG_008`，但 EMG 系列不在 Oracle 规则集中

### 1.4 MySQL 关联规则重复

- "连接状态变量监控诊断" 在 related_rules 中出现两次

### 1.5 PG 规则偏运维/规划类

48 条 PG 规则中大量是"监控工具选型"、"高可用方案对比"、"版本升级迁移"等规划类规则，正常状态也会匹配。缺少性能诊断类规则（锁等待、慢查询、vacuum 滞后等异常场景）。

---

## 二、decision_tree branch condition 规格

### 2.1 核心原则

**condition.field 必须是 diagnostic_query 返回结果中的真实列名，或者是 "default"**。

不允许使用任何抽象语义字段。确定性引擎的评估逻辑：

```
1. 执行 diagnostic_query，得到结果集（列名 → 值）
2. 从结果集中取 condition.field 对应的列值
3. 用 condition.op 和 condition.value 做比较
4. 返回 true/false
```

### 2.2 正确示例

diagnostic_query 定义：
```json
"step4_blocker_info": {
  "desc": "查看 blocker 详情",
  "sql": "SELECT s.sid, s.last_call_et AS idle_sec, t.used_ublk, t.used_urec FROM v$session s LEFT JOIN v$transaction t ON s.saddr = t.ses_addr WHERE s.sid IN (SELECT DISTINCT blocking_session FROM v$session WHERE event = 'enq: TX - row lock contention')"
}
```

branch condition 引用 query 返回的列：
```json
{
  "label": "长事务持有行锁（idle > 300秒）",
  "condition": {"field": "idle_sec", "op": ">", "value": 300},
  "root_cause": "RC1",
  "severity": "high",
  "confidence": "置信度 85%: idle_sec > 300 且 used_ublk > 0"
}
```

### 2.3 错误示例（不允许）

```json
{
  "label": "确认为长事务持有行锁",
  "condition": {"field": "primary_confirmed", "op": "eq", "value": true}
}
```

`primary_confirmed` 不是任何 SQL 返回的列名，确定性引擎无法评估。

### 2.4 允许的 condition 类型

| 类型 | field 来源 | 示例 |
|------|-----------|------|
| Query 列值 | diagnostic_query SQL 的 SELECT 列 | `{"field": "idle_sec", "op": ">", "value": 300}` |
| Query 行数 | 固定字段名 `count` | `{"field": "count", "op": ">", "value": 0}` |
| Default | 字符串 `"default"` | `"condition": "default"` |
| 无条件 | 省略 condition 字段 | 终端节点（叶子），直接输出 findings/actions |

### 2.5 支持的操作符

```
数值: gt (>), lt (<), gte (>=), lte (<=), eq (=), ne (!=), between
百分比: pct_gt, pct_lt
字符串: contains, not_contains, starts_with, ends_with, matches, like
存在性: exists, not_empty, is_null
计数: count_gt, count_lt, count_eq
布尔: is_true, is_false
集合: in, not_in
```

符号形式 `>`, `<`, `>=`, `<=`, `=`, `!=` 也支持。

---

## 三、decision_tree 结构规格

### 3.1 每个 step 必须绑定一个 diagnostic_query

```json
{
  "step": "Step 1: 检查行锁等待详情",
  "query": "step1_check_row_lock",
  "branches": [...]
}
```

`query` 字段引用 `diagnostic_queries` 中的 key。引擎执行该 SQL，结果传给 branches 的 condition 评估。

### 3.2 分支评估逻辑

```
单行结果 → 返回 map[列名]值，condition.field 直接取列值
多行结果 → 返回 {"rows": [...], "count": N}，condition.field 取 "count" 或首行列值
无结果 → 返回 nil，所有 condition 评估为 false，走 default
```

### 3.3 排除法节点

```json
{
  "step": "Step 5: 排除法",
  "elimination_method": true,
  "query": "step5_verification",
  "branches": [
    {"label": "排除 RC2", "condition": {"field": "hot_row_count", "op": "<=", "value": 1}},
    {"label": "排除 RC3", "condition": {"field": "sfu_count", "op": "=", "value": 0}},
    {"label": "所有其他原因已排除", "condition": "default", "root_cause": "RC4"}
  ]
}
```

当 `elimination_method: true` 时，引擎评估所有分支：
- 不匹配的 → 报告为"已排除"
- 最后匹配的 → 作为确认的根因

### 3.4 cross_reference

```json
"cross_reference": [
  {"rule_id": "ORA_084", "reason": "检查是否存在死锁"}
]
```

引擎将关联规则注入为 Finding（"关联规则 ORA_084 建议一并排查"）。

---

## 四、diagnostic_queries 规格

### 4.1 SQL 返回列命名

SQL 的 SELECT 列名必须与 branch condition 的 field 名一致：

```json
"step1_check": {
  "sql": "SELECT COUNT(*) AS lock_count, MAX(seconds_in_wait) AS max_wait_sec FROM v$session WHERE event = 'enq: TX - row lock contention'"
}
```

对应 branch：
```json
{"field": "lock_count", "op": ">", "value": 0}
{"field": "max_wait_sec", "op": ">", "value": 60}
```

### 4.2 使用别名确保列名可预测

```sql
-- 好：列名明确
SELECT COUNT(*) AS cnt, MAX(idle_sec) AS max_idle FROM ...

-- 差：列名不可预测
SELECT COUNT(*), MAX(last_call_et) FROM ...
```

### 4.3 参数化

用 `{param_name}` 占位符：
```sql
SELECT * FROM v$sql WHERE sql_id = '{sql_id}'
```

引擎在执行时替换参数。

---

## 五、trigger 条件规格

### 5.1 source 字段映射

引擎将 JSON 的 source 映射到内部数据源：

| JSON source | 引擎数据源 | 说明 |
|-------------|-----------|------|
| `wait_profile` | BurstReport.WaitProfile | 等待事件分布 |
| `metrics` | BurstReport.Metrics | sentinel 指标 |
| `blocking_chains` | BurstReport.BlockingChains | 阻塞链 |
| `top_sqls` | BurstReport.TopSQLs | Top SQL |
| `summary` | 标量汇总 | peak_active, duration_sec 等 |
| `v$sysstat`, `v$session` 等 | 映射到 `metrics` | Oracle 视图名自动映射 |
| `performance_schema`, `global_status` 等 | 映射到 `metrics` | MySQL 源自动映射 |
| `pg_stat_activity`, `pg_stat_database` 等 | 映射到 `metrics` | PG 源自动映射 |

### 5.2 field 字段必须匹配 sentinel 指标名

Oracle sentinel 48 个指标名：
```
active_sessions, cpu_sessions, io_sessions, lock_sessions,
redo_rate, hard_parse, physical_read_rate, logical_read_rate,
log_file_sync_avg, db_file_seq_read_avg, enqueue_wait_avg,
buffer_cache_hit_pct, library_cache_hit_pct,
tablespace_used_pct, temp_used_pct, undo_used_pct,
blocking_chains, deadlock_count, ...
```

MySQL sentinel 指标名：
```
threads_running, lock_waits, innodb_row_lock_waits,
buffer_pool_hit_pct, connections_pct, ...
```

PG sentinel 指标名：
```
active_sessions, idle_in_transaction, lock_waits, long_queries,
cache_hit_pct, dead_tuple_ratio, xid_age_pct,
connections_pct, blocker_count, deadlocks, ...
```

trigger.conditions.field 应使用上述指标名，否则条件不会被评估。

### 5.3 skip_when 表达式格式

```json
"skip_when": [
  {"desc": "硬解析率低", "condition": "hard_parse < 5 AND no_lc_contention"}
]
```

支持的格式：`field op value [AND field op value]*`。`no_` 前缀表示"该指标不存在或为零"。

---

## 六、signals 规格

### 6.1 type 映射

| JSON type | 引擎 SignalType | 索引方式 |
|-----------|----------------|---------|
| `wait_event` | SignalWaitEvent | 按 key 精确匹配 |
| `error`, `alert` | SignalErrorCode | 按 key 精确匹配 |
| `metric` | SignalMetric | 按 key 精确匹配 |
| 其他 | SignalKeyword | 按 key 子串匹配 |

### 6.2 key 必须可匹配

- `wait_event` 的 key 必须和数据库真实等待事件名一致（如 `enq: TX - row lock contention`）
- `metric` 的 key 必须和 sentinel 指标名一致（如 `active_sessions`）
- `error` 的 key 以 `ORA-` 开头自动识别为 ErrorCode

---

## 七、findings / actions 规格

### 7.1 findings 只包含证据描述

```json
"findings": [
  "UPDATE WHERE 条件无索引，Oracle 需要全表扫描并对每一行尝试加锁",
  "blocking_session 显示 SID=123 持有锁超过 300 秒"
]
```

不允许放 severity 值、RC ID 或其他元数据。

### 7.2 actions 结构

```json
"actions": [
  {
    "type": "fix",
    "desc": "增大序列 CACHE",
    "sql": "ALTER SEQUENCE {owner}.{seq_name} CACHE 1000",
    "risk": "序列号间隙增大",
    "rollback": "ALTER SEQUENCE {owner}.{seq_name} CACHE 原值"
  }
]
```

type 支持：`urgent`（紧急）、`fix`（修复）、`investigate`（排查）、`preventive`/`prevent`（预防）、`monitor`（监控）。

### 7.3 confidence 格式

```json
"confidence": "置信度 85%: 3个独立证据"
```

引擎用正则 `(\d+)%` 提取百分比值。

---

## 八、数据质量检查清单

生成规则后，请验证：

- [ ] 所有 branch condition.field 在对应 diagnostic_query 的 SELECT 列中存在
- [ ] 没有 `clear_anomaly`, `symptom_match_*`, `primary_confirmed`, `*_excluded` 等语义字段
- [ ] findings 数组只包含中文/英文证据描述，不含 severity 值
- [ ] related_rules 引用的 rule_id 都存在于同一数据库的规则集中
- [ ] related_rules 无重复引用
- [ ] trigger.conditions.field 使用 sentinel 指标名
- [ ] signals.key 和数据库真实等待事件名 / 指标名一致
- [ ] diagnostic_queries 的 SQL SELECT 列使用明确别名
- [ ] 每个 decision_tree step 都绑定了 diagnostic_query
- [ ] PG 规则集包含性能诊断类规则（锁、慢查询、vacuum 等），不只是运维规划类
