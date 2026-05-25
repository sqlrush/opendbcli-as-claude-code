# OG 经典复合故障场景 — multi-fault benchmark

测试 OG `/llm` 诊断质量的标准场景。同时触发 5+ 个相互关联的根因，验证 LLM 是否
能在多症状里定位主因 + 串清因果链 + 给出可执行修复 SQL。

实测过 deepseek-v4-pro / glm-5.1 / kimi-k2.6 / opus-4.6，是 v1.1.20+ 默认 benchmark。

## 触发的 6 类根因（一条因果链，不是 6 个独立问题）

| # | 现象 | 根因 | 修复 |
|---|------|------|------|
| 1 | **连接数冲高** (active+idle ~107，逼近 max_connections) | 长事务 + 后台慢查询会话累积 | 先 kill 长事务，慢查询会话自然回收 |
| 2 | **idle in transaction × 60** (`BEGIN; pg_sleep(1800);`) | 应用层连接没 close + 中途没 commit | `pg_terminate_backend` + 应用层加超时 |
| 3 | **autovacuum 被阻塞** (bench_mix_a dead_tup 累积) | #2 长事务持有 xmin，autovacuum 不能回收 | 修 #2，autovacuum 自然恢复 |
| 4 | **bench_og_hot 全表扫** (300 万行, `WHERE uid=?`, 8 active) | uid 列无索引，仅 PK(id) | `CREATE INDEX CONCURRENTLY ON bench_og_hot(uid)` |
| 5 | **bench_mix_b 全表扫** (200 万行, `WHERE uid BETWEEN`, 50 active) | uid 列无索引 | `CREATE INDEX CONCURRENTLY ON bench_mix_b(uid)` |
| 6 | **historical 死锁 ~134 次** (UPDATE bench_mix_a WHERE id BETWEEN range overlap) | 并发 UPDATE 范围重叠 + 行锁顺序不一致 | 应用层缩小范围 / 串行化 / 加二级索引降低锁粒度 |

## 期望 LLM 四层诊断输出

按 [feedback-diag-four-layer-strategy](../../../.claude/projects/-Users-yingjiewang-opendb/memory/feedback-diag-four-layer-strategy.md):

1. **告警主线**: 连接数冲高（最显眼现象，触发 Sentinel）
2. **关联问题**: 长事务 / dead_tup 累积 / 缺索引慢查询 / 历史死锁
3. **当前对比**: 与 PROFILE.md 基线对比，辨认是不是新问题
4. **综合评估**: 修复优先级 = `kill 长事务 → autovacuum 自然恢复 → CREATE INDEX × 2 → 应用层修死锁`

输出必须包含：
- 根因分析表格（4 列：指标 / 数据 / 来源工具 / 一句话因果）
- 因果链图（根因 → 现象 → 症状）
- 紧急措施 SQL（可立即执行，比如 `pg_terminate_backend`）
- 根因修复 SQL（带 ⚠️ 风险评估 / 📋 前置检查 / 🔄 回滚方案 三件套）
- 优先级表（🔴 高 / 🟡 中 / 🟢 低）

实测 deepseek-v4-pro 跑 5 轮 ~6 分钟出 5800 字完整报告（v1.1.21 修了 streamRound bug 之后）。

## 启动命令

```bash
# 起两个负载脚本（同 OG 实例）
bash scripts/og_load_mixed_a.sh setup       # 长事务 + bench_mix_a/b 慢查询
bash scripts/og_load_oracle_mirror.sh setup # bench_og_hot 全扫 + 历史死锁

# 验证症状全触发
bash scripts/og_load_mixed_a.sh verify
bash scripts/og_load_oracle_mirror.sh verify

# 跑 LLM 诊断
dbaa -c gaussdb /llm "数据库现在有什么问题"
# 或
opendb -c og /llm "数据库现在有什么问题"
```

## 清理

```bash
bash scripts/og_load_mixed_a.sh cleanup
bash scripts/og_load_oracle_mirror.sh cleanup
```

⚠️ **已知 cleanup 缺陷**: cleanup 不会杀掉 `pg_sleep(1800)` 长事务（脚本 ssh 远程
nohup 起的，cleanup 找不到对应 PID）。需手动补一刀：

```sql
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE query LIKE '%pg_sleep(1800)%' AND pid != pg_backend_pid();
```

## 配套数据

- 测试 OG: `47.251.30.180:15432` (gauss/OpenGauss@2026, OG 5.0.0 单机)
- 连接 alias: opendb 用 `og`, dbaa 用 `gaussdb` (database=postgres, user=opendb, auth_mode=save)
- bench 表初始化耗时: ~2 min (300 万 + 200 万 + 100 万行 INSERT)
- 满载 session: 60 idle-in-tx + 50 慢查询 + 40 hot 全扫 = ~150 sessions

## 历史 benchmark 结果

- v1.1.10 (deepseek-v4-pro): 14 轮, 3774 bytes — 漏 effective_cache_size + 死锁
- v1.1.10 (dbaa, deepseek-v4-pro): 12 轮, 634 bytes — 输出过简（streamRound bug）
- v1.1.21 (dbaa, deepseek-v4-pro): 7 轮, 5892 bytes — 完整 ✓
- v1.1.21 (opendb, deepseek-v4-pro): 9 轮, 4706 bytes — 完整 ✓
