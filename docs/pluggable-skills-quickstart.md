# DBAA Pluggable Skills Quickstart

DBAA supports customer-provided pluggable skills for extending diagnostics without changing Go source code or rebuilding the binary.

This is designed for customer-owned runtime environments: DBAA validates and gates the skill as part of its own skill registry, while the customer remains responsible for the OS/container/VM sandbox around scripts or MCP servers they install.

## Scope in v1.2.31+

- P0 script skills: `skill.md` + shell/python/other executable script.
- P1 MCP adapter surface: configuration and allowlist metadata are available; runtime MCP invocation is intentionally kept opt-in and deny-by-default.
- P2 plugin package lifecycle is not enabled yet.

## Runtime Commands

```text
/skills list
/skills show <name>
/skills doctor
/skills reload
/skills init <name>
/skills run <name> [json-params]
```

`/help` remains the user-facing command catalog. External skills appear in `/help` after they are loaded. `/skills` is the management and inspection entry.

## Directory Layout

Default directory:

```text
~/.opendb/skills/
  my_check/
    skill.md
    run.sh
```

The directory can be customized in `config.yaml`:

```yaml
external_skills:
  enabled: true
  dirs:
    - ~/.opendb/skills
  allow_override_builtin: false
  max_timeout: 60s
  max_output_bytes: 262144
  max_stderr_bytes: 65536
  inherit_env: false
  env_allowlist: [PATH, LANG]
  expose_db_credentials: false
```

## skill.md Template

```markdown
---
api_version: opendb.skill/v1
name: my_check
title: My Check
description: Customer read-only check
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 30s
command: ["./run.sh"]
parameters:
  type: object
  properties:
    args:
      type: string
      description: Optional free-form arguments
triggers:
  - my custom check
---

This free-form body is shown by `/help my_check` and helps LLMs understand when to use the skill.
```

`command` never runs through a shell. Use an array when arguments are needed:

```yaml
command: ["python3", "scripts/run.py"]
```

Scalar commands are also split safely without shell expansion:

```yaml
command: python3 scripts/run.py "arg with space"
```

## Script Input

DBAA sends one JSON object to stdin:

```json
{
  "api_version": "opendb.skill/v1",
  "skill": "my_check",
  "params": {"args": "optional user args"},
  "context": {
    "db_type": "gaussdb",
    "connection": "gauss_local",
    "database": "postgres",
    "host": "127.0.0.1",
    "port": 5433,
    "user": "omm"
  }
}
```

Database passwords are not included. If a customer script needs privileged credentials, the customer should inject them through their own sandbox, secret manager, or service account policy.

## Script Output

Plain text is accepted:

```sh
printf '检查完成：未发现风险\n'
```

Structured JSON is also accepted:

```json
{
  "ok": true,
  "summary": "check completed",
  "rendered": "检查完成：未发现风险",
  "data": {"risk_count": 0},
  "metadata": {"source": "customer"}
}
```

## Safety Model

DBAA-side controls:

- Rejects external skills that conflict with built-in skill names by default.
- Registers skills into the normal skill registry, so they go through the standard security level and executor path.
- Requires explicit `security` metadata: `read_only`, `operator`, `admin`, or `dangerous`.
- Runs script commands without shell expansion.
- For script-relative paths, prevents escaping the skill directory.
- Applies timeout and stdout/stderr size limits.
- Does not inherit the full process environment by default.

Customer-side responsibilities:

- Decide which scripts/MCP servers are trusted enough to install.
- Run DBAA in the customer VM/container/sandbox appropriate for their security policy.
- Control OS permissions, network access, Python package installation, and secrets.
- Review scripts before placing them under the external skill directory.

## MCP Configuration Skeleton

MCP tools are deny-by-default and must be explicitly allowlisted:

```yaml
mcp:
  enabled: false
  servers:
    - name: customer-mcp
      command: ["python3", "server.py"]
      timeout: 30s
      tools:
        inspect_backup:
          enabled: true
          name: mcp_inspect_backup
          description: Inspect customer backup metadata
          security: read_only
          db_types: [opengauss, gaussdb]
          triggers:
            - backup metadata
```

In the current implementation, `/skills doctor` reports configured MCP servers/tools. Runtime MCP invocation should be added only after transport, authentication, timeout, and audit logging are all explicitly defined.
