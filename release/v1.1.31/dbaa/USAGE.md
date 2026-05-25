# dbaa v1.1.31 使用指南

> 农业银行系统六部 · 数据库智能体（dbaa = DataBase AI Agent）
> 单二进制零依赖，类似 Claude Code 之于代码开发，dbaa 之于数据库管理。

---

## 1. 安装

### 1.1 二进制选择

| 平台 | 文件 | 大小 | 包含驱动 |
|---|---|---|---|
| Linux x86_64 | `dbaa-linux-amd64` | 10 MB（UPX 压缩） | Oracle / MySQL / PostgreSQL / openGauss / GaussDB / **DM** |
| Linux ARM64 | `dbaa-linux-arm64` | 8.8 MB（UPX 压缩） | 同上（含 DM，鲲鹏/飞腾国产化适用） |
| macOS Apple Silicon | `dbaa-darwin-arm64` | 49 MB | Oracle / MySQL / PostgreSQL / openGauss / GaussDB（无 DM） |
| macOS Intel x86_64 | `dbaa-darwin-amd64` | 51 MB | 同上 |

> 生产环境用 Linux 版本（驱动最全 + UPX 压缩省 80% 体积）。macOS 版仅本机测试用——上游 UPX 不支持 darwin 二进制压缩，所以 macOS 文件保持原始体积。

### 1.2 校验完整性

```bash
shasum -a 256 -c SHA256SUMS.txt
# 应输出 OK
```

### 1.3 部署

```bash
# Linux 生产服务器
chmod +x dbaa-linux-amd64
sudo cp dbaa-linux-amd64 /usr/local/bin/dbaa

# 或装到用户目录（无 sudo）
mkdir -p ~/.local/bin
cp dbaa-linux-amd64 ~/.local/bin/dbaa
chmod +x ~/.local/bin/dbaa
# 确保 PATH 包含 ~/.local/bin

# macOS — Apple Silicon 必须重签名（cp 后内核 kill）
xattr -cr dbaa-darwin-arm64 2>/dev/null || true
codesign --force -s - dbaa-darwin-arm64       # ← 重要：解决 "zsh: killed dbaa"
chmod +x dbaa-darwin-arm64
sudo cp dbaa-darwin-arm64 /usr/local/bin/dbaa
sudo codesign --force -s - /usr/local/bin/dbaa  # cp 后再签一次
```

### 1.4 验证

```bash
dbaa --version
# dbaa v1.1.31 (commit: ..., built: ...)
```

---

## 2. 首次配置

```bash
dbaa setup
```

向导会按顺序问：

1. **Welcome** → 回车继续
2. **Existing config check** → 全新安装自动跳过；已存在配置会问是否覆盖（旧文件自动备份为 `config.yaml.bak.YYYYMMDD-HHMMSS`）
3. **Setup mode** → QuickStart（推荐，仅配 DB + LLM）/ Custom（全量配置）
4. **Database type** → Oracle / MySQL / PostgreSQL / openGauss / GaussDB
   - 进入选项后第 1 秒内 Enter 不生效（防误过），按 ↑↓ 后立即解锁
5. **Permission guide** → 提示 DBA 应授予的最小权限集
6. **Connection form** → 填 host / port / db / user / password
7. **Connection test** → 自动 EXPLAIN 一条查询，输出 `n/n 必要权限就绪`
8. **Sentinel**（Custom 模式）→ 实时探针采样间隔 / 触发阈值
9. **LLM provider** → 云端（OpenAI/Moonshot/Doubao/DeepSeek/通义/智谱）或本地（Ollama/llama.cpp/vLLM/MLX）
10. **LLM configure** → API key / base_url / 模型名
11. **LLM test** → 给 LLM 发一句话，验证连通+推理
12. **Rule engine**（Custom）→ 启用规则引擎兜底
13. **Finalize** → 生成 `~/.dbaa/config.yaml`

---

## 3. 核心命令速查

### 进入交互式 REPL

```bash
dbaa                      # 不指定连接，进 REPL 后用 /conn 切换
dbaa -c <conn-name>       # 直接连指定连接进 REPL
```

### 批量模式（脚本化）

```bash
dbaa -c oratest /health   # 跑一条 skill 命令直接返回，不进 REPL
dbaa -c oratest "SELECT 1 FROM DUAL"  # 直接执行 SQL
```

### 常用 skill 命令（在 REPL 中输入）

