# Rule Engine 优化路线图

> 基于 Round 2（20 场景重测）+ Round 3（39 个新场景）的验证结果

## 一、当前状态总结

### 已验证：59 个场景

| 轮次 | 场景数 | 有诊断 | 无诊断 | 正确诊断 | 均分 |
|------|--------|--------|--------|----------|------|
| R2（重测） | 16 可评 | 16 | 0 | ~6 | 52.4 |
| R3（新增） | 39 | 17 | 22 | ~3 | ~15 |
| **合计** | **55** | **33** | **22** | **~9** | - |

### Rule Engine 当前能力覆盖

| 领域 | 覆盖度 | 说明 |
|------|--------|------|
| 锁/阻塞 | ★★★★☆ | blocker 分析、多层链已实现，缺死锁检测 |
| 容量（表空间） | ★★★★☆ | 空闲时可检测，缺 TEMP/UNDO 专项 |
| 等待事件（并发类） | ★★★☆☆ | cursor pin S、CBC latch 可识别，部分走兜底 |
| 等待事件（IO类） | ★★☆☆☆ | 有规则但测试环境 SSD 无法验证 |
| 共享池/解析 | ★★☆☆☆ | 检测到 latch 但不关联硬解析根因 |
| SQL 性能 | ★☆☆☆☆ | 几乎空白，无执行计划分析能力 |
| 参数配置 | ☆☆☆☆☆ | 完全空白 |
| 会话管理 | ☆☆☆☆☆ | 完全空白 |
| Redo/日志 | ★★☆☆☆ | 有 WE_021/022 但覆盖不全 |

---

## 二、问题分类

### 问题 A：兜底规则过宽（"万金油"诊断）

**影响**：11 个场景被归到 2-3 个泛化根因

| 万金油根因 | 命中场景 | 实际应该是 |
|-----------|----------|-----------|
| "invalidation 导致重编译" | T016,T017,T018,T019,T026,T027,T029,T031,T036 | 全表扫描/数据倾斜/分区裁剪/HINT错误/buffer冲刷... |
| "无DDL — 并发解析争用" | T021,T042,T099 | 硬解析风暴/SP碎片化/latch:shared pool |
| "subpool 分布正常" | T020,T035,T038 | 硬解析+SP不足/递归SQL/ORA-04031 |

**根因**：当 `/rule live` 采集到活跃会话但等待事件不在已有规则匹配范围内时，走了 `library cache: mutex X` 或 `latch: shared pool` 的宽泛分支。这些分支的决策树缺乏深度验证（如检查 hard_parse_count、v$sqlarea literal SQL 占比、v$rowcache miss 率等），导致大量不同场景落入相同兜底诊断。

**修复策略**：
1. 收窄兜底分支的匹配条件（增加排除条件）
2. 在兜底前增加"二次采集"验证步骤
3. 新增具体场景的专用规则

### 问题 B：22 个场景完全无诊断

**原因分析**：

| 原因 | 场景数 | 场景 |
|------|--------|------|
| SSD 秒完成，无活跃会话 | 8 | T025,T032,T033,T034,T039,T047,T051,T022 |
| 有活跃会话但规则引擎不覆盖 | 8 | T076,T077,T080,T081,T086,T087,T089,T090 |
| 等待事件为 Idle/Network 类 | 4 | T043,T053,T092,T095 |
| 只触发容量检测（非目标诊断） | 2 | T088 触发容量但非 LOG_BUFFER 诊断 |

### 问题 C：已有规则的诊断深度不够

| 场景 | 现状 | 需要 |
|------|------|------|
| T005 死锁 | "blocker 空闲" | 识别循环阻塞链 + enqueue_deadlocks 统计 |
| T009 Sequence NOCACHE | "并发登录" | 识别 dc_sequences + 建议 CACHE |
| T012 Row Cache DDL | "AWR 指标正常" | 识别并发 DDL + 建议低峰执行 |
| T020 硬解析 | "subpool 正常" | 关联 hard_parse_count + literal SQL |
| T015 DDL+DML | "blocker 空闲" | 识别 DDL 类型锁模式 |

