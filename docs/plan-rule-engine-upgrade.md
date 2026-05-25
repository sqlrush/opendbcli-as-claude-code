# Rule Engine JSON 升级方案（Oracle）

## 现状关键发现

| 发现 | 影响 |
|------|------|
| **QueryExecutor = nil** | decision_tree 里的 Query 全部不能执行，树形推理形同虚设 |
| **MaxTreeDepth = 5** | JSON 规则需要 8 层，当前截断 |
| **操作符只有 10 种** | JSON 规则用了 45+ 种 |
| **置信度是二值的** | 有 Finding = 高，没有 = 低，不支持累积 |
| **cross_reference 只存不跑** | CausedBy/CausesOf 只用于结果排序，不参与推理过程 |
| **Specificity 写死 0.5** | 所有规则同等权重，无法区分专科 vs 通用规则 |

## 两条主线，10 件事

### 主线一：Rule Engine 升级（核心）

**1. 让 decision_tree 能真正执行（当前完全断路）**
- 实现 QueryExecutor，接入数据库连接
- 每条 JSON 规则自带 5-8 个 diagnostic_queries，树遍历时按需执行
- 动态 QueryID 注册（`json:ORA_086:step1_check`）
- MaxTreeDepth 从 5 → 20

**2. JSON 规则加载**
- JSON 反序列化类型（处理多态 condition）
- JSONRuleProvider 实现 RuleProvider 接口
- `go:embed` 嵌入二进制，174 个 Oracle JSON
- CombinedProvider 与现有 266 条 Go 规则共存

**3. 声明式求值器（替代 Go lambda）**
- ExprOps：45+ 操作符注册表
- EvalTreeCheck：替代 TreeNode.Check lambda
- EvalBranchMatch：替代 Branch.Match lambda
- EvalSkipWhen：替代 SkipCondition.Check lambda

**4. 置信度累积模型（从二值升级到渐进式）**
- JSON 规则每步诊断累积置信度（20% → 35% → 55% → 75% → 92%）
- 解析 `"置信度 92%: 4个独立证据"` 格式
- Diagnosis.Confidence 从简单 finding 计数改为按步骤累积
- 多根因概率排名（RC1: 35%, RC2: 30%, RC3: 20%）

**5. 排除法推理**
- JSON 规则的 decision_tree 用排除法：列出 5 个候选，逐步排除
- 当前 walkTree 是"第一匹配即停止"，需要支持"全部评估，排除不符合的"
- 排除结果也是 Finding（"RC3 已排除：shared_pool_free > 10%"）

**6. cross_reference 执行**
- decision_tree 分支中的 `cross_reference: [{rule_id: "ORA_084"}]`
- 浅层：注入关联规则 Finding，增加置信度
- 深层（未来）：跳转执行关联规则的 tree，合并诊断结果

**7. Trigger 条件扩展**
- 新增 35+ 操作符（contains, count_gt, spike_gt, between 等）
- 支持多条件 AND（当前单条件）
- skip_when 从 Go 函数改为声明式表达式解析
- Source 映射：JSON 的 `v$sysstat` → 内部 `metrics`

### 主线二：为 Rule 服务的 Sentinel/基础设施升级

**8. QueryExecutor 实现（Rule 依赖，最高优先级）**
- 当前 `rule_skill.go` 传 nil 给 Engine
- 需要接入真实数据库连接，执行 diagnostic_queries 里的 SQL
- 结果缓存（同一诊断周期内同 SQL 只跑一次）
- 超时控制 + 错误处理（SQL 执行失败不中断诊断）

**9. BurstReport 数据增强**
- JSON 规则的 trigger 引用了 BurstReport 当前没有的数据：
  - 数据库版本信息（version_specific_notes 依赖）
  - 更多参数详情（cursor_sharing, session_cached_cursors 等）
  - ASH 采样数据（step2_identify_hot_object 类查询依赖）
  - 工作负载类型标记（OLTP/OLAP，thresholds 依赖）
- Sentinel burst 采集阶段需要多采这些

**10. Signal 映射层**
- JSON 用 135+ signal type → 内部 5 种 SignalType
- 关键映射：wait_event/error/metric 直接对应，其余归入 Keyword
- 48 个 sentinel 指标名 → JSON signal key 的双向映射表

## 实施顺序

```
第一步：让树能跑起来
  ├─ [8] QueryExecutor 实现     ← 不做这个，后面全部白搭
  ├─ [1] MaxTreeDepth 调到 20
  └─ [1] decision_tree 能执行 diagnostic_queries

第二步：能加载 JSON 规则
  ├─ [2] JSON 反序列化 + Provider
  ├─ [3] 声明式求值器（45+ 操作符）
  ├─ [7] Trigger 条件扩展
  └─ [10] Signal 映射层

第三步：推理质量提升
  ├─ [4] 置信度累积模型
  ├─ [5] 排除法推理
  └─ [6] cross_reference 执行

第四步：数据完备性
  └─ [9] BurstReport 数据增强
```

## 新建文件清单

| 文件 | 作用 | 预估行数 |
|------|------|---------|
| `json_types.go` | JSON 反序列化结构体 | ~200 |
| `json_provider.go` | JSONRuleProvider | ~150 |
| `json_parser.go` | JSON→Rule 转换 | ~300 |
| `json_tree.go` | JSONTreeNode→TreeNode 递归转换 | ~250 |
| `json_signal_map.go` | Signal 类型映射 | ~100 |
| `json_query_registry.go` | 动态 QueryID + DynamicQueryExecutor | ~120 |
| `expr_eval.go` | 声明式表达式求值器 | ~350 |
| `expr_ops.go` | 45+ 操作符实现 | ~300 |
| `cross_ref.go` | cross_reference 执行器 | ~150 |
| `combined_provider.go` | 合并 Provider | ~80 |
| `rules_json/embed.go` | go:embed JSON 文件 | ~20 |

## 修改文件清单

| 文件 | 改动 |
|------|------|
| `types.go` | 声明式 CheckExpr/MatchExpr 字段 |
| `engine.go` | walkTree 支持声明式路径 + 排除法 |
| `trigger.go` | 新增操作符 + 声明式 SkipWhen |
| `resolver.go` | 置信度累积 + 概率排名 |
| `rule_skill.go` | 接入 QueryExecutor |

## 数据来源

- ailinkdb 生产 JSON 规则数据（427 条，174 条 Oracle）
- opendb 消费数据，保证执行正确性
- JSON schema 有变更由 ailinkdb 负责调整
