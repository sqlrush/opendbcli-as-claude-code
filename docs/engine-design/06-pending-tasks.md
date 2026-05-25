# 待办任务

## 待讨论详细方案

### /policy — 用户诊断策略管理（核心功能）

**概念：** DBA 通过 .md 文件定义自己的诊断策略，Engine 加载后注入系统提示，LLM 诊断时遵守。对标 Claude Code 的 CLAUDE.md。

**当前进度：**
- ✅ `context/rules.go` RulesLoader 已实现（加载 .md 文件，全局+实例级合并）
- ✅ `context/builder.go` 已在构建系统提示时调用 RulesLoader
- ⬜ CLI 命令 `/policy` 未实现
- ⬜ 目录名待从 `~/.opendb/rules/` 改为 `~/.opendb/policies/`
- ⬜ 详细交互方案待讨论

**命令设计（初步）：**
```
/policy              → 查看当前生效的所有策略（全局+当前实例）
/policy reload       → 重新加载策略文件（修改后无需重启）
/policy edit         → 用系统编辑器打开策略目录
/policy show global  → 只看全局策略
/policy show {实例名} → 只看某个实例的策略
```

**文件目录：**
```
~/.opendb/policies/*.md              全局策略（影响所有连接）
~/.opendb/policies/{实例名}/*.md      实例级策略（只影响特定实例）
```

**示例文件：**
```markdown
# ~/.opendb/policies/company-standards.md
- 生产库禁止建议 kill 操作，只能建议 DBA 手动执行
- 索引命名规则: idx_{表名}_{列名}
- 诊断结论必须包含风险评估（高/中/低）

# ~/.opendb/policies/orcl-prod/strict.md
- 核心生产库，禁止建议任何 DDL
- 参数修改需走变更审批流程
```

**待讨论：**
- 是否支持 `/policy init` 生成模板文件
- 是否支持 `/policy validate` 检查策略文件语法
- 策略文件大小限制（避免注入过多内容到系统提示）
- 是否支持策略优先级标注（MUST/SHOULD/MAY）

---

## 待实现（不需要讨论方案）

| 任务 | 优先级 | 说明 |
|------|--------|------|
| OpenGauss diagnose.go 替换 | 高 | 和 PG 几乎一样，10分钟 |
| Anthropic 原生 Adapter | 高 | 独立消息格式，cache_control，thinking blocks |
| 测试服务器端到端验证 | 高 | 真实 Oracle + Ollama/Opus 跑完整诊断 |
| feature/engine-v2 合入 main | 高 | 验证通过后 |
| Gemini 原生 Adapter | 中 | 独立协议，接入时再做 |
| vLLM Adapter | 中 | prefix caching + guided_json |
| MLX Adapter | 低 | Apple Silicon 原生，按需 |
| context/rules.go 目录名改为 policies | 中 | 配合 /policy 命令 |
