# Qwen3.5-27B Opus 蒸馏模型 vLLM 部署手册

> **场景**：在 AutoDL A800 80GB 算力服务器上部署 Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled，通过 SSH 隧道接入本地 OpenDB / dbaa。
>
> **完成日期**：2026-04-29
> **总耗时**：~2 小时（清磁盘 + 下载 + 部署 + 调参）

---

## 一、整体架构

```
┌──────────────────┐         ┌──────────────────────────────────┐
│  本地 Mac        │         │  AutoDL 容器 (A800 80GB)          │
│  OpenDB / dbaa   │         │  Ubuntu 22.04 / CUDA 13          │
│                  │  SSH    │                                  │
│  localhost:8000 ─┼─tunnel──┼→ 127.0.0.1:8000 ←─ vLLM 0.20    │
│         ↑        │         │      ↓                           │
│   autossh        │         │  /root/autodl-tmp/...            │
│  (launchd 守护)  │         │  Qwen3.5-27B-Opus-Distilled (55GB)│
└──────────────────┘         └──────────────────────────────────┘
```

---

## 二、AutoDL 实例规格

| 项 | 值 | 备注 |
|----|------|------|
| 厂商 | AutoDL（autodl.com）| 国内最便宜的 GPU 租用 |
| 入口 | `ssh -p 44244 root@connect.nma1.seetacloud.com` | 端口随实例变化 |
| GPU | 1× NVIDIA A800 80GB PCIe | A100 80GB 国行版 |
| GPU 驱动 | 580.105.08 / CUDA 13.0 | 容器镜像预装 |
| CUDA Toolkit | 12.8 | `/usr/local/cuda-12.8` |
| CPU/RAM | 2× Xeon Gold 6348 / 1TB RAM | 远超需求 |
| 系统盘 `/` | 30 GB（容器 overlay）| 不能放大文件 |
| 数据盘 `/root/autodl-tmp` | 90 GB | **模型必须放这里** |
| 共享内存 `/dev/shm` | 60 GB | tmpfs，重启丢失 |
| 价格 | ~¥6/h 按量；包月 ~¥4500-5500 | 比阿里云 GT4 A100 便宜 6 倍 |

⚠️ **AutoDL 没有公网 IP**，必须通过 SSH 隧道访问任何端口。

---

## 三、模型来源

