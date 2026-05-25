---
name: Skill命令非阻塞异步执行
description: 所有skill命令应异步执行，不阻塞输入，结果异步显示在聊天记录窗口
type: project
---

所有 skill 命令（如 `/resize`, `/kill`, `/alter` 等）应改为非阻塞异步执行。

**当前问题:** 执行 `/resize SYSAUX add 512M` 时，输入口被阻塞，用户必须等待 Oracle 执行完毕才能继续操作。

**目标行为:**
1. 用户输入 `/resize SYSAUX add 512M`，命令立即显示在聊天记录窗口
2. 光标立即回到命令输入窗口，用户可以继续输入下一条命令
3. `/resize` 的执行结果异步显示在聊天记录窗口（类似 `/diag` 的异步模式）

**Why:** 与 Claude Code 的交互风格一致 — 命令提交后立即释放输入，结果异步回显。DBA 经常需要连续快速执行多条命令，阻塞等待严重影响效率。

**How to apply:** 参考 `/diag` 的异步执行模式（`startDiagAsync` + `diagCh` + `renderDiagProgress`），将通用 skill 执行也改为异步。优先级待定。