---

## 三、优化路线图

### Phase 1 — 兜底收窄 + 高频误诊修复（影响 14 个场景）

> 目标：消除"万金油"诊断，让错误诊断变成"无诊断"而非"错误诊断"

#### 1.1 收窄 "invalidation 导致重编译" 分支
- **当前逻辑**：library cache: mutex X 出现 → invalidation 检查 → 正常 → "纯并发/invalidation"
- **改为**：增加前置条件 `invalidations > 100 AND loads > 50`，否则不走此分支
- **影响场景**：T016-T019, T026-T029, T031, T036（9 个）

#### 1.2 收窄 "并发解析争用" 分支
- **当前逻辑**：latch: shared pool 出现 → DDL 检查 → 无 DDL → "并发解析"
- **改为**：增加 `hard_parse_rate` 检查，高硬解析 → "硬解析风暴"分支
- **影响场景**：T021, T042, T099（3 个）

#### 1.3 修复 "subpool 正常" 死胡同
- **当前逻辑**：latch: shared pool → subpool 检查 → 正常 → 结束
- **改为**：subpool 正常后继续检查 `hard_parse_count`、`shared_pool_free_pct`
- **影响场景**：T020, T035, T038（3 个）

#### 1.4 修复 T012 回退
- **原因**：row cache lock 匹配路径优化时引入了更泛的兜底
- **修复**：恢复 dc_objects → DDL 推断分支

### Phase 2 — 关键场景专用规则（影响 8 个场景）

#### 2.1 死锁检测规则
- **新规则**：检测 `enqueue_deadlocks > 0` + 循环阻塞链（A blocks B AND B blocks A）
- **产出**：ORA_084 增加 live 诊断能力
- **影响场景**：T005

#### 2.2 Row Cache Lock 细分
- **改进 ORA_194**：
  - `dc_sequences` cache 高争用 → "Sequence NOCACHE/CACHE 太小"
  - `dc_objects` cache 高争用 → "并发 DDL"
  - 其他 → 保持现有逻辑
- **影响场景**：T009, T010, T012

#### 2.3 硬解析 → Shared Pool 关联
- **改进 ORA_186/ORA_119**：
  - `latch: shared pool` + `hard_parse_rate > 100/s` → "硬解析风暴导致 SP latch 争用"
  - 增加建议：检查 literal SQL 占比、CURSOR_SHARING 设置
- **影响场景**：T020, T035, T038

#### 2.4 DDL/DML 锁冲突识别
- **改进 ORA_085/086**：
  - 检测 blocker 的 `command` 类型（DDL=1/2/3/9/15 etc.）
  - DDL blocker → "DDL 与 DML 冲突" 而非 "blocker 空闲"
- **影响场景**：T015

### Phase 3 — SQL 性能诊断能力建设（影响 12 个新场景）

> 这是最大的能力缺口，需要新增 `/rule live` 的采集维度

#### 3.1 Top SQL 执行计划采集
- **新增数据源**：在 `buildLiveReport` 中增加 `v$sql_plan` 采集（仅 Top 3 SQL）
- **新增信号**：
  - `full_scan_detected`: Top SQL 执行计划含 TABLE ACCESS FULL
  - `plan_changed`: SQL 的 `plan_hash_value` 与上次不同
  - `high_physical_reads`: 单 SQL physical reads > 10000

#### 3.2 全表扫描诊断规则
- **新规则**：`full_scan_detected` + 表有可用索引 → "缺索引或索引未使用"
- **决策树**：
  1. 检查 WHERE 列是否有索引
  2. 有索引但不用 → 检查统计信息/HINT/隐式转换
  3. 无索引 → 建议创建
- **影响场景**：T016, T017, T029, T081

#### 3.3 执行计划漂移检测
- **新规则**：`plan_changed` + 性能下降 → "执行计划漂移"
- **影响场景**：T018, T019, T034

