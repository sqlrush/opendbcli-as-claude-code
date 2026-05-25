# dbaa + Qwen3.5-27B Opus 蒸馏模型 完整部署手册

> **本手册覆盖**：从零开始，一步步部署 dbaa + 本地 LLM 模型的完整方案
> **预计耗时**：首次部署 ~3 小时（其中 1.5 小时是模型下载等待）
> **适用版本**：dbaa v1.1.21 + vLLM 0.20.0 + Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled

---

## 部署架构总览

```
┌──────────────────────┐               ┌─────────────────────────────────┐
│  办公电脑 / 工作站   │               │  GPU 算力服务器                 │
│  (Linux / Mac)       │               │  (A800 80GB 或同级)             │
│                      │               │                                 │
│  ┌────────────────┐  │   SSH         │  ┌──────────────────────────┐  │
│  │  dbaa CLI      │──┼──tunnel──────►│  │  vLLM API Server         │  │
│  │  ~/.dbaa/...   │  │   8000:8000   │  │  http://127.0.0.1:8000   │  │
│  └────────┬───────┘  │               │  │  qwen3.5-27b-opus 模型   │  │
│           │          │               │  └──────────────────────────┘  │
│           │ JDBC/ODBC│               │                                 │
└───────────┼──────────┘               └─────────────────────────────────┘
            │
            ▼
   ┌────────────────────┐
   │  生产数据库        │
   │  Oracle/MySQL/PG/  │
   │  GaussDB           │
   └────────────────────┘
```

---

## 物料清单（部署前确认）

| 项 | 要求 | 备注 |
|----|------|------|
| **GPU 服务器** | NVIDIA A800/A100/H800/H100 ≥ 80GB 显存 | 阶段二会用 |
| **服务器系统盘** | ≥ 30 GB | 装 Python/CUDA 工具链 |
| **服务器数据盘** | ≥ 80 GB（建议 90GB+）| 放模型 55GB + KV cache |
| **服务器 OS** | Ubuntu 22.04 LTS（推荐）| CentOS 7/8 也行 |
| **CUDA 驱动** | ≥ 525（CUDA 12.4+）| `nvidia-smi` 能跑就行 |
| **dbaa 客户端机器** | Linux x86_64 / arm64 | 跟操作员桌面/终端机 |
| **网络** | 客户端能 SSH 到 GPU 服务器 | 可走专线、跳板机或 VPN |
| **数据库** | Oracle 11g+ / MySQL 5.7+ / PG 13+ / GaussDB | 至少能从客户端访问 |

---

## 阶段一：GPU 服务器选型

### 选项 A：AutoDL 公有云（推荐做 POC / 测试）

- 入口：https://autodl.com
- 推荐机型：**A800 80GB PCIe**，价格 ~¥6/h
- 选系统镜像：`PyTorch 2.5 / Python 3.11 / Ubuntu 22.04 / CUDA 12.4`
- 创建实例时**扩容数据盘到 90GB**（默认 50GB 装不下模型）

### 选项 B：阿里云 / 腾讯云 / 华为云

- 阿里云：`ecs.gn7e`（A100 80GB）或 `ecs.gn8v`（L20 48GB + Q8 量化）
- 腾讯云：`GT4.LARGE96`（A100 80GB）
- 价格高 5-10 倍，适合长期生产部署

### 选项 C：客户自有 GPU 服务器

- 必须 80GB 显存（FP16 部署）；48GB 也能跑但要量化
- 建议先按本手册步骤跑一次

---

## 阶段二：在 GPU 服务器上部署模型

### Step 1：登录服务器

```bash
ssh -p <端口> root@<服务器地址>
nvidia-smi              # 验证 GPU 可见
df -h /root/autodl-tmp  # 验证数据盘 ≥ 80GB
```

### Step 2：下载模型（44 分钟）