| 项 | 值 |
|----|------|
| 仓库 | [Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled](https://huggingface.co/Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled) |
| 大小 | 55.6 GB（11 个 safetensors 分片，FP16）|
| 架构 | Qwen3.5-27B 多模态（vision tower + Mamba 混合）|
| 原生上下文 | 256K tokens（max_position_embeddings=262144）|
| 蒸馏数据 | Claude Opus 4.6 推理轨迹 |
| 镜像加速 | `https://hf-mirror.com`（国内 ~22 MB/s）|

---

## 四、部署步骤

### Step 1：磁盘准备

AutoDL 数据盘默认 50GB，**必须扩容到 ≥ 80GB** 才能放 55.6GB 模型。

- 在 AutoDL 控制台 → 实例 → 「扩容数据盘」
- 扩到 **90 GB**（额外 40GB × ¥1/100GB/天 ≈ ¥12/月）
- 关机后扩容也行，不丢数据

### Step 2：从 hf-mirror.com 下载模型（44 分钟）

```bash
ssh -p 44244 root@connect.nma1.seetacloud.com

mkdir -p /root/autodl-tmp/Qwen3.5-27B-Opus-Distilled /root/autodl-tmp/logs

cat > /root/autodl-tmp/download.sh <<'BASH'
#!/bin/bash
set -u
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

下载完后字节级校验（HF 官方文件大小）：

| 文件 | 字节数 |
|------|--------|
| model.safetensors-00001-of-00011 | 5263851872 |
| model.safetensors-00002-of-00011 | 5347741440 |
| model.safetensors-00003-of-00011 | 5347741504 |
| model.safetensors-00004-of-00011 | 5347741504 |
| model.safetensors-00005-of-00011 | 5347741504 |
| model.safetensors-00006-of-00011 | 5347741504 |
| model.safetensors-00007-of-00011 | 5347741496 |
| model.safetensors-00008-of-00011 | 5368714128 |
| model.safetensors-00009-of-00011 | 5347745520 |
| model.safetensors-00010-of-00011 | 5347749200 |
| model.safetensors-00011-of-00011 | 2148512760 |
| tokenizer.json | 19989343 |
| **总和** | **55583153599 字节（51.76 GiB）** |

### Step 3：装 vLLM（~5 分钟）

```bash
mkdir -p /root/autodl-tmp/pip-cache /root/autodl-tmp/tmp

source /root/miniconda3/bin/activate

export TMPDIR=/root/autodl-tmp/tmp
export PIP_CACHE_DIR=/root/autodl-tmp/pip-cache

pip install -i https://pypi.tuna.tsinghua.edu.cn/simple --upgrade pip wheel setuptools
pip install -i https://pypi.tuna.tsinghua.edu.cn/simple vllm
```

实际装上的版本：vllm 0.20.0 + torch 2.11.0 + transformers 5.7.0 + flashinfer 0.6.8

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

### Step 5：启动 vLLM（首次冷启动 6-9 分钟）

```bash
nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &

# 等启动完成
until curl -sf http://127.0.0.1:8000/v1/models >/dev/null 2>&1; do sleep 30; done
echo "vLLM ready"
```

### Step 6：本地 Mac 配 SSH 隧道（autossh + launchd）

`~/Library/LaunchAgents/com.yingjiewang.vllm-tunnel.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.yingjiewang.vllm-tunnel</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/bin/autossh</string>
        <string>-M</string><string>0</string>
        <string>-N</string>
        <string>-o</string><string>ServerAliveInterval=30</string>
        <string>-o</string><string>ServerAliveCountMax=3</string>
        <string>-o</string><string>ExitOnForwardFailure=yes</string>
        <string>-p</string><string>44244</string>
        <string>-L</string><string>8000:127.0.0.1:8000</string>
        <string>root@connect.nma1.seetacloud.com</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>EnvironmentVariables</key>
    <dict><key>AUTOSSH_GATETIME</key><string>0</string></dict>
</dict>
</plist>
```

加载：
```bash
launchctl load ~/Library/LaunchAgents/com.yingjiewang.vllm-tunnel.plist
```

验证：
```bash
curl http://localhost:8000/v1/models
```

### Step 7：客户端配置

#### OpenDB（`~/.opendb/models/llm.yaml`）
```yaml
- api_key: dummy
  base_url: http://localhost:8000/v1
  capability: large
  description: Qwen3.5-27B Opus Distilled (AutoDL A800 vLLM, via SSH tunnel)
  model: qwen3.5-27b-opus
  name: qwen-opus-local
  provider: openai
  strip_think: true
  vendor: AutoDL Self-Hosted
```

#### dbaa（`~/.dbaa/config.yaml` 的 `models:` 数组）
```yaml
- name: qwen-opus-local
  provider: openai
  vendor: AutoDL Self-Hosted
  base_url: http://localhost:8000/v1
  model: qwen3.5-27b-opus
  capability: large
  api_key: dummy
  strip_think: true
  description: Qwen3.5-27B Opus Distilled (AutoDL A800 vLLM, via SSH tunnel)
```

切换：
```
opendb -c oratest
> /model qwen-opus-local
```

---

## 五、vLLM 关键参数详解

| 参数 | 值 | 为什么 |
|------|------|------|
| `--max-model-len` | **65536** | OpenDB diag prompt 可达 24K+，加 8K 输出超过 32K。模型原生支持 256K，64K 是平衡点 |
| `--max-num-seqs` | **64** | **Mamba 混合架构**限制：max_num_seqs 必须 ≤ 348 Mamba cache 块，否则 cudagraph 失败 |
| `--gpu-memory-utilization` | **0.93** | 27B FP16 占 51GB，留 ~24GB 给 KV cache + CUDA graph |
| `--enable-auto-tool-choice` + `--tool-call-parser hermes` | 必开 | OpenDB `/diag` 用 `tool_choice: auto`，不开报 HTTP 400 |
| `--reasoning-parser deepseek_r1` | 必开 | 模型输出 `<think>...</think>` CoT，让 vLLM 拆到 `reasoning` 字段 |
| `--enable-prefix-caching` | **必开** | 复用前缀 KV cache，重复 prompt 第 2/3 轮 prefill 直接命中。Mamba 混合架构下加速 30-50%（vLLM 标记 experimental，实测稳定） |
| `--dtype bfloat16` | 默认 | 推理质量等同 FP16，BF16 在 A800 上更稳定 |
| `--trust-remote-code` | 必开 | 多模态模型自定义 processor 需要 |

---

## 六、运维操作

### 重启 vLLM（修改配置后）

```bash
ssh -p 44244 root@connect.nma1.seetacloud.com

# 杀干净（必须包括孤儿 EngineCore 子进程）
pkill -9 -f 'start-vllm.sh|vllm.entrypoints|EngineCore'
sleep 5

# 验证 GPU 释放
nvidia-smi --query-compute-apps=pid,used_memory --format=csv

# 如果还有残留 PID，手动 kill
kill -9 <PID>

# 等待 GPU 干净
until [ "$(nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits)" -lt 1000 ]; do sleep 3; done

# 重启
nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &
```

### 自启动（关机重开后）

`/root/autodl-tmp/auto-start-vllm.sh`：
```bash
#!/bin/bash
LOGDIR=/root/autodl-tmp/logs
mkdir -p "$LOGDIR"

if curl -sf --max-time 3 http://127.0.0.1:8000/v1/models >/dev/null 2>&1; then
  exit 0
fi

# 清理孤儿 GPU 进程
PID=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null | head -1 | tr -d "\n ")
[ -n "$PID" ] && kill -9 "$PID" 2>/dev/null && sleep 5

