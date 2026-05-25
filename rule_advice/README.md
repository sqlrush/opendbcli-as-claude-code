# Rule Engine 优化策略文档

基于 2026-03-25 redo_rate 触发场景的诊断偏差分析，梳理当前规则引擎的策略和待优化点。

## 文档索引

| 文件 | 内容 |
|------|------|
| [01_diagnosis_pipeline.md](01_diagnosis_pipeline.md) | 当前四阶段诊断管线架构 |
| [02_signal_coverage_gaps.md](02_signal_coverage_gaps.md) | 信号覆盖缺口分析（核心问题） |
| [03_redo_rate_correct_diagnosis.md](03_redo_rate_correct_diagnosis.md) | redo_rate 的正确诊断路径 + 新规则设计 |
| [04_resolver_trigger_relevance.md](04_resolver_trigger_relevance.md) | Resolver 触发指标相关性优化方案 |
| [05_existing_rules_audit.md](05_existing_rules_audit.md) | 现有 redo 相关规则审计 |
| [06_we2_007b_weakness.md](06_we2_007b_weakness.md) | WE2-007b 规则质量问题 |