```bash
mkdir -p /root/autodl-tmp/Qwen3.5-27B-Opus-Distilled /root/autodl-tmp/logs

cat > /root/autodl-tmp/download.sh <<'BASH'
#!/bin/bash
set -u
# 国内用户走 hf-mirror 镜像，~22 MB/s；国外用 huggingface.co
BASE="https://hf-mirror.com/Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled/resolve/main"
OUT="/root/autodl-tmp/Qwen3.5-27B-Opus-Distilled"
LOG="/root/autodl-tmp/logs/download.log"

FILES=(
  "config.json" "tokenizer_config.json" "tokenizer.json"
  "processor_config.json" "chat_template.jinja" "model.safetensors.index.json"
  "model.safetensors-00001-of-00011.safetensors"
  "model.safetensors-00002-of-00011.safetensors"
  "model.safetensors-00003-of-00011.safetensors"
  "model.safetensors-00004-of-00011.safetensors"
  "model.safetensors-00005-of-00011.safetensors"
  "model.safetensors-00006-of-00011.safetensors"
  "model.safetensors-00007-of-00011.safetensors"
  "model.safetensors-00008-of-00011.safetensors"
  "model.safetensors-00009-of-00011.safetensors"
  "model.safetensors-00010-of-00011.safetensors"
  "model.safetensors-00011-of-00011.safetensors"
)

cd "$OUT"
for f in "${FILES[@]}"; do
  for attempt in $(seq 1 10); do
    wget -c --tries=3 --timeout=60 --connect-timeout=30 -O "$f" "$BASE/$f" 2>>"$LOG"
    [ $? -eq 0 ] && break
    sleep 15
  done
done
echo "[$(date)] === All done ===" >> "$LOG"
BASH

chmod +x /root/autodl-tmp/download.sh
nohup /root/autodl-tmp/download.sh > /root/autodl-tmp/logs/nohup.out 2>&1 &
```

#### 监控进度
```bash
# 每隔几分钟看一眼
du -sh /root/autodl-tmp/Qwen3.5-27B-Opus-Distilled
tail -f /root/autodl-tmp/logs/download.log
```

#### 下载完成校验
全部 17 个文件，总字节 **55,583,153,599**（51.76 GiB）。任何一个文件大小不对就重下：

```bash
ls -l /root/autodl-tmp/Qwen3.5-27B-Opus-Distilled/*.safetensors | awk '{print $5}' | paste -sd+ | bc
# 应输出：55563164472（11个权重分片字节和）
```

### Step 3：装 vLLM 推理框架（5-10 分钟）

```bash
# 配置 pip 缓存到数据盘（避免撑爆 30GB 系统盘）
mkdir -p /root/autodl-tmp/pip-cache /root/autodl-tmp/tmp
export TMPDIR=/root/autodl-tmp/tmp
export PIP_CACHE_DIR=/root/autodl-tmp/pip-cache

# 激活已有 conda 环境（AutoDL 默认有 miniconda3）
source /root/miniconda3/bin/activate

# 国内换源装 vLLM（自动拉 PyTorch 2.11 + CUDA 13 deps）
pip install -i https://pypi.tuna.tsinghua.edu.cn/simple --upgrade pip wheel setuptools
pip install -i https://pypi.tuna.tsinghua.edu.cn/simple vllm

# 验证
python -c "import vllm; print(vllm.__version__)"     # 应输出 0.20.0 或更高
```

### Step 4：写启动脚本

```bash
cat > /root/autodl-tmp/start-vllm.sh <<'BASH'
#!/bin/bash
source /root/miniconda3/bin/activate
export TMPDIR=/root/autodl-tmp/tmp
export HF_HOME=/root/autodl-tmp/hf-cache
exec python -m vllm.entrypoints.openai.api_server \
  --model /root/autodl-tmp/Qwen3.5-27B-Opus-Distilled \
  --served-model-name qwen3.5-27b-opus \
  --dtype bfloat16 \
  --max-model-len 65536 \
  --max-num-seqs 64 \
  --gpu-memory-utilization 0.93 \
  --enable-prefix-caching \
  --enable-auto-tool-choice \
  --tool-call-parser hermes \
  --reasoning-parser deepseek_r1 \
  --host 0.0.0.0 \
  --port 8000 \
  --trust-remote-code
BASH
chmod +x /root/autodl-tmp/start-vllm.sh
```