nohup /root/autodl-tmp/start-vllm.sh > "$LOGDIR/vllm.log" 2>&1 &
```

`/root/.bashrc` 末尾加：
```bash
if [ -z "$VLLM_AUTOSTART_DONE" ] && [ -t 0 ]; then
  export VLLM_AUTOSTART_DONE=1
  /root/autodl-tmp/auto-start-vllm.sh 2>&1 | head -3
fi
```

每次 SSH 登录 AutoDL 自动检查并启动。

### 检查服务状态

```bash
# AutoDL 上
ps -ef | grep vllm | grep -v grep
nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader
curl http://127.0.0.1:8000/v1/models
tail -f /root/autodl-tmp/logs/vllm.log

# 本地 Mac 上
launchctl list | grep vllm
curl http://localhost:8000/v1/models
tail -f /tmp/vllm-tunnel.log
```

---

## 七、调优迭代历程（三轮）

整个部署不是一次成型的，从首次启动到 OpenDB 跑通经历了三轮配置调整。每轮都因为遇到具体错误才发现下一个该改的参数。**复现部署直接抄第八节最终参数即可，本节是给后人理解每个参数为什么必须这么设。**

### 第 1 轮：首次启动 → Mamba 架构限制

**最初的启动命令**：
```bash
python -m vllm.entrypoints.openai.api_server \
  --model /root/autodl-tmp/Qwen3.5-27B-Opus-Distilled \
  --served-model-name qwen3.5-27b-opus \
  --dtype bfloat16 \
  --max-model-len 32768 \
  --gpu-memory-utilization 0.92 \
  --host 0.0.0.0 --port 8000 \
  --trust-remote-code
