# 11 — 用户自定义规则设计（对标 CLAUDE.md）

## 灵感来源

Claude Code 的 CLAUDE.md 体系让用户能在不改代码的情况下，通过文本文件控制模型行为。
OpenDB 借鉴这个设计，让 DBA 能定义自己的诊断规范。

## Claude Code 的 CLAUDE.md 体系

### 存放位置和 4 级优先级

```
级别 1 — Managed（组织级，最低优先级）
  由管理员通过 Anthropic 后台设置
  → 用户看不到文件，通过 API 下发

级别 2 — User（用户级）
  ~/.claude/CLAUDE.md
  → 用户全局规则，影响所有项目

级别 3 — Project（项目级）
  {项目根目录}/CLAUDE.md
  → 提交到 git，团队共享

级别 4 — Local（本地私有，最高优先级）
  {项目根目录}/.claude/CLAUDE.md
  → 不提交 git，个人私有覆盖
```

### 加载和合并逻辑

```typescript
// claudemd.ts:getMemoryFiles() → context.ts:getUserContext()
// 在 20 步管线的第 15 步 prependUserContext() 注入

Managed 规则                  ← 最先加载
+ ~/.claude/CLAUDE.md         ← 叠加
+ 项目/CLAUDE.md              ← 叠加
+ 项目/.claude/CLAUDE.md      ← 最后叠加（最高优先）

注入时标注来源:
"Contents of ~/.claude/CLAUDE.md (user's private global instructions)"
"Contents of /project/CLAUDE.md (project instructions, shared by team)"

冲突时高优先级覆盖低优先级
前置声明: "IMPORTANT: These instructions OVERRIDE any default behavior"
```

---

## OpenDB 的用户自定义规则设计

### 3 级优先级（对标 CLAUDE.md 4 级，去掉 Managed）

```
级别 1 — 全局（用户级，最低优先级）
  ~/.opendb/rules/*.md
  → DBA 的个人诊断规范，影响所有数据库连接
  → 类似 Claude Code 的 ~/.claude/CLAUDE.md

级别 2 — 实例级
  ~/.opendb/rules/{实例名}/*.md
  → 针对特定数据库实例的规则
  → 类似 Claude Code 的 {项目}/CLAUDE.md

级别 3 — 会话级（最高优先级）
  通过 /diag 参数或环境变量传入
  → 临时规则，只影响当次诊断
  → 类似 Claude Code 的 {项目}/.claude/CLAUDE.md
```

### 文件格式

```markdown
# ~/.opendb/rules/company-standards.md

# 公司 Oracle 规范

## 诊断偏好
- 优先关注等待事件，其次看 SQL 性能
- 生产库禁止任何 kill 操作，只能建议 DBA 手动执行
- 分析完成后必须给出风险评估（高/中/低）

## 索引规范
- 索引命名: idx_{表名}_{列名}
- 单表索引不超过 6 个
- 组合索引列数不超过 3 个

## 参数规范
- SGA 不超过物理内存的 60%
- PGA 不超过物理内存的 20%
- processes 不超过 500
```

```markdown
# ~/.opendb/rules/orcl-prod/safety.md

# orcl-prod 实例专属规则

## 安全约束
- 这是核心生产库，诊断建议必须格外谨慎
- 禁止建议任何 DDL 操作（CREATE INDEX 也不行，需要走变更流程）
- 禁止建议修改 SGA/PGA 参数（需要重启，影响业务）
- kill session 只能在确认阻塞超过 5 分钟时建议

## 业务时间
- 工作日 9:00-18:00 为业务高峰，此时段内不建议任何变更操作
- 维护窗口: 每周日 02:00-06:00
```

### 加载逻辑

```go
// userRules.go

type UserRulesLoader struct {
    rulesDir string   // 默认 ~/.opendb/rules/
}

func (l *UserRulesLoader) Load(instanceName string) string {
    var rules []string

    // 级别 1: 全局规则
    globalRules := l.loadDir(l.rulesDir)
    if globalRules != "" {
        rules = append(rules, fmt.Sprintf(
            "# 用户全局规则 (来自 %s)\n%s", l.rulesDir, globalRules))
    }

    // 级别 2: 实例级规则
    if instanceName != "" {
        instanceDir := filepath.Join(l.rulesDir, instanceName)
        instanceRules := l.loadDir(instanceDir)
        if instanceRules != "" {
            rules = append(rules, fmt.Sprintf(
                "# 实例 %s 专属规则 (来自 %s)\n%s",
                instanceName, instanceDir, instanceRules))
        }
    }

    if len(rules) == 0 {
        return ""
    }

    return strings.Join(rules, "\n\n")
}

func (l *UserRulesLoader) loadDir(dir string) string {
    files, err := filepath.Glob(filepath.Join(dir, "*.md"))
    if err != nil || len(files) == 0 {
        return ""
    }

    var parts []string
    sort.Strings(files) // 按文件名字母序，保证加载顺序确定
    for _, f := range files {
        content, err := os.ReadFile(f)
        if err != nil {
            continue
        }
        parts = append(parts, string(content))
    }

    return strings.Join(parts, "\n\n")
}
```

### 在 ContextBuilder 中的注入位置

