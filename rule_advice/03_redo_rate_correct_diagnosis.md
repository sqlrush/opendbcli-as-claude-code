# redo_rate 触发的正确诊断路径

## 因果链

```
大量 DML (INSERT/UPDATE/DELETE)
  ├── → 产生大量 Redo（redo_rate 冲高）    ← 触发指标
  ├── → 频繁取序列号 → NOCACHE/小 CACHE → enq: SQ 争用  ← 下游瓶颈
  ├── → 大量 buffer 修改 → buffer busy waits              ← 下游瓶颈
  └── → cursor 高并发共享 → cursor: pin S                  ← 下游瓶颈
```

## 正确诊断结构

### 根因
**大量 DML 操作导致 Redo 生成量异常** — 需定位具体 SQL 和表

### 关联分析（下游瓶颈，非根因）
- enq: SQ - contention: 高并发 INSERT 使序列成为瓶颈
- buffer busy waits: 大量写入导致 buffer 争用
- cursor: pin S: 热点 SQL 游标共享争用

### 处置建议
1. [紧急] 定位 Top Redo 生成 SQL
   ```sql
   SELECT sql_id, executions, rows_processed,
          ROUND(rows_processed/GREATEST(executions,1)) rows_per_exec,
          substr(sql_text,1,80) sql_text
   FROM v$sql
   WHERE command_type IN (2,6,7)  -- INSERT/UPDATE/DELETE
   ORDER BY rows_processed DESC
   FETCH FIRST 10 ROWS ONLY
   ```

2. [紧急] 查看 ASH 中写入热点
   ```sql
   SELECT h.sql_id, s.sql_text, COUNT(*) samples,
          SUM(CASE WHEN h.in_parse='Y' THEN 1 ELSE 0 END) parse_samples
   FROM v$active_session_history h
   JOIN v$sql s ON h.sql_id = s.sql_id AND h.sql_child_number = s.child_number
   WHERE h.sample_time > SYSDATE - 1/24
     AND h.sql_opname IN ('INSERT','UPDATE','DELETE')
   GROUP BY h.sql_id, s.sql_text
   ORDER BY samples DESC
   FETCH FIRST 10 ROWS ONLY
   ```

3. [修复] 优化高频 DML
   - 批量 INSERT → 减少 commit 频率，使用 APPEND hint
   - 热点 UPDATE → 考虑分区、减少更新范围
   - 评估是否为正常业务高峰 vs 异常批处理

4. [关联] 缓解 SQ 争用
   - 增大序列 CACHE 到 1000+
   - 考虑 UUID 替代序列

## 新规则设计

### 规则 ID: WE2-REDO-GEN (建议)

```
signals:
  - {type: "metric", key: "redo_rate"}
  - {type: "category", key: "redo"}

trigger:
  conditions:
    - source: "metrics", field: "redo_rate", op: "anomalous"  # spike 或 rising
  skip_when:
    - "log file switch > 10%" (此时转 ORA_162/163 处理)

decision_tree:
  Step 1: 查 Top DML SQL (rows_processed 排序)
    ├── 找到高频 DML → severity: high, 给出具体 SQL + 表 + 优化建议
    └── 未找到 → 检查 Direct Path Write (bulk load 场景)
        ├── 有 → severity: medium, 提示批量导入优化
        └── 无 → severity: low, 可能是正常业务波动

causes_of:
  - WE2-007b (enq: SQ)     # 高 DML 量导致序列争用
  - WE2-BBW  (buffer busy)  # 高 DML 量导致 buffer 争用

caused_by: []  # redo 生成量高是 DML 的直接后果，本身是最上游
```

### 对 Resolver 的影响

有了这条规则后：
1. WE2-REDO-GEN 和 WE2-007b 都会产出诊断
2. WE2-REDO-GEN.causes_of 包含 WE2-007b → Resolver 识别 WE2-007b 为下游
3. WE2-REDO-GEN 成为 Primary，WE2-007b 被吸收为关联分析