| 命令 | 用途 |
|---|---|
| `/health` | 实例总览：状态、连接数、内存、表空间 |
| `/sessions` | 当前会话列表 |
| `/activesessions` | 仅 active 会话 |
| `/waits` | 等待事件分析 |
| `/blocktree` | 阻塞链树状视图 |
| `/topsql` | 高消耗 SQL TOP-N |
| `/slowsql` | 慢 SQL（默认 >1s） |
| `/sqlfetch <SQL_ID/queryid/DIGEST>` | 按 ID 拉可 EXPLAIN 的完整 SQL — og v1.1.28 / PG/MySQL/Oracle v1.1.31 |
| `/sqltune <SQL>` | SQL 性能优化（双轮 LLM 分析 + 候选验证） |
| `/llm <自然语言>` | 用自然语言提问，LLM 自主调用工具诊断 |
| `/explain <SQL>` | 执行计划 |
| `/locks` | 锁信息 |
| `/dbtop` | 类似 top 的实时大盘 |
| `/help` | 完整命令列表 |
| `/exit` | 退出 |

### 切换连接 / 模型

```bash
/conn         # 列出所有连接，选一个切换
/model        # 切换 LLM 模型
/login        # 重新输入密码（针对 auth_mode: save 的连接）
```

---

## 4. 配置文件

路径：`~/.dbaa/config.yaml`

### 4.1 数据库连接

```yaml
connections:
  # 方式 1: 明文密码（开发测试）
  - name: oratest
    db_type: oracle
    host: 192.168.1.100
    port: 1521
    service: orclpdb1
    user: system
    credential:
      value: MyPassword123    # 不设 auth_mode/provider 时直接用 value

  # 方式 2: 加密保存（生产）
  - name: oraprod
    db_type: oracle
    host: 10.0.0.50
    port: 1521
    service: prod1
    user: system
    auth_mode: save
    credential:
      provider: save           # 第一次连接时通过 /login 输入
                               # 自动加密保存到 ~/.dbaa/credentials/
```

### 4.2 LLM 配置（云端示例）

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

---

## 5. 常见诊断流程

### 5.1 系统慢，先做总览

```
/health
/waits
/activesessions
```

### 5.2 发现慢 SQL，深度优化

```
/slowsql                       # 找出慢 SQL
/sqltune <SQL_文本或 SQL_ID>    # LLM 双轮分析 + 候选方案
```

### 5.3 自然语言诊断

```
/llm 当前数据库 CPU 跑满，帮我看看根因
```

LLM 会自动串联 `/waits` `/topsql` `/blocktree` 等工具给结论。

### 5.4 阻塞排查

```
/blocktree                    # 看阻塞链
/sessions <PID>               # 查具体会话详情
```

---

## 6. 升级

把新版本二进制覆盖旧的即可，**配置文件不动**：

```bash
sudo cp dbaa-linux-amd64-vX.Y.Z /usr/local/bin/dbaa
dbaa --version
```

补充 release notes 见 `~/.dbaa/CHANGELOG.md`（首次启动后自动生成）或仓库 `docs/CHANGELOG.md`。

---

## 7. 故障排查

| 现象 | 排查 |
|---|---|
| `permission denied` | `chmod +x dbaa` |
| macOS "zsh: killed dbaa" / "无法验证开发者" | `codesign --force -s - dbaa`（必须）+ `xattr -cr dbaa` |
| 启动卡住 | 检查 `~/.dbaa/config.yaml` 中所有 active 连接是否可达 |
| LLM 长查询卡住 | `/model` 切到能力更强的模型；或缩窄诊断范围 |
| `auth_mode: save` 不读密码 | 删 `~/.dbaa/credentials/<name>.bin`，重跑 `/login` |
| 安装向导中输入键失效 | 终端不是 ANSI 兼容（检查 `$TERM`，至少 xterm-256color） |
| openGauss `gs_stat_activity` 报缺视图 | og-lite 无此视图；v1.1.31 已自动跳过 |

详细日志：`~/.dbaa/logs/dbaa.log`

---

## 8. 路径速查

| 路径 | 用途 |
|---|---|
| `~/.dbaa/config.yaml` | 主配置文件 |
| `~/.dbaa/credentials/` | 加密的连接密码 |
| `~/.dbaa/history/` | REPL 历史 |
| `~/.dbaa/memory/` | LLM 跨会话记忆 |
| `~/.dbaa/policies/` | 安全策略 |
| `~/.dbaa/logs/` | 运行日志 |

---

## 9. 反馈与支持

- 内部 issue：研发部·数据库智能化平台组
- 紧急问题：联系系统六部 DBA 值班

— 系统六部 · 农业银行
