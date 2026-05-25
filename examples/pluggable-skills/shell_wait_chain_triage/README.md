# shell_wait_chain_triage

Shell + Markdown pluggable skill for OpenGauss/GaussDB wait-chain triage.

## Install

```bash
cp -R shell_wait_chain_triage ~/.opendb/skills/
dbaa -c gauss_local /skills reload
dbaa -c gauss_local /skills show shell_wait_chain_triage
```

## Run

```bash
dbaa -c gauss_local /skills run shell_wait_chain_triage '{"min_seconds":30,"limit":10}'
```

Natural language examples:

- 当前有没有阻塞链，帮我用 shell_wait_chain_triage 看一下
- 当前有没有长事务或等待链

## Production Requirements

- `gsql` or `psql` in `PATH`.
- Passwordless local auth, `.pgpass`, or customer-approved secret injection.
- If `PATH` is not enough, configure `external_skills.env_allowlist` to include the needed client environment variables.

