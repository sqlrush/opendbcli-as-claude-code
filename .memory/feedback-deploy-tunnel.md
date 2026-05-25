---
name: deploy-check-tunnel
description: Every deployment must verify SSH tunnel for opus-forwarder is alive; re-establish if down
type: feedback
---

## 部署时必须检查 SSH 隧道

每次部署到 Oracle 测试服务器后，必须：

1. **检查隧道是否存活**:
   ```bash
   ssh root@8.160.176.23 "ss -tlnp | grep 11434"
   ```

2. **如果端口无人监听或测试超时，重建隧道**:
   ```bash
   # 远程清理旧绑定
   ssh root@8.160.176.23 "fuser -k 11434/tcp 2>/dev/null"
   # 本地清理旧隧道
   pkill -f "ssh -f -N -R 11434"
   sleep 1
   # 建立新隧道（带 keepalive 防断）
   ssh -f -N -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -R 11434:localhost:11434 root@8.160.176.23
   ```

3. **验证可用**:
   ```bash
   ssh root@8.160.176.23 "timeout 60 curl -s --connect-timeout 5 --max-time 55 http://127.0.0.1:11434/v1/chat/completions -X POST -H 'Content-Type: application/json' -d '{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":5}'"
   ```

### 常见问题
- **本地 HTTP proxy** (`http_proxy=http://127.0.0.1:7990`): 用 `--noproxy localhost` 或 `NO_PROXY='*'`
- **旧 sshd 进程占端口**: 远程 `fuser -k 11434/tcp` 清理
- **隧道频繁断开**: 加 `ServerAliveInterval=30 -o ServerAliveCountMax=3`
