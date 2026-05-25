# linkdb v1.1.29 使用指南

> 仁合时创 · 数据库智能体（linkdb = Link DataBase）
> 单二进制零依赖，类似 Claude Code 之于代码开发，linkdb 之于数据库管理。

---

## 1. 安装

### 1.1 二进制选择

| 平台 | 文件 | 大小 | 包含驱动 |
|---|---|---|---|
| Linux x86_64 | `linkdb-linux-amd64` | 10 MB（UPX 压缩） | Oracle / MySQL / PostgreSQL / openGauss / GaussDB / **达梦 DM** |
| Linux ARM64 | `linkdb-linux-arm64` | 8.8 MB（UPX 压缩） | 同上（含 DM，鲲鹏/飞腾国产化适用） |
| macOS Apple Silicon | `linkdb-darwin-arm64` | 49 MB | Oracle / MySQL / PostgreSQL / openGauss / GaussDB（无 DM） |
| macOS Intel x86_64 | `linkdb-darwin-amd64` | 51 MB | 同上 |

> 国产化场景必须用 Linux 版本（DM 驱动只支持 Linux/Windows，且 UPX 压缩省 80% 体积）。
> macOS 版只供本机开发测试用——上游 UPX 不支持 darwin 压缩，体积保持原始大小。

### 1.2 校验完整性

```bash
shasum -a 256 -c SHA256SUMS.txt
# 应输出 OK
```

### 1.3 部署

```bash
# Linux 生产服务器
chmod +x linkdb-linux-amd64
sudo cp linkdb-linux-amd64 /usr/local/bin/linkdb

# 或装到用户目录（无 sudo）
mkdir -p ~/.local/bin
cp linkdb-linux-amd64 ~/.local/bin/linkdb
chmod +x ~/.local/bin/linkdb
# 确保 PATH 含 ~/.local/bin

# macOS — Apple Silicon 必须重签名（cp 后内核 kill）
xattr -cr linkdb-darwin-arm64 2>/dev/null || true
codesign --force -s - linkdb-darwin-arm64       # ← 重要：解决 "zsh: killed linkdb"
chmod +x linkdb-darwin-arm64
sudo cp linkdb-darwin-arm64 /usr/local/bin/linkdb
sudo codesign --force -s - /usr/local/bin/linkdb  # cp 后再签一次
```

### 1.4 验证

```bash
linkdb --version
# linkdb v1.1.29 (commit: ..., built: ...)
```

---

## 2. 首次配置

```bash
linkdb setup
```

向导按顺序：

1. **Welcome** → 回车继续
2. **Existing config check** → 全新装自动跳过；已有配置会问覆盖确认（旧文件自动备份成 `config.yaml.bak.YYYYMMDD-HHMMSS`）
3. **Setup mode** → QuickStart（仅配 DB + LLM）/ Custom（含 Sentinel 等）
4. **Database type** → Oracle / MySQL / PostgreSQL / openGauss / GaussDB / **DM 达梦**
   - 进入选项第 1 秒内 Enter 不生效（防误过），按 ↑↓ 后立即解锁
5. **Permission guide** → 提示 DBA 应授予的最小权限
6. **Connection form** → 填 host / port / db / user / password
7. **Connection test** → 自动跑权限校验，提示 `n/n 必要权限就绪`
8. **Sentinel**（Custom）→ 实时探针参数
9. **LLM provider** → 云端（OpenAI / Moonshot / Doubao / DeepSeek / 通义 / 智谱）或本地（Ollama / **llama.cpp** / vLLM / MLX）
10. **LLM configure** → 模型 / API key / base_url
11. **LLM test** → 验证 LLM 连通+推理
12. **Rule engine**（Custom）→ 启用规则引擎兜底
13. **Finalize** → 写入 `~/.linkdb/config.yaml`

---

## 3. 核心命令速查

### 进入 REPL

```bash
linkdb                    # 不指定连接，进 REPL 后用 /conn 切换
linkdb -c <conn-name>     # 直接连指定连接
```

### 批量模式（脚本化）

```bash
linkdb -c og /health                     # 跑一条 skill 命令
linkdb -c og "SELECT version()"          # 直接执行 SQL
```

### 常用 skill 命令（REPL 内输入）

| 命令 | 用途 |
|---|---|
| `/health` | 实例总览：状态 / 连接数 / 内存 / 表空间 |
| `/sessions` | 会话列表 |
| `/activesessions` | 仅 active 会话 |
| `/waits` | 等待事件分析 |
| `/blocktree` | 阻塞链树形视图 |
| `/topsql` | 高消耗 SQL TOP-N |
| `/slowsql` | 慢 SQL（默认 >1s） |
| `/sqlfetch <SQL_ID/queryid/DIGEST>` | 按 ID 拉可 EXPLAIN 的完整 SQL — og v1.1.28 / PG/MySQL/Oracle v1.1.29 |
| `/sqltune <SQL>` | SQL 性能优化（双轮 LLM 分析 + 候选验证） |
| `/llm <自然语言>` | 自然语言提问，LLM 自动调用工具诊断 |
| `/explain <SQL>` | 执行计划 |
| `/locks` | 锁信息 |
| `/dbtop` | 实时大盘 |
| `/help` | 完整命令列表 |
| `/exit` | 退出 |

