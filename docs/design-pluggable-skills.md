# DBAA 可插拔式 Skill 设计方案

状态: draft  
目标版本: v1.2.31 后续增量  
范围: P0 External Script Skill + P1 MCP Skill Adapter  
暂不包含: P2 Plugin 包管理、签名分发市场、远程安装中心

## 1. 背景与目标

DBAA 当前的 skill 主要由 Go 代码实现。客户如果要增加现场专用能力，通常需要：

- 理解 DBAA 内部 Go 接口；
- 修改源码；
- 重新编译；
- 重新发布二进制。

这不适合客户现场快速扩展。例如客户希望接入自有 CMDB、备份平台、监控平台、巡检脚本、复制链路检查、合规检查、机房侧脚本时，不应该要求客户改 DBAA 源码。

本方案目标是提供一套可插拔 skill 框架，使客户通过 `skill.md + shell/python 脚本` 或 MCP server，就能扩展 DBAA 能力，并让这些能力自动接入：

- slash 命令；
- LLM native function calling；
- PromptToolAdapter；
- intent router 可选触发；
- security guard；
- diagtrace / audit；
- golden / doctor 验证。

核心原则:

```text
Go 内置 skill
External Script skill
MCP tool skill
未来 Plugin skill
        ↓
统一适配为 skill.Skill
        ↓
DBAA Registry / Executor / Security Guard / Trace
        ↓
slash command / native FC / PromptToolAdapter / router
```

也就是说，外部 skill 不应该绕过 DBAA 的执行器直接运行，而是必须先被适配成现有 `skill.Skill`。

## 2. 非目标

本方案不是让 LLM 任意执行 shell。

不做以下事情：

- 不提供自由 shell 执行入口；
- 不允许 LLM 拼接任意系统命令；
- 不把数据库密码默认传给外部脚本；
- 不默认加载所有 MCP tools；
- 不默认允许外部 skill 覆盖内置 skill；
- 不承诺客户脚本本身是安全的；
- P0/P1 不做 plugin 安装、卸载、签名、版本市场。

产品边界应明确表述为：

> DBAA 支持客户通过标准 manifest 注册受控外部 skill。LLM 只能调用已注册、已声明参数 schema、已设置安全等级、受 DBAA 权限与审计控制的外部 skill。

## 3. 现有接口基础

当前 DBAA 的 skill 接口位于 `internal/skill/skill.go`：

```go
type Skill interface {
    Name() string
    Description() string
    ToolDef() ToolDef
    CLIDef() CLIDef
    Validate(params Params) error
    Execute(ctx context.Context, params Params) (*Result, error)
    SecurityLevel() SecurityLevel
}
```

这个接口已经足够作为统一适配层。新增能力不改接口，而是新增实现：

```go
type ExternalScriptSkill struct { ... } // implements skill.Skill
type MCPSkill struct { ... }            // implements skill.Skill
```

注册后，现有 executor 流程保持不变：

```text
Registry.Get(name)
        ↓
Validate(params)
        ↓
SecurityGuard.Authorize(level, name)
        ↓
Skill.Execute(ctx, params)
```

## 4. 总体模块设计

新增包建议：

```text
internal/skill/external/
  manifest.go          # skill.md frontmatter schema
  loader.go            # 扫描目录并加载 external skills
  script_skill.go      # ExternalScriptSkill 实现 skill.Skill
  script_runner.go     # exec.CommandContext + stdin/stdout/stderr/timeout
  output.go            # 文本/JSON 输出解析
  security.go          # 路径、权限、覆盖、环境变量校验
  mcp_config.go        # MCP server 配置结构
  mcp_loader.go        # 加载 MCP server tool schema
  mcp_skill.go         # MCPSkill 实现 skill.Skill
  audit.go             # 外部 skill 调用摘要
```

共享命令建议：

```text
internal/skill/builtin/shared/skills.go
```

提供：

```text
/skills list
/skills show <name>
/skills doctor
/skills reload
/skills init <name>
/skills run <name> '{"param":"value"}'
```

配置扩展建议：

```text
internal/config/config.go
```

新增：

```yaml
external_skills:
  enabled: true
  dirs:
    - ~/.opendb/skills
    - /etc/dbaa/skills
    - ./.opendb/skills
  allow_override_builtin: false
  max_timeout: 60s
  max_output_bytes: 262144
  max_stderr_bytes: 65536
  inherit_env: false
  env_allowlist:
    - PATH
    - LANG
  expose_db_credentials: false

mcp:
  enabled: false
  servers: []
```

