# dbaa 安装包

> **dbaa**：数据库智能体 CLI（基于 OpenDB 引擎，针对农行场景定制）
> **版本**：v1.1.21
> **支持数据库**：GaussDB / Oracle / MySQL / PostgreSQL

---

## 文件清单

```
/tmp/dbaa/
├── README.md                            ← 本文件
├── config.yaml.template                 ← 配置文件模板（必读）
├── dbaa-linux-amd64                     ← Linux x86_64 主分发版（UPX 压缩，8.7 MB）
├── dbaa-linux-amd64-uncompressed        ← Linux x86_64 未压缩版（47 MB，备选）
├── dbaa-linux-arm64                     ← Linux ARM64 主分发版（UPX 压缩，7.2 MB）
└── dbaa-linux-arm64-uncompressed        ← Linux ARM64 未压缩版（45 MB，备选）
```

| 用途 | 选哪个 |
|------|------|
| 普通生产部署 | **`dbaa-linux-amd64`** 或 **`dbaa-linux-arm64`**（UPX 版，启动慢 ~150ms 可忽略）|
| 防病毒软件误报 UPX 包 | 改用 `*-uncompressed` 版 |
| Apple Silicon Mac | 当前包不含 macOS 版（如需请联系打包）|

---

## 一、安装

### 1. 选择对应架构二进制
```bash
# 检查目标机器架构
uname -m
# x86_64 → 用 dbaa-linux-amd64
# aarch64/arm64 → 用 dbaa-linux-arm64
```

### 2. 拷到 PATH
```bash
sudo cp dbaa-linux-amd64 /usr/local/bin/dbaa     # 或 arm64 版
sudo chmod +x /usr/local/bin/dbaa
dbaa --version                                    # 应输出: dbaa v1.1.21 (commit: ...)
```

### 3. 准备配置文件
```bash
mkdir -p ~/.dbaa
cp config.yaml.template ~/.dbaa/config.yaml
# 编辑 ~/.dbaa/config.yaml
# - 修改 connections: 数据库连接信息
# - 修改 models: 中的 api_key（替换 REPLACE_WITH_YOUR_API_KEY）
# - 选择 active_model: 指向你能用的模型 name
vi ~/.dbaa/config.yaml
```

### 4. 首次启动
```bash
dbaa                              # 默认进入第一个连接
dbaa -c gaussdb                   # 指定连接名启动
```

---

## 二、密码管理（生产推荐）

`config.yaml.template` 默认已启用加密存储模式：
```yaml
auth_mode: save
credential:
  provider: save
```

启动 dbaa 后输入：
```
/login
```
按提示输入数据库密码，自动加密保存到 `~/.dbaa/credentials/<name>.enc`，配置文件里**不会**留明文密码。

---

## 三、常用命令

进入 dbaa 后：

| 命令 | 说明 |
|------|------|
| `/help` | 命令总览（三层帮助）|
| `/model` | 切换 LLM 模型 |
| `/login` | 配置数据库密码（加密保存）|
| `/diag` | AI 诊断当前数据库 |
| `/sentinel` | 启停实时监控 |
| `/scheduler` | 定时任务管理 |
| `/policy` | 查看安全规范 |
| `/exit` 或 Ctrl+D | 退出 |

直接输入自然语言问题（中文）即可触发 LLM 诊断：
```
> 当前数据库存在什么问题
> 帮我分析这条慢 SQL: SELECT ... 
```

---

## 四、配置文件结构

详见 `config.yaml.template` 内每节注释。核心三块：

1. **`connections`**：数据库连接列表（一台 dbaa 可管多个库）
2. **`models`**：LLM 模型列表（云 API 或自部署）+ `active_model` 指向当前默认
3. **`sentinel` / `scheduler`**：监控和定时任务策略

---

## 五、卸载

```bash
sudo rm /usr/local/bin/dbaa           # 删二进制
rm -rf ~/.dbaa                        # 删配置（含加密密码、历史、会话）
```

---

## 六、常见问题

**Q：启动报 "permission denied"？**
A：`chmod +x dbaa-linux-*` 加执行权限。

**Q：启动报 "exec format error"？**
A：架构不匹配，确认机器是 x86_64 还是 aarch64，换对应版本。

**Q：UPX 压缩版被防病毒拦截？**
A：用 `*-uncompressed` 版替代（功能完全一致，只是体积大 5 倍）。

**Q：API key 不想写在配置里？**
A：把 `api_key` 字段值设为 `${ENV_VAR_NAME}`，从环境变量读取（待支持，当前需写在配置里）。

**Q：本地无 LLM API key 怎么办？**
A：模板里的 LLM 模型都是商用云 API（需购买 token）。如要本地部署 Qwen/GLM/Llama 等开源模型，参考 `~/.dbaa/config.yaml` 中注释掉的 `qwen-opus-local` / `ollama-qwen` 示例。

---

## 七、版本信息

| 项 | 值 |
|----|------|
| 版本 | v1.1.21 |
| 编译时间 | 见 `dbaa --version` |
| 上游 | https://github.com/sqlrush/opendb (`dbaa` 分支) |
| 部署文档（本地 LLM）| `docs/deploy-qwen-opus-vllm.md` |