#### 关键参数说明（不要改）

| 参数 | 必须这么设的原因 |
|------|------|
| `--max-model-len 65536` | dbaa diag 单次 prompt 可达 24K tokens，加输出超过 32K |
| `--max-num-seqs 64` | 模型是 Mamba 混合架构，max_num_seqs > 348 启动失败 |
| `--gpu-memory-utilization 0.93` | 27B FP16 占 51GB，留 24GB 给 KV cache |
| `--enable-prefix-caching` | 重复 prefix 复用 KV cache，dbaa 多轮诊断快 30-50% |
| `--enable-auto-tool-choice` + `--tool-call-parser hermes` | dbaa 用 `tool_choice: auto`，不开会报 HTTP 400 |
| `--reasoning-parser deepseek_r1` | 模型输出 `<think>...</think>` CoT，让 vLLM 拆到独立字段 |
| `--trust-remote-code` | 多模态模型自定义 processor 需要 |

### Step 5：启动 vLLM（首次冷启动 6-9 分钟）

```bash
nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &
echo $!  # 记下 PID

# 等待启动完成
until curl -sf http://127.0.0.1:8000/v1/models >/dev/null 2>&1; do sleep 30; done
echo "✅ vLLM API ready"
```

#### 验证
```bash
# 列出模型
curl http://127.0.0.1:8000/v1/models

# 推理测试
curl http://127.0.0.1:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5-27b-opus","messages":[{"role":"user","content":"你好"}],"max_tokens":50}'
```

### Step 6：配置开机自启（关键，重启后免操作）

```bash
cat > /root/autodl-tmp/auto-start-vllm.sh <<'BASH'
#!/bin/bash
LOGDIR=/root/autodl-tmp/logs
mkdir -p "$LOGDIR"

# 已运行则跳过
if curl -sf --max-time 3 http://127.0.0.1:8000/v1/models >/dev/null 2>&1; then
  exit 0
fi

# 清理孤儿 GPU 进程（vLLM kill 后子进程可能残留）
PID=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null | head -1 | tr -d "\n ")
[ -n "$PID" ] && kill -9 "$PID" 2>/dev/null && sleep 5

nohup /root/autodl-tmp/start-vllm.sh > "$LOGDIR/vllm.log" 2>&1 &
BASH
chmod +x /root/autodl-tmp/auto-start-vllm.sh

# 加到 .bashrc 末尾
cat >> /root/.bashrc <<'EOF'

# Auto-start vLLM on shell login (added by deploy guide)
if [ -z "$VLLM_AUTOSTART_DONE" ] && [ -t 0 ]; then
  export VLLM_AUTOSTART_DONE=1
  /root/autodl-tmp/auto-start-vllm.sh 2>&1 | head -3
fi
EOF
```

每次 SSH 登录服务器都会自动检查 vLLM 是否运行，没运行就自动起。

---

## 阶段三：在客户端机器上安装 dbaa

### Step 1：选择对应架构二进制

```bash
# 在客户端机器上执行
uname -m
# 输出 x86_64    → 用 dbaa-linux-amd64
# 输出 aarch64   → 用 dbaa-linux-arm64
```

### Step 2：拷贝二进制 + 加权限

```bash
# 把交付包里的二进制拷过去
sudo cp dbaa-linux-amd64 /usr/local/bin/dbaa     # 或 arm64
sudo chmod +x /usr/local/bin/dbaa

# 验证
dbaa --version
# 应输出: dbaa v1.1.21 (commit: ...)
```