```

**报错**（启动 ~3 分钟后）：
```
ValueError: max_num_seqs (1024) exceeds available Mamba cache blocks (348). 
Each decode sequence requires one Mamba cache block, so CUDA graph capture cannot proceed. 
Please lower max_num_seqs to at most 348 or increase gpu_memory_utilization.
```

**发现**：模型 architecture 显示为 `Qwen3_5ForConditionalGeneration`，加载日志中可见 `mamba_mixer2`、`gdn_attention_core` 等算子 — 这是 **Mamba + Attention 混合架构**，不是纯 Transformer。Mamba SSM 层每个解码序列需要独占一个 cache 块，而 vLLM 默认 `max_num_seqs=1024` 远超 348 上限。

**修复**：增加 `--max-num-seqs 256`（实际后续降到 64，单用户场景足够）

### 第 2 轮：vLLM 上线 → OpenDB 调用报 400（tool_choice）

**第 1 轮修完后服务起来了**，本地 curl 直接调 `/v1/chat/completions` 都 OK，模型推理正常（28 tok/s）。

**OpenDB 端 `/diag` 立刻报错**：
```
auto diagnosis failed: openai Model 不可用
HTTP 400: "auto" tool choice requires --enable-auto-tool-choice 
and --tool-call-parser to be set
```

**根因**：OpenDB 诊断流程要求 LLM 调用 SQL 工具（`tool_choice: "auto"`），vLLM 默认不开启工具调用功能。

**修复**：启动参数加：
```
--enable-auto-tool-choice
--tool-call-parser hermes
--reasoning-parser deepseek_r1
```

**两个 parser 选择理由**：
- `tool-call-parser hermes`：Qwen 系列模型用 Hermes 风格 `<tool_call>...</tool_call>` 输出工具调用，vLLM 对应解析器即 `hermes`
- `reasoning-parser deepseek_r1`：模型会输出 `<think>...</think>` CoT 标签（Opus 蒸馏特征），用 `deepseek_r1` parser 让 vLLM 把推理拆到 `reasoning` 字段，content 字段就是干净的最终答案

### 第 3 轮：tool-calling OK → 又报 400（context length）

**第 2 轮修完后 OpenDB 不再报 tool_choice 错**，模型也开始正常输出推理。

**但下一秒又报新的 400**：
```
auto diagnosis failed: HTTP 400: This model's maximum context length is 32768 tokens. 
However, you requested 8000 output tokens and your prompt contains at least 24769 input tokens, 
for a total of at least 32769 tokens.
```

**分析**：
- OpenDB 单次 diag prompt 已 24769 tokens（包含数据库 schema + sentinel 数据 + 历史对话）
- OpenDB 默认要 8000 tokens 输出
- 24769 + 8000 = 32769，**恰好超过 32K 1 个 token**

**修复**：检查模型 config.json 发现 `max_position_embeddings: 262144`（256K 原生上下文），把 vLLM 的 `--max-model-len` 从 32768 提到 65536（64K，平衡 KV cache 显存）。

### 第 3.5 轮：重启时遇到 GPU 孤儿进程

**修第 3 轮要重启 vLLM**，pkill 后启动新实例报：
```
ValueError: Free memory on device cuda:0 (5.45/79.25 GiB) on startup is less than 
desired GPU memory utilization (0.93, 73.7 GiB).
```

**根因**：vLLM 的 `EngineCore` 是 `multiprocessing` 启动的子进程，pkill 父进程后 EngineCore 孤儿化（PPID=1）继续占 75GB GPU。

**修复**（已纳入 redeploy.sh）：
```bash
nvidia-smi --query-compute-apps=pid --format=csv,noheader  # 找出占 GPU 的 PID
kill -9 <PID>                                              # 显式杀
```

### 第 4 轮：性能优化 — prefix caching

**第 3 轮上线后 OpenDB 跑通**，但单次 sqltune 诊断耗时 **~6-8 分钟**，太长。分析时间分布发现：

| 阶段 | 耗时 |
|------|------|
| 第 1 轮 LLM 调用（24K 输入 prefill + 推理）| ~60-90s |
| 第 2 轮 LLM 调用（含上一轮上下文，仍要重新 prefill）| ~70-100s |
| 第 3 轮 LLM 调用（综合分析，prefill 又一次）| ~80-120s |
| 最终输出生成（~3500 tokens × 1/28s/tok）| ~125s |

**核心浪费**：每轮 LLM 调用的前缀（system + 已诊断历史 + 表结构 schema）都是一样的，但 vLLM 默认每次都从头 prefill。

**修复**：启动参数加 `--enable-prefix-caching`

vLLM 启动时会打印：
```
Warning: Prefix caching in Mamba cache 'align' mode is currently enabled. 
Its support for Mamba layers is experimental.
```

**实际效果**（Mamba 混合架构）：
- ✅ Attention 层 KV：正常缓存命中
- ⚠️ Mamba 层 state：仍要重算（无法前缀复用）
- 📉 加速效果：纯 Transformer 模型可达 80%，**Mamba 混合模型大约 30-50%**
- ✅ 输出质量：**完全不受影响**（KV cache 复用是数学等价）

**预期收益**：6 分钟 → 3-4 分钟。

### 第 5 轮（已知遗留）：tool-call JSON 偶发截断

**第 3 轮后 OpenDB 不再因为 vLLM 配置问题报错**，但实际 `/diag` 时观察到模型偶尔输出：
```
<tool_call>
{"name": "execute_sql", "arguments": {"sql": "SELECT * FROM v$version;"}
</tool_call>
```

外层 JSON 的闭合 `}` 缺失 → hermes parser 解析失败 → `tool_calls: []`。

**根因**：模型 SFT 数据是 **纯推理 CoT 蒸馏**（来自 Opus 4.6 推理轨迹），**不含 tool-calling 训练样本**。基础 Qwen 的工具调用能力在蒸馏过程中被弱化。

**当前状态**：未彻底修复，OpenDB 配置 `strip_think: true` 部分缓解。可选改进路径：
- `--tool-call-parser pythonic`（更宽容的语法）
- 强制 guided JSON decoding（牺牲速度换准确性）
- 接受局限：用此模型做 `/chat`、`/explain`，`/diag` 切回 deepseek-v4-pro

### 五轮迭代总结

| 轮次 | 问题 | 触发场景 | 修复参数 |
|------|------|---------|---------|
| 1 | Mamba 架构限制 | vLLM 启动 | `--max-num-seqs 64` |
| 2 | Tool calling 未启 | OpenDB /diag 调用 | `--enable-auto-tool-choice` `--tool-call-parser hermes` `--reasoning-parser deepseek_r1` |
| 3 | 上下文不够 | OpenDB 大 prompt | `--max-model-len 65536` |
| 3.5 | GPU 孤儿进程 | 重启 vLLM | nvidia-smi 找 PID + kill -9 |
| 4 | 性能慢（6-8 分钟）| sqltune 多轮 LLM 调用 | `--enable-prefix-caching`（实测降至 3-4 分钟） |
| 5 | Tool-call JSON 截断 | 模型本身 | 暂未修，已知局限 |

**经验**：vLLM 的"能跑通"和"OpenDB 能用 + 速度可接受"差五轮配置。每个 LLM 应用栈的需求不一样，部署一次新模型/新服务时建议预留 2-3 小时调试。

---

## 八、踩坑记录

### 1. 容器内 Python 3.6 装不了 huggingface-cli
**现象**：`from dataclasses import dataclass` ModuleNotFoundError
**根因**：CentOS 7 自带 Python 3.6，缺 dataclasses（Py 3.7+）
**修法**：直接 wget -c 用 hf-mirror，绕过 huggingface-cli

### 2. 跨云 rsync 极慢（~100 KB/s）
**现象**：从测试机（阿里云）到 AutoDL 速度只有 100 KB/s，预计 100+ 小时
**根因**：跨数据中心带宽限速 / SSH 加密开销
**修法**：直接从 hf-mirror.com 下载（~22 MB/s，44 分钟搞定）

### 3. vLLM 启动失败：max_num_seqs (1024) > Mamba blocks (348)
**现象**：`ValueError: max_num_seqs (1024) exceeds available Mamba cache blocks (348)`
**根因**：模型是 **Mamba+Attention 混合架构**，每个解码序列需独占 Mamba cache 块
**修法**：`--max-num-seqs 64`（远小于 348 限制，单用户场景够用）

### 4. OpenDB 报 400：tool_choice "auto" 缺参数
**现象**：`"auto" tool choice requires --enable-auto-tool-choice and --tool-call-parser`
**根因**：vLLM 默认不开 tool calling
**修法**：`--enable-auto-tool-choice --tool-call-parser hermes`

### 5. OpenDB 报 400：context length exceeded
**现象**：`maximum context length is 32768. you requested 8000 output + 24769 input = 32769`
**根因**：OpenDB diag 单次 prompt 太长（包含表结构 + 上下文 + 历史）
**修法**：`--max-model-len 65536`（模型原生 256K，提到 64K 安全）

### 6. pkill vLLM 后 GPU 不释放（孤儿进程）
**现象**：杀掉父进程后 nvidia-smi 仍显示 75GB 占用，新 vLLM 启动报 OOM
**根因**：vLLM EngineCore 子进程在父进程死后变成孤儿（PPID=1）继续占 GPU
**修法**：每次重启前 `nvidia-smi --query-compute-apps=pid` 找 PID 显式 kill

### 7. SSH 隧道闲置断连
**现象**：长时间不发请求，下一次 OpenDB 调用报 connection refused
**根因**：中间网络层关闭闲置 TCP 连接
**修法**：autossh + launchd + ServerAliveInterval=30

### 8. Tool call JSON 偶发截断（已知未修）
**现象**：模型输出 `<tool_call>{"name":"...","arguments":{"sql":"..."}` 缺最后 `}`
**根因**：Opus 蒸馏 SFT 数据是纯推理 CoT，不含 tool-calling 训练样本
**临时方案**：`strip_think: true` 让 OpenDB 容错处理；偶发不影响主流程
**长期方案**：换 `pythonic` parser 或 fine-tune 加入 tool-calling 数据

---

## 八、性能数据

| 指标 | 实测值 |
|------|--------|
| 模型加载时间（冷启动）| 13.7 秒（51.1 GiB 权重）|
| torch.compile 编译时间 | 52.75 秒 |
| Initial profiling/warmup | 80.29 秒 |
| **总冷启动时间** | **~6-9 分钟** |
| 二次启动（编译缓存命中）| ~3 分钟 |
| 推理速度（单用户）| ~28 tok/s |
| GPU 占用 | 75 GB / 80 GB（93%）|
| 系统 RAM 占用 | ~3 GB |

---

## 九、成本估算

| 项 | 月费 |
|----|------|
| AutoDL A800 80GB（按量 ¥6/h × 24h × 30 天）| ¥4320 |
| AutoDL A800 80GB（关机时只付存储 ¥0.6/GB/月 × 90GB）| ¥54 |
| 数据盘扩容（额外 40GB）| ¥12 |
| **持续运行**：~¥4380/月 |
| **按需启停**：~¥66/月 + 按量计费 |

对比阿里云 GT4.LARGE96（A100 80GB）¥18000+/月 — **AutoDL 便宜 4-5 倍**。

---

## 十、未来扩展

1. **A/B 对比 benchmark**：用 OpenDB 同一故障场景跑 `qwen-opus-local` vs `deepseek-v4-pro` vs `glm-5.1`，对比诊断质量
2. **量化版本**：换 GPTQ-Int4 或 AWQ-Int4 把显存占用降到 16GB，可以同时跑 27B + 9B
3. **多 GPU TP**：升级到双卡部署 70B 模型（A800 80GB × 2 ≈ ¥12/h）
4. **GGUF 转换**：转 GGUF 后能在 Ollama / LM Studio 等更轻量框架上跑
5. **Production 化**：迁到阿里云包年实例 + 加 Nginx 反代 + JWT 鉴权

---

## 附录：完整启动命令一键脚本

放在 AutoDL 上 `/root/autodl-tmp/redeploy.sh`：

```bash
#!/bin/bash
# 一键全清重启 vLLM
set -e

echo "[1/4] 杀干净..."
pkill -9 -f 'start-vllm.sh|vllm.entrypoints|EngineCore' 2>/dev/null
sleep 5

echo "[2/4] 等 GPU 释放..."
for i in $(seq 1 12); do
  used=$(nvidia-smi --query-gpu=memory.used --format=csv,noheader,nounits)
  [ "$used" -lt 1000 ] && break
  pid=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | head -1)
  [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null
  sleep 5
done

echo "[3/4] 启动 vLLM..."
nohup /root/autodl-tmp/start-vllm.sh > /root/autodl-tmp/logs/vllm.log 2>&1 &
echo "PID: $!"

echo "[4/4] 等 API ready..."
until curl -sf http://127.0.0.1:8000/v1/models >/dev/null 2>&1; do
  sleep 30
  echo "$(date +%T) loading..."
done
echo "✅ ready"
```

```bash
chmod +x /root/autodl-tmp/redeploy.sh
/root/autodl-tmp/redeploy.sh
```

---

**部署人**：Yingjie Wang
**最后更新**：2026-04-29
