# WE2-007b 规则质量问题

## 规则位置
`internal/oracle/ruleengine/rules_wait_ext.go` ruleEnqSQContentionSingle()

## 问题 1: MatchDefault 无条件命中

```go
Tree: &TreeNode{
    Step:  "检查序列 CACHE 配置",
    Query: QueryCursorStats,  // 复用了无关的 query
    Branches: []Branch{
        {
            Label:    "序列 CACHE 过小或 NOCACHE 导致 SQ 争用",
            Match:    MatchDefault(),  // ← 永远命中，不做任何验证
            Severity: SeverityHigh,
            Findings: []Finding{...},
        },
    },
},
```

**问题**: 不管序列 CACHE 实际是多少，只要 SQ contention > 2% 就直接输出
"CACHE 过小"。如果所有序列 CACHE 都是 10000，这个结论就是错的。

**修复**: 应该查 dba_sequences，根据实际 CACHE 值走不同分支：
- NOCACHE (cache_size=0) → "NOCACHE 导致 SQ 争用"
- CACHE < 100 → "CACHE 过小"
- CACHE >= 100 → "CACHE 配置合理，SQ 争用可能来自其他原因（极高并发/热点序列）"

## 问题 2: 复用了无关的 Query

```go
Query: QueryCursorStats,  // cursor stats query，和序列无关
```

应该用专门的序列查询：
```sql
SELECT s.sequence_owner, s.sequence_name, s.cache_size, s.order_flag
FROM dba_sequences s
WHERE s.sequence_owner NOT IN ('SYS','SYSTEM','AUDSYS','DBSNMP')
  AND s.cache_size < 100
ORDER BY s.cache_size ASC
FETCH FIRST 20 ROWS ONLY
```

## 问题 3: 缺少 ASH 关联验证

应该增加步骤：查 ASH 中 enq: SQ 等待的 sql_id，关联到具体序列对象，
而不是泛泛地说"序列 CACHE 过小"。

```sql
SELECT h.current_obj#, o.object_name, h.sql_id, COUNT(*) waits
FROM v$active_session_history h
LEFT JOIN dba_objects o ON h.current_obj# = o.object_id
WHERE h.event = 'enq: SQ - contention'
  AND h.sample_time > SYSDATE - 1/24
GROUP BY h.current_obj#, o.object_name, h.sql_id
ORDER BY waits DESC
FETCH FIRST 10 ROWS ONLY
```