> **UPX 压缩版被防病毒拦截**：换成 `*-uncompressed` 版（功能完全一致）

### Step 3：准备配置文件

```bash
mkdir -p ~/.dbaa
cp config.yaml.template ~/.dbaa/config.yaml
chmod 600 ~/.dbaa/config.yaml      # 配置文件含敏感信息，限定只本用户可读
```

---

## 阶段四：配置 dbaa（核心）

编辑 `~/.dbaa/config.yaml`，按以下要点改：

### 4.1 数据库连接（`connections:` 数组）

#### 推荐方式：加密存储密码

```yaml
connections:
  - name: gaussdb_prod          # 连接别名，dbaa -c gaussdb_prod 启动
    db_type: opengauss          # oracle / mysql / postgres / opengauss
    host: 192.168.10.5
    port: 15432
    database: postgres
    user: dbadmin
    auth_mode: save             # 启用加密存储
    credential:
      provider: save             # 与 auth_mode 配对
```

首次启动 dbaa 后输入 `/login` 命令，按提示交互式输入密码，会自动加密保存到 `~/.dbaa/credentials/<name>.enc`。

#### 测试方式：明文密码

```yaml
connections:
  - name: oratest
    db_type: oracle
    host: 192.168.20.10
    port: 1521
    service: orclpdb1
    user: system
    credential:
      value: "明文密码"          # 仅开发测试用，生产严禁
```

### 4.2 LLM 模型（`models:` 数组）— 关键

把本地部署的 vLLM 模型加入 `models:` 列表：

```yaml
models:
  - name: qwen-opus-local       # dbaa 内通过 /model qwen-opus-local 切换
    provider: openai            # vLLM 是 OpenAI 兼容协议
    vendor: Self-Hosted
    base_url: http://localhost:8000/v1   # 注意是 localhost，要靠 SSH 隧道接到 GPU 服务器
    model: qwen3.5-27b-opus     # 与 vLLM 启动参数 --served-model-name 一致
    capability: large
    api_key: dummy              # vLLM 不验 key，但字段必须存在
    strip_think: true           # 剥离 <think>...</think> 思考块再展示
    description: Qwen3.5-27B Opus Distilled (本地 vLLM)
```

设置默认激活模型：
```yaml
active_model: qwen-opus-local
```

### 4.3 安全级别（`security:`）

```yaml
security:
  default_level: 0              # 0=只读 / 1=操作需确认 / 2=管理需确认 / 3=危险强制确认
  confirm_on_dangerous: true    # DDL / DML 等危险操作前弹出确认
```

> **生产环境强烈建议 `default_level: 1` 以上**，避免误操作。

### 4.4 Sentinel 实时监控（`sentinel:`）

```yaml
sentinel:
  auto_start: true              # 启动 dbaa 自动开
  probe_interval: 1s            # 探测频率
  trigger_mode: adaptive        # 自适应阈值（推荐），观察 60 秒基线后自动学习
  sigma: 3                      # 偏离基线 3 倍标准差视为异常
  cooldown: 5m                  # 同一告警 5 分钟内不重复
```

#### 关闭 Sentinel（仅诊断、不监控）

```yaml
sentinel:
  auto_start: false
```

### 4.5 完整配置示例（最小可用）

```yaml
active_model: qwen-opus-local

security:
  default_level: 1
  confirm_on_dangerous: true

output:
  format: terminal
  max_rows: 1000

llm:
  diagnose_mode: auto
  max_rounds: 0
  max_result_tokens: 4000

sentinel:
  auto_start: true
  trigger_mode: adaptive
  sigma: 3
  cooldown: 5m

connections:
  - name: gaussdb_prod
    db_type: opengauss
    host: 192.168.10.5
    port: 15432
    database: postgres
    user: dbadmin
    auth_mode: save
    credential:
      provider: save

models:
  - name: qwen-opus-local
    provider: openai
    vendor: Self-Hosted
    base_url: http://localhost:8000/v1
    model: qwen3.5-27b-opus
    capability: large
    api_key: dummy
    strip_think: true
```