### 切换连接 / 模型

```
/conn         # 列出连接，选一个切换
/model        # 切换 LLM 模型
/login        # 重新输入密码（auth_mode: save 时）
```

---

## 4. 配置文件

路径：`~/.linkdb/config.yaml`

### 4.1 数据库连接（达梦示例）

```yaml
connections:
  - name: dm-prod
    db_type: dm
    host: 10.10.10.10
    port: 5236
    user: SYSDBA
    credential:
      value: SYSDBA001        # 明文（不设 auth_mode）
    # 或加密保存：
    # auth_mode: save
    # credential:
    #   provider: save

  # openGauss 示例
  - name: og
    db_type: opengauss
    host: 127.0.0.1
    port: 5432
    database: postgres
    user: gaussdb
    credential:
      value: Root@1234

  # Oracle 示例
  - name: oratest
    db_type: oracle
    host: 192.168.1.100
    port: 1521
    service: orclpdb1
    user: system
    credential:
      value: MyPassword123
```

### 4.2 LLM 配置（云端示例 — 推荐生产）

```yaml
active_model: kimi-k2

models:
  - name: kimi-k2
    provider: openai
    vendor: Moonshot
    base_url: https://api.moonshot.cn/v1
    model: kimi-k2-turbo-preview
    api_key: sk-...
    capability: large

llm:
  provider: openai
  capability: large
  diagnose_mode: assist
```

### 4.3 LLM 配置（本地 llama.cpp）

```yaml
active_model: qwen36-35b-local

models:
  - name: qwen36-35b-local
    provider: openai
    vendor: llama.cpp
    base_url: http://127.0.0.1:8081/v1
    model: Qwen3.6-35B-A3B
    api_key: sk-no-key-required
    capability: large
```

启动 llama-server：

```bash
llama-server -m /path/to/model.gguf \
  --host 127.0.0.1 --port 8081 \
  -ngl 99 -c 65536 --jinja
```

---

## 5. 常见诊断流程

### 5.1 系统慢 — 先看总览

```
/health
/waits
/activesessions
```

### 5.2 发现慢 SQL，深度优化

```
/slowsql                       # 列出慢 SQL
/sqltune <SQL 文本>             # LLM 双轮分析 + 候选方案 + 验证
```

### 5.3 自然语言诊断

```
/llm 数据库 CPU 跑满帮我看看根因
```

LLM 自动串联 `/waits` `/topsql` `/blocktree` 等工具给结论。

### 5.4 阻塞排查

```
/blocktree                    # 阻塞链
/sessions <PID>               # 具体会话
```

---

## 6. 达梦（DM）专项注意事项

- **驱动只在 Linux/Windows 编译**：macOS 编译的 linkdb 不含 DM 驱动；选 DM 类型连接会报"驱动未注册"
- **生产部署**：用 `linkdb-linux-amd64`（已包含 DM 驱动）
- **DM 版本支持**：DM8（主线测试通过），DM7 部分功能受限

---

## 7. 升级

新版本二进制覆盖旧的即可，**配置不动**：

```bash
sudo cp linkdb-linux-amd64-vX.Y.Z /usr/local/bin/linkdb
linkdb --version
```

CHANGELOG 见 `docs/CHANGELOG.md`。

---

## 8. 故障排查

| 现象 | 排查 |
|---|---|
| `permission denied` | `chmod +x linkdb` |
| macOS "zsh: killed linkdb" / "无法验证开发者" | `codesign --force -s - linkdb`（必须）+ `xattr -cr linkdb` |
| DM 报"驱动未注册" | 用 Linux 二进制（macOS 不支持 DM） |
| 启动卡住 | 检查 `~/.linkdb/config.yaml` 所有 active 连接是否可达 |
| LLM 长查询卡住 | `/model` 切到能力更强的模型 |
| `auth_mode: save` 不读密码 | 删 `~/.linkdb/credentials/<name>.bin`，重跑 `/login` |
| og `gs_stat_activity` 报缺视图 | og-lite 无此视图；v1.1.29 已自动跳过 |
| `/sqltune` 报 "phase A failed" | SQL 不能含 `SET ...; SELECT ...` 多语句；改用 schema-qualified（如 `db.t` 替代 `SET search_path`） |

详细日志：`~/.linkdb/logs/linkdb.log`

---

## 9. 路径速查

| 路径 | 用途 |
|---|---|
| `~/.linkdb/config.yaml` | 主配置 |
| `~/.linkdb/credentials/` | 加密密码 |
| `~/.linkdb/history/` | REPL 历史 |
| `~/.linkdb/memory/` | LLM 跨会话记忆 |
| `~/.linkdb/policies/` | 安全策略 |
| `~/.linkdb/logs/` | 运行日志 |

---

## 10. 反馈与支持

- 内部 issue：仁合时创·研发部
- 紧急问题：联系数据库平台组

— 仁合时创
