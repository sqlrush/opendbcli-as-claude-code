# OpenDB 部署指南

本文档覆盖 OpenDB Autopilot 集群的部署、升级和回滚。

---

## 目录

- [前置条件](#前置条件)
- [部署方式一：Pull 安装](#部署方式一pull-安装)
- [部署方式二：SSH 批量部署](#部署方式二ssh-批量部署)
- [部署方式三：Docker 部署](#部署方式三docker-部署)
- [网络要求](#网络要求)
- [单机多角色部署](#单机多角色部署)
- [滚动升级](#滚动升级)
- [回滚](#回滚)
- [常见问题](#常见问题)

---

## 前置条件

### 编译环境

| 依赖 | 版本 | 说明 |
|------|------|------|
| Go | 1.21+ | 编译所需 |
| GCC | 任意 | CGO 依赖（Oracle 驱动） |
| golangci-lint | 最新 | 可选，代码检查 |

### 运行环境

| 依赖 | 说明 |
|------|------|
| 操作系统 | Linux (CentOS 7+, Ubuntu 18.04+, Debian 10+) 或 macOS |
| CPU | 最低 1 核（Worker），建议 2 核（Overlord/Cerebrate） |
| 内存 | 最低 512MB（Worker），建议 1GB（Overlord/Cerebrate） |
| 磁盘 | 最低 100MB（二进制 + 配置 + 日志） |
| 网络 | Agent 三层间需 gRPC 可达 |

### 编译

```bash
git clone https://github.com/sqlrush/opendb.git
cd opendb
go build -tags full -o opendb ./cmd/opendb/

# 验证
./opendb --version
```

> **必须使用 `-tags full`**，否则缺少数据库驱动链接。

### 交叉编译

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -tags full -o opendb-linux-amd64 ./cmd/opendb/

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -tags full -o opendb-linux-arm64 ./cmd/opendb/
```

---

## 部署方式一：Pull 安装

适用场景：目标节点不开 SSH，Agent 主动从 Cerebrate 拉取二进制。

### 原理

Cerebrate 启动时在 Web 端口（默认 8080）提供 HTTP 文件服务。新节点通过 curl 下载二进制，然后手动执行 `cluster join`。

### 步骤

**1. 在 Cerebrate 节点**

确保 Cerebrate 已初始化并启动，且 `--web` 参数已设置：

```bash
opendb cluster init --role manager --listen 0.0.0.0:9100 --web 0.0.0.0:8080
opendb agent start --role manager --web 0.0.0.0:8080
```

将编译好的二进制放到 Cerebrate 的 Pull 下载目录。

**2. 在新节点上**

```bash
# 下载二进制
curl -fsSL http://cerebrate-host:8080/pull/opendb -o /usr/local/bin/opendb
chmod +x /usr/local/bin/opendb

# 验证
opendb --version

# 加入集群（以 Worker 为例）
opendb cluster join --role worker \
  --overlord 192.168.1.20:9200 \
  --token <token>

# 启动 Agent
opendb agent start --role worker
```

### 优势

- 不需要开放 SSH（端口 22）
- 新节点自主拉取，适合安全要求高的环境

---

## 部署方式二：SSH 批量部署

适用场景：有 SSH 访问权限，一次性部署大规模集群。

### 准备 inventory.yaml

完整示例：3 个区域，6 个 Overlord，600 个 Worker。

```yaml
# inventory.yaml -- 大规模集群部署清单
ssh_user: opendb
ssh_key: ~/.ssh/id_rsa_opendb

# ── Cerebrate（全局管理节点）──
cerebrate:
  host: 10.0.0.10
  name: cerebrate-main
  listen: "0.0.0.0:9100"
  web: "0.0.0.0:8080"

# ── Overlord（区域协调节点，每区域至少 3 个保证 HA）──
overlords:
  # 华东区（200 Workers）
  - host: 10.1.0.10
    name: overlord-east-1
    region: china-east
    listen: "0.0.0.0:9200"
  - host: 10.1.0.11
    name: overlord-east-2
    region: china-east
    listen: "0.0.0.0:9200"

  # 华南区（200 Workers）
  - host: 10.2.0.10
    name: overlord-south-1
    region: china-south
    listen: "0.0.0.0:9200"
  - host: 10.2.0.11
    name: overlord-south-2
    region: china-south
    listen: "0.0.0.0:9200"

  # 华北区（200 Workers）
  - host: 10.3.0.10
    name: overlord-north-1
    region: china-north
    listen: "0.0.0.0:9200"
  - host: 10.3.0.11
    name: overlord-north-2
    region: china-north
    listen: "0.0.0.0:9200"

# ── Drone（工作节点）──
drones:
  # 华东区 Workers -- Oracle
  - host: 10.1.1.1
    name: worker-east-oracle-001
    overlord: 10.1.0.10:9200
    db_type: oracle
    db_conn: "system/pass@localhost:1521/ORCL"
  - host: 10.1.1.2
    name: worker-east-oracle-002
    overlord: 10.1.0.10:9200
    db_type: oracle
    db_conn: "system/pass@localhost:1521/ORCL"
  # ... 更多 Worker（每区域最多 200 个）

  # 华南区 Workers -- MySQL
  - host: 10.2.1.1
    name: worker-south-mysql-001
    overlord: 10.2.0.10:9200
    db_type: mysql
  - host: 10.2.1.2
    name: worker-south-mysql-002
    overlord: 10.2.0.10:9200
    db_type: mysql
  # ...

  # 华北区 Workers -- PostgreSQL
  - host: 10.3.1.1
    name: worker-north-pg-001
    overlord: 10.3.0.10:9200
    db_type: postgres
  - host: 10.3.1.2
    name: worker-north-pg-002
    overlord: 10.3.0.10:9200
    db_type: postgres
  # ...
```

### 执行部署

```bash
opendb cluster deploy \
  --inventory inventory.yaml \
  --binary ./opendb-linux-amd64 \
  --batch-size 50
```

### 部署流程

```
Phase 1: 部署 Cerebrate (1 节点)
  SCP 二进制 -> chmod +x -> cluster init
  |
Phase 2: 部署 Overlords (并行，6 节点)
  SCP 二进制 -> chmod +x -> cluster join --role memory
  |
Phase 3: 部署 Workers (分批，每批 50)
  Batch 1-50:   SCP -> join --role worker
  Batch 51-100: SCP -> join --role worker
  ...（间隔 10 秒）
  Batch 551-600: SCP -> join --role worker
```

### 部署结果

```
Phase 1: Deploying Cerebrate...
Phase 2: Deploying 6 Overlords...
Phase 3: Deploying 600 Workers (batch=50)...
  Batch 1-50...
  Batch 51-100...
  ...

Deployment complete: 607 success, 0 failed (total 607)
```

### SSH 要求

- 所有节点需 SSH 可达（端口 22）
- 统一的 SSH 用户和密钥
- SSH 用户需要有 `/usr/local/bin/` 写权限（或使用 sudo）

---

## 部署方式三：Docker 部署

适用场景：开发/测试环境快速验证。

### 单机完整集群（1 Cerebrate + 2 Overlord + 10 Worker）

```yaml
# docker-compose.yaml
version: "3.8"

services:
  cerebrate:
    image: opendb:latest
    command: >
      sh -c "opendb cluster init --role manager --listen 0.0.0.0:9100 --web 0.0.0.0:8080 &&
             opendb agent start --role manager --web 0.0.0.0:8080"
    ports:
      - "9100:9100"
      - "8080:8080"
    environment:
      - OPENDB_HOME=/data/opendb
    volumes:
      - cerebrate-data:/data/opendb

  overlord-1:
    image: opendb:latest
    command: >
      sh -c "opendb cluster join --role memory --cerebrate cerebrate:9100 --token auto --region east --listen 0.0.0.0:9200 &&
             opendb agent start --role memory --listen 0.0.0.0:9200"
    environment:
      - OPENDB_HOME=/data/opendb
    depends_on:
      - cerebrate
    volumes:
      - overlord1-data:/data/opendb

  overlord-2:
    image: opendb:latest
    command: >
      sh -c "opendb cluster join --role memory --cerebrate cerebrate:9100 --token auto --region west --listen 0.0.0.0:9200 &&
             opendb agent start --role memory --listen 0.0.0.0:9200"
    environment:
      - OPENDB_HOME=/data/opendb
    depends_on:
      - cerebrate
    volumes:
      - overlord2-data:/data/opendb

  worker:
    image: opendb:latest
    command: >
      sh -c "opendb cluster join --role worker --overlord overlord-1:9200 --token auto &&
             opendb agent start --role worker"
    environment:
      - OPENDB_HOME=/data/opendb
    depends_on:
      - overlord-1
    deploy:
      replicas: 10
    volumes:
      - worker-data:/data/opendb

volumes:
  cerebrate-data:
  overlord1-data:
  overlord2-data:
  worker-data:
```

### 构建 Docker 镜像

```dockerfile
# Dockerfile
FROM golang:1.21 AS builder
WORKDIR /src
COPY . .
RUN go build -tags full -o /opendb ./cmd/opendb/

FROM debian:bookworm-slim
COPY --from=builder /opendb /usr/local/bin/opendb
RUN chmod +x /usr/local/bin/opendb
ENTRYPOINT ["opendb"]
```

```bash
docker build -t opendb:latest .
docker-compose up -d
```

### 验证

```bash
# 查看集群状态
curl http://localhost:8080/api/fleet | jq .

# 查看健康状态
curl http://localhost:8080/api/health | jq .

# 查看全局拓扑
curl http://localhost:8080/api/topology | jq .
```

---

## 网络要求

### 端口规划

| 角色 | 端口 | 协议 | 说明 |
|------|------|------|------|
| Cerebrate | 9100 | gRPC (TCP) | Overlord 注册和心跳 |
| Cerebrate | 8080 | HTTP | Web 大盘 + Pull 下载 |
| Overlord | 9200 | gRPC (TCP) | Worker 注册和心跳 |
| Worker | 9300 | gRPC (TCP) | CLI 通信 + 接收命令 |

### 通信方向

```
Worker (9300) ──── gRPC ────> Overlord (9200) ──── gRPC ────> Cerebrate (9100)
                                                              Cerebrate (8080) <── HTTP ── Browser
```

- Worker 主动连接 Overlord（出站 TCP）
- Overlord 主动连接 Cerebrate（出站 TCP）
- 浏览器访问 Cerebrate Web 大盘（入站 HTTP）
- 心跳间隔：Worker -> Overlord 30 秒，Overlord -> Cerebrate 30 秒

### 防火墙规则

```bash
# Cerebrate 节点
firewall-cmd --add-port=9100/tcp --permanent  # gRPC
firewall-cmd --add-port=8080/tcp --permanent  # Web
firewall-cmd --reload

# Overlord 节点
firewall-cmd --add-port=9200/tcp --permanent
firewall-cmd --reload

# Worker 节点
firewall-cmd --add-port=9300/tcp --permanent
firewall-cmd --reload
```

---

## 单机多角色部署

通过 `OPENDB_HOME` 环境变量实现同一台机器运行多个角色，每个角色使用独立数据目录。

```bash
# 角色 1：Cerebrate
OPENDB_HOME=/opt/opendb/cerebrate opendb cluster init --role manager \
  --listen 0.0.0.0:9100 --web 0.0.0.0:8080
OPENDB_HOME=/opt/opendb/cerebrate opendb agent start --role manager \
  --web 0.0.0.0:8080

# 角色 2：Overlord
OPENDB_HOME=/opt/opendb/overlord opendb cluster join --role memory \
  --cerebrate 127.0.0.1:9100 --token <token> --region local \
  --listen 0.0.0.0:9200
OPENDB_HOME=/opt/opendb/overlord opendb agent start --role memory \
  --listen 0.0.0.0:9200

# 角色 3：Worker
OPENDB_HOME=/opt/opendb/worker opendb cluster join --role worker \
  --overlord 127.0.0.1:9200 --token <token> \
  --listen 0.0.0.0:9300
OPENDB_HOME=/opt/opendb/worker opendb agent start --role worker
```

**注意事项：**

- 每个角色必须使用不同的 `OPENDB_HOME`
- 每个角色必须使用不同的 `--listen` 端口
- 数据目录结构完全独立：配置、日志、记忆、报告互不干扰
- 适合开发测试或小规模验证

---

## 滚动升级

升级通过 gRPC 层级分发新二进制。顺序：Worker -> Overlord -> Cerebrate（最后）。

### 升级流程

```
                    准备新版本二进制
                         |
                         v
        Phase 1: 升级 Workers（分批，每批 50 个）
          ┌──────────────────────────────────────┐
          │ 接收二进制 -> 校验 SHA256 -> 备份旧版本 │
          │ -> 替换二进制 -> 重启 -> 健康检查       │
          └──────────────────────────────────────┘
                         |
                         v
        Phase 2: 升级 Overlords（逐个）
          同上流程，确保每个 Overlord 升级完成后
          其管辖的 Workers 正常通信
                         |
                         v
        Phase 3: 升级 Cerebrate（最后）
          同上流程
```

### 执行

```bash
# 准备新版本
go build -tags full -o opendb-new ./cmd/opendb/

# 查看升级计划（不执行）
opendb cluster upgrade --binary opendb-new --dry-run

# 执行滚动升级
opendb cluster upgrade --binary opendb-new --batch-size 50
```

### 升级安全机制

- **SHA-256 校验**：传输后校验二进制完整性，不匹配则拒绝
- **备份旧版本**：替换前将旧二进制保存为 `.bak`
- **兼容性**：新版必须兼容旧版（硬性策略），滚动过程中新旧版本共存

---

## 回滚

回滚顺序与升级相反：Cerebrate -> Overlord -> Worker。

### 手动回滚

```bash
# 在每个节点上
opendb agent stop

# 恢复旧版本二进制
mv /usr/local/bin/opendb.bak /usr/local/bin/opendb

# 重启
opendb agent start --role <role>
```

### 回滚顺序

```
Phase 1: 回滚 Cerebrate
  停止 -> 恢复旧二进制 -> 启动 -> 验证
         |
Phase 2: 回滚 Overlords（逐个）
  停止 -> 恢复 -> 启动 -> 验证 Worker 重连
         |
Phase 3: 回滚 Workers（分批）
  停止 -> 恢复 -> 启动 -> 验证心跳
```

### 注意事项

- 回滚前确认新版本的数据格式没有不兼容的变更
- Overlord 回滚后，其管辖的 Worker 会自动重连
- 集群配置（`~/.opendb/cluster/config.yaml`）不需要修改

---

## 常见问题

### 1. cluster init 报错 "cluster already initialized"

节点已加入集群。查看当前状态：

```bash
opendb cluster status
```

如需重新初始化，删除集群配置目录：

```bash
rm -rf ~/.opendb/cluster/
opendb cluster init --role manager ...
```

### 2. Worker 无法连接 Overlord

**检查步骤：**

```bash
# 1. 确认 Overlord 正在运行
opendb agent status

# 2. 确认网络可达
telnet <overlord-host> 9200

# 3. 确认防火墙允许 9200 端口
firewall-cmd --list-ports

# 4. 检查 Worker 配置
cat ~/.opendb/cluster/config.yaml
# 确认 overlord 地址正确
```

### 3. Web 大盘显示 0 个 Worker

**可能原因：**

- Overlord 心跳未到达 Cerebrate：检查 Overlord 的 `cerebrate` 地址配置
- Worker 未注册到 Overlord：检查 Worker 日志中是否有注册成功信息
- 缓存延迟：新 Worker 注册后最多 30 秒内反映到大盘

**排查方法：**

```bash
# 查看 Cerebrate 日志
docker logs cerebrate | grep heartbeat

# 查看 Overlord 日志
docker logs overlord-1 | grep "Worker registration"

# 直接调用 API 确认
curl http://cerebrate-host:8080/api/fleet | jq .
```

### 4. LLM 诊断超时

**检查步骤：**

```bash
# 1. 确认 LLM 服务可达
curl http://localhost:11434/api/tags

# 2. 确认 config.yaml 中 LLM 配置正确
cat ~/.opendb/config.yaml | grep -A5 "llm:"

# 3. LLM 不可用时会自动降级到 Rule Engine（273+ 规则）
# 查看日志确认降级行为
grep "Rule Engine fallback" ~/.opendb/logs/*.log
```

### 5. 审计日志校验失败

```bash
# 运行审计验证
opendb audit verify

# 如果报告篡改，检查：
# 1. 审计密钥文件权限是否正确
ls -la ~/.opendb/audit.key
# 应为 -rw------- (0600)

# 2. 是否有人手动编辑了 audit.log
# 链式哈希一旦断裂，后续所有条目都会校验失败
```

### 6. Agent 启动后立即退出

**检查步骤：**

```bash
# 1. 查看 PID 文件是否残留
ls ~/.opendb/agent-*.pid

# 2. 如果有残留的 PID 文件但进程不存在，手动删除
rm ~/.opendb/agent-worker.pid

# 3. 重新启动
opendb agent start --role worker
```

### 7. 数据库连接失败但 Agent 继续运行

这是设计行为。Worker 在数据库连接失败时退化为心跳模式：
- 仍保持与 Overlord 的 gRPC 连接
- 不运行 Sentinel 监控
- 定期重试数据库连接

检查数据库连接配置：

```bash
cat ~/.opendb/connections/*.yaml
```

### 8. 多个 Worker 连接同一个数据库

每个 Worker 对应一个数据库实例。如果多个 Worker 连接同一个实例：
- 只有一个 Worker 运行 Sentinel 监控
- 其余 Worker 作为备份，主 Worker 失联后自动接管

### 9. OPENDB_HOME 环境变量不生效

确认设置方式：

```bash
# 正确：命令前设置
OPENDB_HOME=/opt/opendb/worker opendb agent start --role worker

# 或 export 后执行
export OPENDB_HOME=/opt/opendb/worker
opendb agent start --role worker
```

### 10. 内存占用过高

Overlord 的内存占用与管辖的 Worker 数量成正比。每个 Worker 的状态缓存约占 1MB。建议：
- 每个 Overlord 管辖不超过 200 个 Worker
- Cerebrate 内存缓存最近 1000 份报告
- 超过限制时增加 Overlord 数量，分担负载