---

## 阶段五：建 SSH 隧道连通本地 ↔ GPU 服务器

dbaa 在客户端机器上跑，但 vLLM 在 GPU 服务器上 — 客户端需要把本地 `localhost:8000` 转发到 GPU 服务器的 `127.0.0.1:8000`。

### 方案 A：autossh 守护（Linux/Mac，推荐生产）

```bash
sudo apt install autossh        # Ubuntu/Debian
# 或: brew install autossh       # Mac

# 后台启动隧道，断线自动重连
autossh -M 0 -f -N \
  -o "ServerAliveInterval=30" -o "ServerAliveCountMax=3" \
  -o "ExitOnForwardFailure=yes" \
  -p <GPU服务器SSH端口> \
  -L 8000:127.0.0.1:8000 \
  root@<GPU服务器地址>

# 验证
curl http://localhost:8000/v1/models
```

### 方案 B：systemd 服务（Linux 生产标配）

```bash
sudo tee /etc/systemd/system/dbaa-tunnel.service <<EOF
[Unit]
Description=SSH tunnel to dbaa GPU server
After=network-online.target

[Service]
Type=simple
User=$(whoami)
ExecStart=/usr/bin/autossh -M 0 -N \
  -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
  -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=no \
  -p <端口> -L 8000:127.0.0.1:8000 root@<GPU服务器>
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now dbaa-tunnel
sudo systemctl status dbaa-tunnel
```

### 方案 C：Mac 专用 launchd（如果客户端是 Mac）

详见 `docs/deploy-qwen-opus-vllm.md` 第六节「本地 SSH 隧道」。

### SSH 免密登录（避免每次输密码）

```bash
# 在客户端生成 key
ssh-keygen -t ed25519 -N "" -f ~/.ssh/id_ed25519_dbaa

# 把公钥加到 GPU 服务器
ssh-copy-id -p <端口> -i ~/.ssh/id_ed25519_dbaa.pub root@<GPU服务器>

# 之后 ssh / autossh 都自动免密
```

---

## 阶段六：启动验证（端到端）

### 6.1 验证 GPU 服务器侧
```bash
# 在 GPU 服务器上
curl http://127.0.0.1:8000/v1/models   # 应返回 qwen3.5-27b-opus
nvidia-smi --query-gpu=memory.used --format=csv,noheader  # 应显示 ~74GB
```

### 6.2 验证客户端侧（隧道）
```bash
# 在客户端机器上
curl http://localhost:8000/v1/models   # 应跟 GPU 服务器一样的输出
```

### 6.3 启动 dbaa 跑一次诊断
```bash
dbaa -c gaussdb_prod              # 进入连接

# dbaa 内部
> /model                          # 应看到 qwen-opus-local 在列表中
> /model qwen-opus-local           # 切换到本地模型
> /diag                            # 触发 AI 诊断
> 当前数据库存在什么问题
```

预期：
- 进入 dbaa 后显示连接成功 + Sentinel 启动
- 诊断 4-6 分钟内出结果（27B FP16 速度）
- 结论给出"先列分析维度再下结论"的 Opus 风格输出

### 6.4 退出
```
> /exit       # 或 Ctrl+D
```

---

## 阶段七：日常运维

### 7.1 GPU 服务器侧

| 任务 | 命令 |
|------|------|
| 检查 vLLM 状态 | `pgrep -fa vllm` + `curl http://127.0.0.1:8000/v1/models` |
| 看 vLLM 日志 | `tail -f /root/autodl-tmp/logs/vllm.log` |
| 重启 vLLM | `pkill -9 -f 'start-vllm.sh\|vllm.entrypoints\|EngineCore' && sleep 5 && nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &` |
| GPU 占用 | `nvidia-smi` |
| 释放孤儿进程 | `nvidia-smi --query-compute-apps=pid --format=csv,noheader \| xargs -r kill -9` |

