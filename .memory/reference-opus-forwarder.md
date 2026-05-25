---
name: Opus转发器连接方式
description: 测试服务器的LLM连接是通过SSH隧道转发到本机Opus 4.6，不是在服务器上装Ollama
type: reference
---

测试服务器（8.160.176.23）的 LLM 连接方式：

- opendb 配置的 `localhost:11434` 不是 Ollama，是 **Opus 4.6 转发器**
- 通过 SSH 隧道将本机 11434 端口转发到测试服务器
- 隧道命令：`ssh -R 11434:localhost:11434 root@8.160.176.23`
- **绝对不要在测试服务器上安装 Ollama**
- LLM 连不上时 = SSH 隧道断了，需要用户重建隧道