## 5. 发现与加载机制

### 5.1 搜索目录

建议支持三层目录：

```text
/etc/dbaa/skills/              # 企业/客户全局 skill
~/.opendb/skills/              # 当前用户 skill
./.opendb/skills/              # 当前项目/现场目录 skill
```

优先级从低到高：

```text
/etc/dbaa/skills
~/.opendb/skills
./.opendb/skills
```

默认不允许覆盖内置 skill。外部 skill 之间如果同名：

- 默认报冲突；
- 或按更高优先级目录覆盖，但必须在 `/skills doctor` 中明确显示覆盖关系；
- 初版建议冲突即拒绝，避免现场不可预期。

### 5.2 目录结构

每个 skill 一个目录：

```text
~/.opendb/skills/
  check_replication_lag/
    skill.md
    run.sh

  customer_backup_check/
    SKILL.md
    main.py
```

`skill.md` 和 `SKILL.md` 都支持：

- `skill.md`: DBAA 推荐命名；
- `SKILL.md`: 兼容 Claude Code 风格，降低迁移成本。

同一目录若两者都存在，优先 `skill.md`，并在 doctor 中提示。

### 5.3 加载流程

```text
DBAA 启动 / /skills reload
        ↓
扫描 external_skills.dirs
        ↓
发现 skill.md / SKILL.md
        ↓
解析 YAML frontmatter
        ↓
校验 api_version / name / kind / db_types / security / command / parameters
        ↓
根据 kind 创建 ExternalScriptSkill 或 MCPSkill
        ↓
按 db_types 注册到 registry.RegisterForDB
        ↓
/skills list 和 LLM tools 可见
```

### 5.4 注册策略

如果 `db_types` 为空：

- 默认注册为 shared skill；
- 或拒绝加载，要求客户显式声明。

建议 P0 选择显式声明，避免外部 skill 被误暴露到所有数据库：

```yaml
db_types: [opengauss, gaussdb]
```

`db_types` 支持：

```text
opengauss
gaussdb
postgres
mysql
oracle
dm
all
```

P0 可以先支持 `opengauss/gaussdb/all`，其他数据库后续扩展。

## 6. Manifest 标准

### 6.1 基础模板

```markdown
---
api_version: opendb.skill/v1
name: check_replication_lag
title: 检查 GaussDB 主备复制延迟
description: 检查客户自定义主备复制状态和延迟
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 30s
command: ./run.sh

parameters:
  type: object
  properties:
    threshold_seconds:
      type: integer
      description: 复制延迟告警阈值
      default: 60
  required: []

triggers:
  - 复制延迟
  - 主备同步
  - replication lag

tags:
  - replication
  - gaussdb
---

# 作用

检查客户现场自定义主备复制状态。

# 输出要求

必须输出：
- 当前复制状态
- 延迟秒数
- 是否超过阈值
- 建议动作
```

### 6.2 字段说明

| 字段 | 必填 | 说明 |
|---|---:|---|
| `api_version` | 是 | 当前为 `opendb.skill/v1` |
| `name` | 是 | skill 名，建议 snake_case，全局唯一 |
| `title` | 否 | 展示标题 |
| `description` | 是 | 给用户和 LLM 看的工具说明 |
| `kind` | 是 | `script` 或 `mcp` |
| `db_types` | 是 | 支持的数据库类型 |
| `security` | 是 | `read_only` / `operator` / `admin` |
| `timeout` | 否 | 单次执行超时，不得超过全局上限 |
| `command` | script 必填 | 脚本入口 |
| `parameters` | 是 | JSON Schema，转换为 ToolDef.Parameters |
| `triggers` | 否 | router 弱触发词 |
| `tags` | 否 | prompt mode scene filter / 分类 |
| `env` | 否 | skill 需要的额外环境变量 allowlist |
| `requires` | 否 | 特权能力声明，如 `db_credentials` |

### 6.3 name 规则

建议只允许：

```text
^[a-zA-Z][a-zA-Z0-9_]{1,63}$
```

不允许：

- 空格；
- `/`；
- `..`；
- shell 元字符；
- 与内置 skill 同名；
- 与其他外部 skill 同名。

## 7. P0: External Script Skill

### 7.1 标准调用协议

DBAA 执行脚本时，向 stdin 写入 JSON：