### 7.2 客户端侧

| 任务 | 命令 |
|------|------|
| 隧道状态 | `systemctl status dbaa-tunnel` |
| 隧道重启 | `sudo systemctl restart dbaa-tunnel` |
| 测试 LLM 通 | `curl http://localhost:8000/v1/models` |
| 看 dbaa 日志 | dbaa 内输入 `/log` 或查 `~/.dbaa/sessions/` |
| 切换模型 | dbaa 内 `/model <name>` |
| 加密保存密码 | dbaa 内 `/login` |

### 7.3 数据库连接管理

| 任务 | 命令 |
|------|------|
| 列连接 | dbaa 内 `/conn` 或 `/connections` |
| 切连接 | dbaa 内 `/conn <name>` |
| 加新连接 | 编辑 `~/.dbaa/config.yaml` 的 `connections:` 后重启 |
| 改密码 | dbaa 内 `/login`（覆盖旧加密密码）|

---

## 八、常见问题（FAQ）

### Q1：vLLM 启动报 "max_num_seqs (1024) exceeds available Mamba cache blocks (348)"
**A**：模型是 Mamba 混合架构，必须设 `--max-num-seqs ≤ 348`。本手册启动参数已设 `64`，如自己改过请改回。

### Q2：dbaa 报 "HTTP 400: maximum context length is 32768 tokens"
**A**：vLLM 上下文不够。本手册启动参数 `--max-model-len 65536` 已修；如自己改过请确认。

### Q3：dbaa 报 "connection refused" / 模型不可用
**A**：SSH 隧道断了。
```bash
sudo systemctl status dbaa-tunnel
sudo systemctl restart dbaa-tunnel
curl http://localhost:8000/v1/models   # 验证
```

### Q4：`pkill vllm` 后 GPU 内存没释放
**A**：vLLM EngineCore 子进程会变孤儿。
```bash
nvidia-smi --query-compute-apps=pid --format=csv,noheader | xargs -r kill -9
```

### Q5：dbaa 启动报 "permission denied"
**A**：`chmod +x /usr/local/bin/dbaa`

### Q6：dbaa 启动报 "exec format error"
**A**：架构不匹配。`uname -m` 看是 x86_64 还是 aarch64，换对应版本。

### Q7：诊断很慢（每次 6+ 分钟）
**A**：27B FP16 在 A800 上正常 ~28 tok/s，单次诊断 4-6 分钟正常。如果想更快：
- 选项 A：升级到 H100（速度翻倍）
- 选项 B：换 Q8 量化（28GB，速度 +50%）
- 选项 C：换更快的云 API 模型（DeepSeek/GLM/Kimi）

### Q8：UPX 二进制被杀毒软件拦
**A**：用 `*-uncompressed` 版本（47MB / 45MB）替代，功能一致。

### Q9：诊断输出有 `<think>` 标签或乱码
**A**：检查 `~/.dbaa/config.yaml` 中模型条目是否设了 `strip_think: true`。

### Q10：模型下载中断
**A**：`download.sh` 已用 `wget -c` 支持断点续传。直接重跑脚本即可（已下完的文件会跳过）。

---

## 九、性能基准参考

### 单次诊断耗时（A800 80GB FP16，dbaa 经典 sqltune 任务）

| 阶段 | 耗时 |
|------|------|
| 路由判断 | ~10s |
| 第 1 轮 LLM（24K 输入 prefill）| 60-90s |
| 工具执行（DB 查询）| 10-20s |
| 第 2 轮 LLM | 70-100s |
| 第 3 轮综合分析 | 80-120s |
| 最终输出生成 | 100-150s |
| **总耗时** | **~4-6 分钟** |