```go
func (b *Builder) buildSystemPrompt(input EngineInput) []SystemPromptBlock {
    blocks := []SystemPromptBlock{}

    // Block 1: 通用基座（可缓存）
    blocks = append(blocks, SystemPromptBlock{
        Text:         b.identityAndRules(),
        CacheControl: &CacheControl{Type: "ephemeral"},
    })

    // Block 2: DB 特定规则（可缓存）
    blocks = append(blocks, SystemPromptBlock{
        Text:         b.profile.SystemPromptRules(),
        CacheControl: &CacheControl{Type: "ephemeral"},
    })

    // Block 3: 用户自定义规则（不缓存 — 可能随时修改）  ← 新增!
    userRules := b.rulesLoader.Load(input.DatabaseInfo.Instance)
    if userRules != "" {
        blocks = append(blocks, SystemPromptBlock{
            Text: fmt.Sprintf(`# 用户自定义规则

IMPORTANT: 以下是用户定义的诊断规范，必须遵守。当与默认规则冲突时，以用户规则为准。

%s`, userRules),
            // 不标记 CacheControl — 用户可能随时改文件
        })
    }

    // Block 4: 环境信息（动态）
    blocks = append(blocks, SystemPromptBlock{
        Text: b.environmentInfo(input),
    })

    // Block 5: 模式修饰
    blocks = append(blocks, SystemPromptBlock{
        Text: b.modeModifier(input.Mode),
    })

    return blocks
}
```

### 完整的系统提示组装顺序

```
最终发给模型的系统提示:

┌─ Block 1: 通用基座 (~3000字, 可缓存) ─────────────────┐
│  身份 + 核心原则 + 推理策略 + 工具策略 + 输出格式 + 安全  │
└──────────────────────────────────────────────────────────┘
┌─ Block 2: DB 特定规则 (~1500字, 可缓存) ───────────────┐
│  Oracle 等待事件 + 视图 + 参数 + ORA 错误                │
│  (来自 PromptProfile.SystemPromptRules())                │
└──────────────────────────────────────────────────────────┘
┌─ Block 3: 用户自定义规则 (不定长, 不缓存) ─────────────┐
│  "IMPORTANT: 以下规则必须遵守，与默认冲突时以此为准"      │
│  公司规范 + 实例专属规则                                  │
│  (来自 ~/.opendb/rules/*.md)                             │
└──────────────────────────────────────────────────────────┘
┌─ Block 4: 环境信息 (动态, 不缓存) ─────────────────────┐
│  数据库类型/版本/实例名/时间/模式                         │
└──────────────────────────────────────────────────────────┘
┌─ Block 5: 模式修饰 (动态) ─────────────────────────────┐
│  playbook/assist/auto 的具体说明                         │
└──────────────────────────────────────────────────────────┘

对比 Claude Code:
  Block 1 ≈ prompts.ts (通用)
  Block 2 ≈ 无直接对应 (OpenDB 独有的 DB 知识)
  Block 3 ≈ CLAUDE.md (用户自定义)     ← 对标!
  Block 4 ≈ env_info (环境信息)
  Block 5 ≈ 无直接对应 (OpenDB 独有的模式系统)
```

---

## 对标关系总结

```
Claude Code CLAUDE.md 体系:                OpenDB 用户自定义规则:
─────────────────────────                  ──────────────────────

Managed (组织级, API下发)                   (不需要: OpenDB 无组织管理后台)

~/.claude/CLAUDE.md (用户全局)        ←→   ~/.opendb/rules/*.md (全局)
  "我喜欢简洁的代码"                         "优先关注等待事件"

{project}/CLAUDE.md (项目级)          ←→   ~/.opendb/rules/{实例名}/*.md (实例级)
  "用 Go 写, 不要 tab"                      "orcl-prod 禁止 kill"

{project}/.claude/CLAUDE.md (私有)    ←→   /diag 会话参数 (会话级)
  "我的私有覆盖"                             "这次只看空间问题"

加载位置: 第15步 prependUserContext   ←→   ContextBuilder Block 3
注入方式: <system-reminder> isMeta    ←→   SystemPromptBlock (不缓存)
优先级声明: "OVERRIDE any default"    ←→   "与默认冲突时以用户规则为准"
```

---

## 使用场景示例

### 场景 1: 金融行业 DBA

```markdown
# ~/.opendb/rules/finance-standards.md
- 所有诊断建议必须包含风险评估（高/中/低）
- 禁止建议直接执行 DDL，必须走变更审批流程
- 诊断结论必须引用具体的等待事件和 SQL ID，不接受模糊判断
- 优先排查锁争用和死锁问题（金融交易场景高发）
```

### 场景 2: 互联网公司 DBA

```markdown
# ~/.opendb/rules/web-standards.md
- 优先关注慢查询和连接数
- 建议索引时说明对写入性能的影响
- 高峰期（9:00-22:00）不建议收集统计信息
```

### 场景 3: 核心生产库专属

```markdown
# ~/.opendb/rules/core-prod/strict.md
- 这是核心生产库，任何操作类建议都需要标注"需审批"
- 禁止建议 ALTER SYSTEM 修改 SGA/PGA（需要重启）
- kill session 仅在确认阻塞链超过 3 层时建议
- 诊断报告末尾必须附上"免责声明：以上建议需 DBA 确认后执行"
```

---

## 管理命令

```
/rules                  — 查看当前生效的所有规则（全局+实例级）
/rules reload           — 重新加载规则文件（修改后无需重启）
/rules edit             — 用系统编辑器打开规则目录
/rules show global      — 只看全局规则
/rules show {实例名}    — 只看某个实例的规则
```