```json
{
  "api_version": "opendb.skill/v1",
  "skill": "check_replication_lag",
  "params": {
    "threshold_seconds": 60
  },
  "context": {
    "db_type": "gaussdb",
    "connection": "gauss_local",
    "database": "postgres",
    "host": "10.0.0.1",
    "port": 8000,
    "user": "omm",
    "user_question": "看下主备复制有没有延迟",
    "trace_id": "diag-20260525-xxxx"
  }
}
```

默认不传数据库密码。

### 7.2 脚本输出协议

支持纯文本：

```text
主备复制正常，当前延迟 3 秒，低于阈值 60 秒。
```

也支持 JSON：

```json
{
  "ok": true,
  "summary": "复制正常",
  "rendered": "主备复制正常，当前延迟 3 秒，低于阈值 60 秒。",
  "data": {
    "lag_seconds": 3,
    "threshold_seconds": 60,
    "status": "ok"
  },
  "metadata": {
    "source": "customer_script"
  }
}
```

DBAA 映射：

```go
&skill.Result{
    Type:     skill.ResultText,
    Rendered: output.Rendered,
    Summary:  output.Summary,
    Data:     output.Data,
    Metadata: output.Metadata,
}
```

如果脚本输出不是 JSON，则作为 `Rendered` 文本。

### 7.3 执行方式

必须使用：

```go
exec.CommandContext(ctx, absCommand, args...)
```

禁止：

```go
exec.Command("sh", "-c", userControlledCommand)
```

初版建议 `command` 只支持可执行文件路径，不支持任意 shell command 字符串。

允许：

```yaml
command: ./run.sh
```

谨慎支持：

```yaml
command:
  - python3
  - ./main.py
```

如果支持数组形式，需要校验：

- 第一个元素不能来自用户参数；
- 相对脚本路径必须在 skill 目录内；
- 不做 shell expansion；
- 不支持管道、重定向、子命令。

### 7.4 工作目录与环境变量

运行时：

- `cwd` 固定为 skill 目录；
- stdin 是 JSON；
- stdout/stderr 捕获；
- 无 TTY；
- 不继承完整环境变量；
- 默认只给 `PATH` 和 `LANG`，由配置 allowlist 控制。

建议注入的环境变量：

```text
OPENDB_SKILL_NAME
OPENDB_SKILL_DIR
OPENDB_DB_TYPE
OPENDB_CONNECTION
OPENDB_TRACE_ID
```

不建议注入：

```text
DB_PASSWORD
API_KEY
HOME 全量密钥路径
```

## 8. P1: MCP Skill Adapter

### 8.1 目标

客户可能已经有 MCP server 暴露内部平台能力，例如：

- CMDB 查询；
- 监控指标查询；
- 工单系统查询；
- 备份平台查询；
- 容器平台状态；
- 存储阵列状态。

DBAA 可以连接这些 MCP server，将 MCP tools 映射成 DBAA skills：

```text
MCP server tools/list
        ↓
DBAA MCP loader
        ↓
allowlist + overlay
        ↓
MCPSkill implements skill.Skill
        ↓
registry/executor/security/trace
```

### 8.2 配置示例

```yaml
mcp:
  enabled: true
  servers:
    - name: customer_cmdb
      command: /opt/customer/cmdb-mcp/server
      timeout: 20s
      tools:
        query_host:
          enabled: true
          name: cmdb_query_host
          description: 查询客户 CMDB 中主机归属和用途
          security: read_only
          db_types: [opengauss, gaussdb]
          triggers:
            - 主机归属
            - cmdb
            - 服务器用途
```

### 8.3 allowlist 原则

MCP 默认不暴露所有 tools。

每个 MCP tool 必须：

- 显式 allowlist；
- 显式声明 DBAA skill name；
- 显式声明 security；
- 显式声明 db_types；
- 设置 timeout；
- 可选设置输出大小限制。

这样避免客户 MCP server 上有高危 tool 被意外暴露给 LLM。

### 8.4 MCP 调用协议映射

MCP tool schema:

```text
name
description
inputSchema
```

映射为 DBAA:

```go
skill.ToolDef{
    Name:        overlay.Name,
    Description: overlay.Description,
    Parameters:  mcpTool.InputSchema,
}
```

执行：

```text
Skill.Execute(ctx, params)
        ↓
MCP client call tool(server, original_tool_name, params)
        ↓
MCP response content
        ↓
skill.Result
```

## 9. 安全边界

### 9.1 DBAA 能保证什么

DBAA 提供应用层控制：

- 只加载允许目录；
- manifest schema 校验；
- skill name 校验；
- 禁止路径逃逸；
- 默认禁止覆盖内置 skill；
- 参数 schema 校验；
- timeout；
- stdout/stderr 输出限制；
- security level；
- operator/admin 确认；
- trace/audit；
- 不让 LLM 任意执行 shell；
- 不默认传数据库密码；
- MCP tools 必须 allowlist。

### 9.2 DBAA 不能保证什么

客户脚本本身是否安全，DBAA 无法完全证明。

客户必须负责：

- 脚本内容审计；
- 脚本运行用户权限；
- OS/容器/VM 沙箱；
- 文件系统访问控制；
- 网络访问控制；
- 第三方 Python 包安全；
- 是否泄露凭据；
- 是否删除文件或修改系统。

产品文档需要明确：

> DBAA 对外部 skill 提供加载、参数、权限、超时、审计和确认机制；客户自定义脚本运行在客户环境中，其系统级隔离、账号权限和脚本安全由客户负责。生产环境建议使用专用低权限用户、容器或客户自有沙箱运行外部 skill。

### 9.3 安全级别

沿用现有：

```text
read_only
operator
admin
```

建议规则：

- `read_only`: 默认可执行，但仍受 timeout/output cap；
- `operator`: 需要确认；
- `admin`: 需要确认，且 `/skills doctor` 高亮；
- 未声明 security: 拒绝加载；
- 声明 unknown security: 拒绝加载。

### 9.4 路径安全

`command` 解析后必须满足：

- clean 后仍在 skill 目录内；
- 目标文件存在；
- 目标不是目录；
- 文件可执行；
- 不是 symlink 到目录外，或 symlink 解析后仍在目录内；
- 所在目录非 world-writable，或 doctor 警告/拒绝。

### 9.5 参数安全

DBAA 将参数作为 JSON 传 stdin，不拼接进 shell command。

这避免：

```text
param = "x; rm -rf /"
```

被 shell 解释。

脚本内部如何使用参数，由客户负责。

## 10. 数据库连接与凭据

P0 默认不向外部脚本传数据库密码。

默认 context 只包含：

```json
{
  "db_type": "gaussdb",
  "connection": "gauss_local",
  "host": "10.0.0.1",
  "port": 8000,
  "database": "postgres",
  "user": "omm"
}
```

如果客户确实需要脚本直连数据库，建议后续增加显式能力：

```yaml
requires:
  - db_credentials
```

同时全局必须允许：

```yaml
external_skills:
  expose_db_credentials: true
```

只有两者都满足，且 security 至少为 `operator`，才允许传递敏感连接信息。

更好的后续方案是提供受控 SQL helper：

```text
外部脚本 -> 调 DBAA helper -> DBAA 用已有连接执行只读 SQL -> 返回 JSON
```

这样脚本不直接持有数据库密码。

## 11. LLM 接入

外部 skill 注册进 registry 后，上层无需区分来源。

### 11.1 Native FC

Engine 从 registry 构建 tool schema。外部 skill 的 `ToolDef()` 会进入 native FC tools。

### 11.2 PromptToolAdapter

Prompt mode 会把外部 skill 的 description 和 parameters 序列化进 system prompt。

需要注意：

- 外部 skill description 必须简洁；
- 外部 skill 数量可能很多，必须通过 tag/scene/filter 限制；
- 如果 current-db 这类受控证据报告阶段 `tools=nil`，PromptToolAdapter 必须不注入工具说明。

### 11.3 小模型可靠性

对小模型，建议：

- external skill descriptions 使用短句；
- parameters schema 避免复杂嵌套；
- 每个 skill 的输出格式在 markdown 正文中写清楚；
- 对高频外部 skill 可配置 trigger，让 DBAA router 先路由，不完全依赖 LLM 自主选择。

## 12. Router 接入

外部 skill 的 `triggers` 进入 router 的弱规则。

建议策略：

```text
trigger 命中 + read_only
        → 可以直接路由或给用户提示

trigger 命中 + operator/admin
        → 必须提示确认，不能静默执行

未命中 trigger
        → 交给 LLM 根据 ToolDef.Description 自主选择
```

示例：

```yaml
triggers:
  - 主备同步
  - 复制延迟
```

用户：

```text
看下主备同步有没有延迟
```

DBAA 可路由到：

```text
check_replication_lag
```

P0 可以先不做 trigger router，只让 LLM 可调用。P1/P2 再把 trigger 接进 deterministic router。

## 13. CLI 与 UX

### 13.1 `/skills list`

输出：

```text
名称                    来源       类型     安全级别   DB        状态
check_replication_lag   external   script   read_only  gaussdb   ok
cmdb_query_host         mcp        mcp      read_only  all       ok
health                  builtin    go       read_only  gaussdb   ok
```

### 13.2 `/skills show <name>`

展示：

- name；
- title；
- description；
- kind；
- db_types；
- security；
- timeout；
- source path；
- manifest hash；
- parameters schema；
- triggers；
- examples。

### 13.3 `/skills doctor`

检查：

- manifest 是否合法；
- command 是否存在；
- command 是否可执行；
- 路径是否逃逸；
- security 是否声明；
- timeout 是否超过上限；
- 是否覆盖内置 skill；
- MCP server 是否可连接；
- MCP tool allowlist 是否有效。

### 13.4 `/skills reload`

重新扫描目录。

注意：

- 应替换旧外部 skill；
- 不应影响内置 skill；
- 如果 reload 后某 skill 失败，应保留旧版本还是禁用？

建议 P0 策略：

- reload 是事务式；
- 新集合全部校验通过才替换；
- 有错误则保持旧集合，并输出错误。

### 13.5 `/skills init <name>`

生成模板：

```text
~/.opendb/skills/<name>/
  skill.md
  run.sh
```

`run.sh` 默认：

```bash
#!/usr/bin/env bash
set -euo pipefail
input="$(cat)"
echo "{\"ok\":true,\"summary\":\"TODO\",\"rendered\":\"TODO: implement skill\"}"
```

### 13.6 `/skills run <name> <json>`

用于手动验证：

```text
/skills run check_replication_lag '{"threshold_seconds":60}'
```

## 14. Trace / Audit

外部 skill 必须可复盘。

每次调用记录：

```json
{
  "trace_id": "...",
  "skill": "check_replication_lag",
  "kind": "script",
  "source": "~/.opendb/skills/check_replication_lag",
  "manifest_hash": "sha256:...",
  "script_hash": "sha256:...",
  "security": "read_only",
  "params_hash": "sha256:...",
  "started_at": "...",
  "ended_at": "...",
  "elapsed_ms": 1234,
  "exit_code": 0,
  "stdout_bytes": 2048,
  "stderr_summary": "",
  "status": "ok"
}
```

不要默认记录完整参数值，避免泄露敏感信息。可以记录：

- 参数 key 列表；
- hash；
- 红acted 摘要。

## 15. 错误处理

错误分类：

| 错误 | 用户提示 |
|---|---|
| manifest parse error | skill manifest 解析失败 |
| validation error | 参数不符合 schema |
| permission denied | 权限等级需要确认或被拒绝 |
| command not found | 脚本入口不存在 |
| path escape | command 不允许跳出 skill 目录 |
| timeout | 外部 skill 超时 |
| stdout too large | 输出过大已截断或拒绝 |
| stderr non-empty | 显示 stderr 摘要 |
| exit non-zero | 脚本返回非 0 |
| invalid json output | JSON 输出无效，按文本处理或报错 |
| mcp connect failed | MCP server 连接失败 |
| mcp tool denied | MCP tool 未在 allowlist |

建议：

- stdout 超限：默认截断并提示；
- stderr 超限：保留摘要；
- exit non-zero：返回 skill error；
- JSON 解析失败：如果 stdout 非空，作为文本输出；如果 manifest 声明 `output: json`，则报错。

## 16. 与 Golden / CI 的关系

P0 必须增加单测和基础 golden。

### 16.1 单测

覆盖：

1. 加载合法 `skill.md`；
2. 拒绝非法 name；
3. 拒绝路径逃逸；
4. 拒绝覆盖内置 skill；
5. timeout 生效；
6. stdout 超限截断；
7. stderr 摘要；
8. exit non-zero；
9. 文本输出映射；
10. JSON 输出映射；
11. read_only/operator/admin 权限；
12. `/skills list/show/doctor/init/run`；
13. external skill 出现在 registry；
14. native FC 可见外部 ToolDef；
15. prompt mode 可序列化外部 ToolDef。

### 16.2 Golden

新增 case：

```text
用户: 检查主备复制延迟
期望:
  - 命中 external skill 或 LLM 调用 external skill
  - 输出包含外部脚本结果
  - trace 包含 external skill 调用
```

MCP P1 使用 mock MCP server：

```text
tools/list -> query_host
tools/call -> mocked host info
```

## 17. 实施里程碑

### M1: Manifest + Loader

- `manifest.go`
- `loader.go`
- 目录扫描；
- frontmatter 解析；
- schema 校验；
- 注册到 registry。

### M2: Script Runner

- `ExternalScriptSkill`;
- stdin JSON；
- stdout/stderr；
- timeout；
- output parser；
- path/env 安全。

### M3: `/skills` 命令

- list；
- show；
- doctor；
- reload；
- init；
- run。

### M4: LLM 接入验证

- native FC tool schema；
- PromptToolAdapter 序列化；
- current-db `tools=nil` 不注入工具说明的修补；
- diagtrace external skill 记录。

### M5: MCP Adapter

- MCP server 配置；
- tools/list；
- allowlist overlay；
- MCPSkill；
- tools/call；
- timeout/audit；
- mock MCP tests。

## 18. 兼容性与升级

### 18.1 对现有内置 skill

默认无影响：

- 不改 `skill.Skill` 接口；
- 不改内置 skill 实现；
- 不允许外部 skill 覆盖内置 skill；
- 注册顺序清晰。

### 18.2 对 LLM

native FC:

- 外部 skill 自动作为 tools 暴露；
- 如果外部 skill 太多，可能增加 token 和工具选择难度，需要 filter。

prompt mode:

- 外部 skill 进入 PromptToolAdapter；
- 必须控制描述长度；
- 必须修复 `tools=nil` 时不注入工具格式的问题。

### 18.3 对客户交付

客户只需：

```text
mkdir -p ~/.opendb/skills/check_xxx
cp skill.md run.sh ~/.opendb/skills/check_xxx/
/skills reload
/skills doctor
/skills run check_xxx '{}'
```

不需要改 Go 源码，不需要重新编译 DBAA。

## 19. 示例: shell skill

```markdown
---
api_version: opendb.skill/v1
name: check_filesystem_usage
description: 检查数据库服务器文件系统使用率
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 10s
command: ./run.sh
parameters:
  type: object
  properties:
    threshold:
      type: integer
      description: 使用率告警阈值
      default: 85
  required: []
triggers:
  - 磁盘空间
  - 文件系统
  - filesystem
---

# 输出要求

输出超过阈值的挂载点、使用率和建议动作。
```

`run.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

input="$(cat)"
threshold="$(python3 - <<'PY' "$input"
import json, sys
obj=json.loads(sys.argv[1])
print(obj.get("params", {}).get("threshold", 85))
PY
)"

df -h | awk -v th="$threshold" '
NR==1 { next }
{
  use=$5
  gsub("%","",use)
  if (use+0 >= th) {
    print $0
  }
}'
```

## 20. 示例: python skill

```markdown
---
api_version: opendb.skill/v1
name: check_backup_window
description: 检查客户备份平台最近一次备份是否成功
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 20s
command:
  - python3
  - ./main.py
parameters:
  type: object
  properties:
    instance:
      type: string
      description: 备份平台实例名
  required: [instance]
triggers:
  - 备份
  - backup
---
```

`main.py`:

```python
#!/usr/bin/env python3
import json
import sys

request = json.load(sys.stdin)
instance = request["params"]["instance"]

print(json.dumps({
    "ok": True,
    "summary": f"{instance} 最近一次备份成功",
    "rendered": f"{instance} 最近一次备份成功，耗时 18 分钟，未超过备份窗口。",
    "data": {
        "instance": instance,
        "status": "ok"
    }
}, ensure_ascii=False))
```

## 21. 设计决策总结

1. 外部 skill 必须统一适配成 `skill.Skill`。
2. LLM 不能直接执行 shell。
3. 脚本输入走 stdin JSON，不拼 shell command。
4. 默认不传数据库密码。
5. MCP tools 默认不全部暴露，必须 allowlist。
6. external skill 默认不能覆盖内置 skill。
7. security level 必须显式声明。
8. 所有外部调用必须进入 trace/audit。
9. P0 先完成脚本 skill，P1 再完成 MCP Adapter。
10. Plugin 包管理作为 P2，不进入当前设计的首批交付范围。

## 22. 后续实现建议

第一批代码落地顺序：

1. `external.Manifest` 与 frontmatter parser；
2. `ExternalScriptSkill`；
3. loader 扫描目录并注册；
4. `/skills list/show/doctor/reload/init/run`；
5. trace/audit；
6. 单测；
7. 修复 PromptToolAdapter `tools=nil` 污染问题；
8. mock MCP adapter；
9. MCP allowlist；
10. golden 覆盖。

完成 P0 后，DBAA 就具备客户现场自定义脚本扩展能力；完成 P1 后，DBAA 可以接入客户内部 MCP 生态。