### GPU 资源
- 模型加载：51 GiB（FP16 权重）
- 运行时占用：~74 GiB / 80 GiB（含 KV cache）
- 推理速度：~28 tokens/s
- 启动冷加载：6-9 分钟（首次）/ 3 分钟（编译缓存命中）

---

## 十、附录

### A. 完整启动检查 checklist

部署完成后逐项验证：

- [ ] GPU 服务器 nvidia-smi 显示 A800 80GB 正常
- [ ] 数据盘 ≥ 80GB，模型文件 17 个齐全
- [ ] vLLM 启动后 `curl /v1/models` 返回 qwen3.5-27b-opus
- [ ] vLLM 占用 ~74GB GPU 内存
- [ ] vLLM 启动脚本 `auto-start-vllm.sh` 加到 `.bashrc`
- [ ] 客户端 `dbaa --version` 正常
- [ ] 客户端 `~/.dbaa/config.yaml` 配置完成
- [ ] SSH 免密登录到 GPU 服务器配置好
- [ ] systemd 服务 `dbaa-tunnel` enabled 并 active
- [ ] 客户端 `curl http://localhost:8000/v1/models` 返回模型信息
- [ ] dbaa 启动后 `/login` 配置数据库密码
- [ ] dbaa `/diag` 一次成功跑完不报错

### B. 关键文件路径速查

| 路径 | 用途 |
|------|------|
| GPU 服务器 `/root/autodl-tmp/Qwen3.5-27B-Opus-Distilled/` | 模型权重 |
| GPU 服务器 `/root/autodl-tmp/start-vllm.sh` | vLLM 启动脚本 |
| GPU 服务器 `/root/autodl-tmp/logs/vllm.log` | vLLM 日志 |
| 客户端 `/usr/local/bin/dbaa` | dbaa 二进制 |
| 客户端 `~/.dbaa/config.yaml` | dbaa 主配置 |
| 客户端 `~/.dbaa/credentials/` | 加密密码（自动生成）|
| 客户端 `~/.dbaa/sessions/` | 历史会话 |
| 客户端 `/etc/systemd/system/dbaa-tunnel.service` | SSH 隧道服务 |

### C. 命令速查

#### GPU 服务器
```bash
# 启动
nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &

# 重启
pkill -9 -f 'start-vllm.sh|vllm.entrypoints|EngineCore'
sleep 5
nvidia-smi --query-compute-apps=pid --format=csv,noheader | xargs -r kill -9
sleep 3
nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &

# 状态检查
curl http://127.0.0.1:8000/v1/models | head -c 200
nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader
```

#### 客户端
```bash
# 启动
dbaa -c <连接名>

# 隧道维护
sudo systemctl status dbaa-tunnel
sudo systemctl restart dbaa-tunnel

# 验证 LLM
curl http://localhost:8000/v1/models
```

#### dbaa 内常用命令
```
/help              帮助
/model             列出/切换模型
/conn              列出/切换数据库连接
/login             配置数据库密码
/diag              AI 诊断
/sentinel          实时监控控制
/scheduler         定时任务管理
/policy            查看安全策略
/log               查看会话日志
/exit              退出
```

---

## 部署完成

完成本手册全部步骤后，你的 dbaa 系统应能：

- ✅ 在客户端机器上独立运行（生产环境）
- ✅ 通过 SSH 隧道使用 GPU 服务器上的本地 LLM
- ✅ 数据不出客户网络（除非选用云 API 模型）
- ✅ Sentinel 实时监控 + AI 诊断 + 工具调用闭环
- ✅ 关机重启后自动恢复

如遇本手册未覆盖的问题，请收集以下信息提交反馈：
1. `dbaa --version` 输出
2. GPU 服务器 `vllm.log` 末 50 行
3. 客户端 `~/.dbaa/sessions/<最新session>` 文件
4. 错误现象描述与重现步骤

---

**手册版本**：v1.0（2026-04-29）
**对应 dbaa 版本**：v1.1.21
**对应模型**：Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled
