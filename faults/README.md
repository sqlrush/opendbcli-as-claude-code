# 故障模拟脚本库

为 opendb / dbaa 的 `/health` `/llm` `/sentinel` 等诊断功能提供**可复现的故障场景**。

## 目录结构

```
faults/
├── README.md                     # 本文件
├── og/                           # openGauss / Postgres 系列
│   ├── og_complex_local.sh       # 本地 docker 复合饱和故障 (5+ 类，推荐)
│   ├── og_load_complex.sh        # [远程] v1.1.09 benchmark 6 项 (XID/bloat/idle-tx/WAL/stats/TOAST)
│   ├── og_load_mixed_a.sh        # [远程] /llm 经典 benchmark 4 项 (长事务级联)
│   ├── og_load_oracle_mirror.sh  # [远程] Oracle 形态镜像 5 项
│   └── og_load_app_antipatterns.sh # [远程] sqltune 用 5 反模式
├── oracle/                       # 待补
├── mysql/                        # 待补
└── pg/                           # 待补
```

## 目标实例约定

| 标签 | 实例 | 端口 | 凭据 | 用途 |
|---|---|---|---|---|
| `[本地]` | docker 容器（OrbStack）| 见 `~/database/README.md` | 见 `~/.opendb/config.yaml` | 自给自足，新机器即用 |
| `[远程]` | 47.251.30.180:15432 | gauss / OpenGauss@2026 | `~/cerebrate/...` 测试服 | 旧脚本沿用，需 ssh 免密 |

新写脚本一律以**本地 docker** 为目标。旧 `[远程]` 脚本保留供历史 benchmark 复现，路径已从 `scripts/` 移过来。

## 故障覆盖矩阵

| 故障维度 | og_complex_local | og_load_complex | og_load_mixed_a | og_load_oracle_mirror | og_load_app_antipatterns |
|---|:-:|:-:|:-:|:-:|:-:|
| 连接数冲高 (~max) | ● | | ●(110) | (50) | |
| idle-in-transaction | ● | ● | ●(60) | | |
| autovacuum 阻塞 | ● | ● | ● | | |
| Seq Scan / 缺索引 | ● | | ● | ● | ● |
| 行锁/热行争用 | ● | | | ● | ● |
| CPU 饱和 | ● | | | ● | |
| WAL / checkpoint 压力 | ● | ● | | | |
| Index/TOAST bloat | | ● | | | |
| 统计信息过期 | | ● | | | |
| XID wraparound 风险 | | ● | | | |
| 死锁 | | | | ● | |
| 无效对象 | | | | ● | |
| 函数包裹谓词 | | | | | ● |
| LIKE 前导通配符 | | | | | ● |
| 深分页 OFFSET | | | | | ● |
| NOT IN + NULL | | | | | ● |

## 通用用法

每个脚本都遵循 `setup / verify / cleanup` 三阶段：

```bash
bash faults/og/og_complex_local.sh setup    # 起负载
bash faults/og/og_complex_local.sh verify   # 检查症状是否触发（连接数/等待事件/dead_tup）
dbaa -c og /health                           # 看 dbaa 是否报告告警
dbaa -c og /llm "诊断当前最严重的 3 个问题"  # 看 LLM 怎么分析
bash faults/og/og_complex_local.sh cleanup   # 清场（务必，否则 200 长事务一直占连接）
```

## 编写新脚本须知

- 所有故障会话用 `SET application_name='fault_F<N>_<type>_<idx>'` 打 tag
- cleanup 必须能 `pg_terminate_backend` 所有 `application_name LIKE 'fault_%'` 的会话
- 不要 hardcode 远程 SSH，本地脚本走 `docker exec`
- 大表数据生成走 `generate_series`，避免依赖外部数据
- 所有 setup 操作幂等（`DROP IF EXISTS` / `CREATE IF NOT EXISTS`）