#### 3.4 排序/TEMP 溢出诊断
- **新规则**：`direct path read/write temp` 等待事件 → 检查 PGA + SQL 排序需求
- **影响场景**：T022, T031, T039, T047

### Phase 4 — 参数/配置/会话检查（影响 14 个新场景）

#### 4.1 参数合理性检查
- **新规则组**：在空闲时（无明显等待事件）检查关键参数
  - `UNDO_RETENTION` < 300 → 警告
  - `OPEN_CURSORS` < 300 → 警告
  - `OPTIMIZER_INDEX_COST_ADJ` != 100 → 提示
  - `DB_FILE_MULTIBLOCK_READ_COUNT` > 256 → 提示
  - `CURSOR_SHARING` = FORCE → 提示
- **影响场景**：T086, T087, T089, T090

#### 4.2 会话/连接监控
- **新增检查**：
  - `sessions_used_pct > 80%` → "会话数接近上限"
  - `processes_used_pct > 80%` → "进程数接近上限"
  - 短时间新建连接 > 50 → "连接风暴"
- **影响场景**：T076, T077, T095

#### 4.3 Resource Manager 检测
- **新规则**：`resmgr:cpu quantum` 占比 > 30% → "Resource Manager CPU 限流"
- **影响场景**：T080

#### 4.4 TEMP/UNDO 容量专项
- **扩展现有容量检查**：
  - TEMP 使用率 > 85% → 定位 Top 消耗 SQL（v$tempseg_usage）
  - UNDO 使用率 > 90% → 定位长事务（v$transaction）
- **影响场景**：T047, T048, T051

#### 4.5 Alert Log 错误集成
- **新增**：检查最近 alert log 中的 ORA 错误数量
- **影响场景**：T092

### Phase 5 — Redo/IO 诊断完善（影响 4+ 个场景）

#### 5.1 Log File Sync 根因分析
- **改进 WE_021**：
  - `log file sync` + 高 commit rate → "提交频率过高"
  - `log file sync` + LGWR 写延迟高 → "存储 IO 问题"
- **影响场景**：T057

#### 5.2 Checkpoint Not Complete
- **新规则**：`log file switch (checkpoint incomplete)` → 检查 redo log 大小和组数
- **影响场景**：T058

#### 5.3 Redo 生成量异常（已有待办）
- **沿用 rule_advice/03_redo_rate_correct_diagnosis.md**
- **影响场景**：T088

---

## 四、实施优先级

| Phase | 影响场景 | 工作量 | 预期提升 |
|-------|----------|--------|----------|
| **Phase 1** | 14 个 | 小（改决策树分支条件） | 消除错误诊断，均分 +5-10 |
| **Phase 2** | 8 个 | 中（新规则 + 改现有规则） | 关键场景 +20-40 |
| **Phase 3** | 12 个 | 大（新增数据采集 + 规则） | 开辟新诊断领域 |
| **Phase 4** | 14 个 | 中（新增检查项） | 覆盖常见配置问题 |
| **Phase 5** | 4 个 | 小-中 | 完善 redo/IO 诊断 |

**建议执行顺序**：Phase 1 → Phase 2 → Phase 5 → Phase 4 → Phase 3

理由：
- Phase 1 投入最小、影响最大（修复错误比新增功能重要）
- Phase 2 覆盖真实高频场景（死锁、硬解析）
- Phase 5 工作量小但完善已有能力
- Phase 4 相对独立，可并行
- Phase 3 工作量最大但长期价值最高

---

## 五、验证计划

每个 Phase 完成后：
1. 重跑该 Phase 影响的场景
2. 确认目标场景分数提升
3. 确认其他场景不回退（回归测试）
4. 更新 progress.md 和 scores

**目标**：
- Phase 1+2 完成后：可评分场景均分 ≥ 65
- Phase 1-5 全部完成后：可评分场景均分 ≥ 80
