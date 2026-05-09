<p align="center">
  <a href="./README.md">English</a> | <a href="./README_zh.md">简体中文</a>
</p>

<p align="center">
  <pre align="center">
 ██████╗ ██████╗ ███████╗███╗   ██╗██████╗ ██████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔══██╗██╔══██╗
██║   ██║██████╔╝█████╗  ██╔██╗ ██║██║  ██║██████╔╝
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║██║  ██║██╔══██╗
╚██████╔╝██║     ███████╗██║ ╚████║██████╔╝██████╔╝
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚═════╝ ╚═════╝
  </pre>
  <strong>DB CLI Agent — Inspired by Claude Code's interaction style, purpose-built for databases.</strong><br>
  <em>Less input. Best diagnosis.</em>
</p>

<p align="center">
  <a href="https://github.com/sqlrush/opendbcli-as-claude-code/releases"><img alt="Release" src="https://img.shields.io/github/v/release/sqlrush/opendbcli-as-claude-code?style=flat-square&color=blue"></a>
  <a href="https://github.com/sqlrush/opendbcli-as-claude-code/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-green?style=flat-square"></a>
  <img alt="Platform" src="https://img.shields.io/badge/platform-linux%20%7C%20macOS-lightgrey?style=flat-square">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go&logoColor=white">
</p>

---

## What is OpenDB?

<h3>OpenDB is a DB CLI Agent — inspired by Claude Code's interaction style, purpose-built for databases. Less input. Best diagnosis.</h3>

<h3>Currently supports Oracle, MySQL, and PostgreSQL. Three interaction modes seamlessly unified in one agent:</h3>

<h3>OpenDB auto-detects input type and routes to the right handler — no mode switching needed</h3>

<h3>(1) Slash commands (like Claude Code)</h3>

Complex operations distilled into simple one-liners:

```
opendb> /health                     ← Full health check (20+ items)
opendb> /dbtop                      ← Real-time dashboard, like Linux top
opendb> /slowsql 500                ← Find SQL slower than 500ms
opendb> /sentinel                   ← Start background anomaly probe
opendb> /rule                       ← Rule engine instant diagnosis
opendb> /llm                        ← AI deep diagnosis
opendb> /kill 1842                  ← Terminate problem session
opendb> /explain a3f8k2j9x          ← View SQL execution plan
```

<h3>(2) SQL & native database syntax (like sqlplus / mysql / psql)</h3>

Type SQL or database-native commands directly — your existing habits work as-is:

```
opendb> SELECT sid, event, seconds_in_wait FROM v$session WHERE status = 'ACTIVE';
opendb> ALTER SYSTEM KILL SESSION '1842,30721' IMMEDIATE;
opendb> SHOW PARAMETER sga_target;
opendb> DESC dba_tablespaces;
```

<h3>(3) Natural language (like a chat tool)</h3>

Describe your problem in plain language — OpenDB routes it to the AI agent:

```
opendb> Why is the database slow right now?
⚡ Analyzing active sessions... 42 sessions waiting on "enq: TX - row lock contention"
📋 Root cause: Session SID=1842 holds row lock on ORDER_MASTER for 47 minutes
🔧 Recommended action:
   ALTER SYSTEM KILL SESSION '1842,30721' IMMEDIATE;

opendb> Show me the top 5 slow queries today
opendb> Tablespace is almost full, what should I do?
opendb> Any ORA errors recently?
```
### Three Interaction Modes, One Shell

OpenDB auto-detects input type and routes to the right handler — no mode switching needed:

| Mode | Example | What happens |
|------|---------|-------------|
| **Slash commands** | `/slowsql 500` | Execute built-in skill: find SQL slower than 500ms |
| **Native SQL** | `SELECT * FROM v$session` | Run directly against the database, like sqlplus/mysql/psql |
| **Natural language** | `Show me the top 5 slow queries today` | Route to LLM agent for intelligent response |

---

## Quick Start

**Install** (Linux / macOS):

```bash
curl -fsSL https://www.opendbcli.org/install.sh | bash
```

**Run the setup wizard:**

```bash
opendb setup
```

The wizard walks you through database connection, LLM configuration, and rule engine setup — you're operational in under a minute.

**Modify existing configuration:**

```bash
opendb configure
```

Adjust database connections, LLM models, or rule engine settings at any time.

## Features

| Feature | Role | One-liner |
|---------|------|-----------|
| [`/health`](#health--full-spectrum-health-check) | Global check | 20+ items across 7 dimensions (instance/storage/sessions/memory/logs/backup/security) with ✓/⚠/✗ status |
| [`/dbtop`](#dbtop--real-time-performance-dashboard) | Second-level snapshot | Like Linux `top` — 1s refresh of SGA/PGA/TPS/QPS/wait events/active sessions for instant status |
| [`/sentinel`](#sentinel--real-time-anomaly-detection) | Continuous monitoring | Background probe: 48 metrics × 9 detection strategies, adaptive 3σ baseline, auto burst collection |
| [`/scheduler`](#schedule--automated-health-checks) | Scheduled patrol | Consolidate health / reports / maintenance into cron-scheduled background tasks |
| [`/rule`](#rule--deterministic-decision-engine) | Rule diagnosis | 273 rules × 5-phase pipeline, millisecond deterministic diagnosis, works offline without LLM |
| [`/llm`](#llm--ai-powered-diagnosis) | AI diagnosis | LLM function calling with up to 20 rounds of chain reasoning, auto evidence collection to root cause |

**How the six features work together:**

```
                    ┌──────────────────────────────────────────────┐
                    │           Continuous Sensing Layer            │
                    │                                              │
                    │  /sentinel ─── 24/7 background monitoring    │
                    │  │             48 metrics, auto-trigger      │
                    │  │                                            │
                    │  /scheduler ── Scheduled patrol, cron-based  │
                    │  │             daily /health automation       │
                    │  │                                            │
                    │  ▼ Anomaly alerts / scheduled triggers       │
                    ├──────────────────────────────────────────────┤
                    │           Status Assessment Layer             │
                    │                                              │
                    │  /health ───── Global check (20+ items)      │
                    │                Overall health score + issues  │
                    │                                              │
                    │  /dbtop ────── Second-level snapshot (1s)    │
                    │                Instant SGA/waits/sessions    │
                    │                                              │
                    │  ▼ Collected evidence data                   │
                    ├──────────────────────────────────────────────┤
                    │           Intelligent Diagnosis Layer         │
                    │                                              │
                    │  /rule ─────── Deterministic decision engine │
                    │  │             273 rules, millisecond-level  │
                    │  │             Works offline, no LLM needed  │
                    │  │                                            │
                    │  /llm ──────── LLM reasoning engine          │
                    │                Up to 20 rounds of function   │
                    │                calling, deep root cause + SQL│
                    └──────────────────────────────────────────────┘
```

---

### `/health` — Full-Spectrum Health Check

One command, 20+ check items across 7 dimensions: instance status, storage, sessions, memory, logs, backup, and security. Each item shows ✓ / ⚠ / ✗ status with actionable advice.

**Real output:**

```
┌─ Database Health — ORCLCDB ─────────────────────────────────────────────────┐
│  Overall: ✗ CRITICAL  (6 issues found)                                      │
├─ Basic ─────────────────────────────────────────────────────────────────────┤
│  Instance       UP 2.4 hours       ✓   DB Role       PRIMARY          ✓    │
│  Archive Mode   NOARCHIVELOG       ⚠                                        │
├─ Storage ───────────────────────────────────────────────────────────────────┤
│  Tablespace     Max SYSTEM 2.2%    ✓   Temp Space    Max 0.0%         ✓    │
│  Undo Space     No data            ✓   FRA Usage     Not configured   ✓    │
│  ASM Diskgroup  Not using ASM      ✓                                        │
├─ Session/Perf ──────────────────────────────────────────────────────────────┤
│  Connections    83 / 2000 (4%)     ✓   Active Sess   2                ✓    │
│  Wait Events    Top1: row cache l… ✗   Slow SQL      1 avg >5s       ⚠    │
│  Buffer Hit     100.0%             ✓   Library Hit   99.8%            ✓    │
├─ Memory ────────────────────────────────────────────────────────────────────┤
│  PGA Usage      7 MB (auto)        ✓   SGA Overview  9216 MB          ✓    │
│  Shared Pool    Free 61.8%         ✓                                        │
├─ Logs ──────────────────────────────────────────────────────────────────────┤
│  Redo Switch    17x in last 1h     ⚠   Alert Log     No alerts 24h   ✓    │
├─ Maintenance ───────────────────────────────────────────────────────────────┤
│  Backup         No backup records  ⚠   Statistics    3.7% stale      ✓    │
│  Invalid Obj    1 object           ⚠   Resource Lim  No limits hit   ✓    │
│  Password Exp   None expiring      ✓                                        │
├─ Alerts ────────────────────────────────────────────────────────────────────┤
│  ⚠ Archive mode: NOARCHIVELOG                                               │
│  ✗ Wait event: Top1: row cache lock 2653.5s                                 │
│  ⚠ Slow SQL: 1 avg >5s                                                      │
│  Tip: /waits to inspect wait events; /slowsql to find slow SQL; /redo to check redo switches │
└─────────────────────────────────────────────────────────────────────────────┘
```

### `/dbtop` — Real-Time Performance Dashboard

Like Linux `top`, but for databases. 1-second refresh showing SGA/PGA memory, db%, wait time ratio, TPS/QPS/Redo, top wait events (with both Delta and Cumulative views), and active session list — all critical metrics on one screen.

**Real output under load test traffic (108 active sessions):**

```
╭─ dbtop ── oracle 19c ── ORCLCDB ── PRIMARY ── ● WARNING ── 15:42:07 ────────────────────╮
│                                                                                          │
│  SGA ▇▇▇▇▇▇░░ 6,834M  PGA ▇▇░░░░░░ 247M  db% ▇▇▇▇▇▇▇░ 78.3  WTR% ▇▇▇▇▇▇▇▇ 92.1    │
│  Session 212  Active 108  ActiveCPU 6  ActiveIO 3  Idle 104  │  TPS 1,247  QPS 8,934    │
│                                                                                          │
╰──────────────────────────────────────────────────────────────────────────────────────────╯
╭─ Top Wait Events ────────────────────────────────────────────────────────────────────────╮
│  EVENT                    Dwait Dtime(ms) DPCT   Delta  │    Cwait   Ctime(s)  CPCT      │
│  enq: SQ - contention     1,247  35,812  ▇▇▇▇▇▇ 46.1%  │   71,342   2,046.8  ▇▇▇░ 46.1%│
│  buffer busy waits         2,891  22,658  ▇▇▇▇░░ 36.7%  │  119,834   1,628.6  ▇▇░░ 36.7%│
│  log file switch (chec..     12   6,607  ▇▇░░░░ 10.7%  │      373     476.9  ▇░░░ 10.7%│
│  cursor: pin S               892  1,914  ▇░░░░░  3.1%  │   35,621     137.3  ░░░░  3.1%│
│  latch: cache buffers ..    241    680  ░░░░░░  1.1%  │   10,234      49.1  ░░░░  1.1%│
╰──────────────────────────────────────────────────────────────────────────────────────────╯
╭─ Active Sessions (108) ──────────────────────────────────────────────────────────────────╮
│  SID   USR        SQLID         EVENT                CLASS       E/T  SQL                │
│  11    LOADTEST   4j031tn1y64s2 enq: SQ - contention Configurat 3.2s INSERT INTO hot_i..│
│  12    LOADTEST   4j031tn1y64s2 enq: SQ - contention Configurat 2.8s INSERT INTO hot_i..│
│  15    LOADTEST   fbnsszmxf8zsj cursor: pin S        Concurrenc 1.5s BEGIN FOR i IN 1..  │
│  17    LOADTEST   4s20v2f39mp83 buffer busy waits    Concurrenc 0.8s update seq$ set i.. │
│  20    LOADTEST   4j031tn1y64s2 enq: SQ - contention Configurat 4.1s INSERT INTO hot_i..│
│  195   LOADTEST   4j031tn1y64s2 enq: SQ - contention Configurat 2.3s INSERT INTO hot_i..│
│  206   LOADTEST   4j031tn1y64s2 enq: SQ - contention Configurat 3.7s INSERT INTO hot_i..│
│  386   LOADTEST   fbnsszmxf8zsj cursor: pin S        Concurrenc 2.1s BEGIN FOR i IN 1..  │
│  389   LOADTEST   4s20v2f39mp83 buffer busy waits    Concurrenc 0.6s update seq$ set i.. │
│  392   LOADTEST   4j031tn1y64s2 enq: SQ - contention Configurat 1.9s INSERT INTO hot_i..│
│  ... and 98 more active sessions                                                         │
╰──────────────────────────────────────────────────────────────────────────────────────────╯
 ● WARNING │ type /health for detailed diagnosis                                   15:42:07
```

### `/sentinel` — Real-Time Anomaly Detection

Sentinel is a background database probe. It's not simple threshold alerting — it uses adaptive statistical learning to continuously sense the database's "pulse" and catch anomalies the moment they emerge.

**Real alert output (active sessions surged from 8 to 41, breaching 3σ threshold of 24):**

```
▸ Active Sessions 8.0→41 (3σ threshold 24.0)

── Report #1 ──

  Trigger: active_sessions  8.0 → 41.0
  Duration: 39.8s

  Wait Event Distribution:
  ┌─────────────────────────────┬───────┬──────────────────────┬───────────────┐
  │ EVENT                       │ PCT   │ Distribution         │ WAIT_CLASS    │
  ├─────────────────────────────┼───────┼──────────────────────┼───────────────┤
  │ enq: SQ - contention        │ 53.6% │ ██████████░░░░░░░░░░ │ Configuration │
  │ DB CPU                      │ 27.0% │ █████░░░░░░░░░░░░░░░ │ CPU           │
  │ buffer busy waits           │ 13.9% │ ██░░░░░░░░░░░░░░░░░░ │ Concurrency   │
  │ cursor: pin S               │ 4.8%  │ ░░░░░░░░░░░░░░░░░░░░ │ Concurrency   │
  │ latch: cache buffers chains │ 0.5%  │ ░░░░░░░░░░░░░░░░░░░░ │ Concurrency   │
  └─────────────────────────────┴───────┴──────────────────────┴───────────────┘

  Top SQL:
  ┌───────────────┬──────────┬──────┬─────────────────────────────┐
  │ SQL_ID        │ Elapsed  │ Conc │ Wait Event                  │
  ├───────────────┼──────────┼──────┼─────────────────────────────┤
  │ 4j031tn1y64s2 │ 1.0s     │ 40   │ enq: SQ - contention        │
  │ 4s20v2f39mp83 │ 1.0s     │ 18   │ buffer busy waits           │
  │ cu13b1uu2upwd │ 1.0s     │ 3    │ latch: cache buffers chains │
  └───────────────┴──────────┴──────┴─────────────────────────────┘

  Blocking Chains:
  ┌──────┬─────────┬───────────────┬──────────────────────┬────────┐
  │ SID  │ Blocker │ SQL_ID        │ Wait Resource        │ Victims│
  ├──────┼─────────┼───────────────┼──────────────────────┼────────┤
  │ 2275 │ -       │ 4j031tn1y64s2 │ enq: SQ - contention │ 39     │
  │ 1534 │ -       │ 4j031tn1y64s2 │ enq: SQ - contention │ 37     │
  │ 2663 │ -       │ 4s20v2f39mp83 │ buffer busy waits    │ 29     │
  └──────┴─────────┴───────────────┴──────────────────────┴────────┘
```

### `/scheduler` — Automated Health Checks

Consolidate daily inspections, recurring reports, and routine maintenance into scheduled background tasks — no crontab, no scripts, no context switching.

```
opendb> /scheduler start health-check --cron "0 8 * * *"
✓ Scheduled: health-check runs daily at 08:00
```

### `/llm` — AI-Powered Diagnosis

OpenDB uses LLM function calling to drive multi-round, evidence-based diagnosis. The agent doesn't guess — it queries the database, analyzes results, forms hypotheses, and iterates until it finds the root cause, delivering executable repair SQL.

**Real diagnosis under load test (Claude Opus, 3 rounds, 1m59s):**

```
opendb> /llm

  ✻ Done (1m 59s)
  ├ Round 1: called health (21.7s)
  ├ Round 2: called waits (1m 5s)
  ├ Round 3: output final diagnosis (32.0s)

■ Database Status Diagnosis

  ▸ Issue 1 (Critical): Sequence Contention — 70.6% of total waits

  ┌──────┬────────────────────────────────────────────────────────────────────────────────────────────┐
  │ Item │ Details                                                                                    │
  ├──────┼────────────────────────────────────────────────────────────────────────────────────────────┤
  │ Root │ ORDER_ITEMS.ITEM_ID identity column sequence cache only 20, 243.8K contentions,            │
  │ Cause│ cumulative wait 3630.7s                                                                    │
  │ Evid │ 51 sessions waiting on enq: SQ - contention, SQL g11yt3q86jfx2 (INSERT INTO ORDER_ITEMS)  │
  │      │ avg execution 5.18s                                                                        │
  └──────┴────────────────────────────────────────────────────────────────────────────────────────────┘

  Immediate fix — increase sequence cache:

  ┌─ sql ──────────────────────────────────────────────────────────────────────────────────────────┐
  │ -- ORDER_ITEMS.ITEM_ID (maps to ISEQ$$_131202, current cache=20)                               │
  │ ALTER TABLE LOADUSER.ORDER_ITEMS MODIFY ITEM_ID GENERATED ALWAYS AS IDENTITY (CACHE 5000 NOO.. │
  │                                                                                                │
  │ -- ORDERS.ORDER_ID (maps to ISEQ$$_131204, current cache=20, fix together)                     │
  │ ALTER TABLE LOADUSER.ORDERS MODIFY ORDER_ID GENERATED ALWAYS AS IDENTITY (CACHE 5000 NOORDER); │
  └────────────────────────────────────────────────────────────────────────────────────────────────┘

  ▸ Issue 2 (Important): Buffer Busy Waits — 21.4% of total waits

  ┌──────┬────────────────────────────────────────────────────────────────────────┐
  │ Item │ Details                                                                │
  ├──────┼────────────────────────────────────────────────────────────────────────┤
  │ Root │ High-concurrency INSERT with ascending identity column, all sessions   │
  │ Cause│ contending on the same rightmost index leaf block (hot block)          │
  │ Evid │ 223.2K buffer busy waits, 1098.9s cumulative, avg 4.9ms               │
  └──────┴────────────────────────────────────────────────────────────────────────┘

  Root cause fix — increasing sequence cache will significantly reduce this.
  If persists, rebuild index as reverse key or hash partition to eliminate hot block:

  ┌─ sql ──────────────────────────────────────────────────────────────────────────────────────────┐
  │ -- If buffer busy waits persist after increasing cache, rebuild index as reverse key           │
  │ -- First find index name                                                                       │
  │ SELECT index_name, uniqueness FROM dba_indexes WHERE table_owner = 'LOADUSER' AND table_name.. │
  │ -- Rebuild as reverse key index (online operation)                                             │
  │ -- ALTER INDEX LOADUSER.<index_name> REBUILD REVERSE ONLINE;                                   │
  └────────────────────────────────────────────────────────────────────────────────────────────────┘

  ▸ Issue 3 (Critical): Redo Log Switches Too Frequent

  ┌──────┬───────────────────────────────────────────────────────────────────────────────────────────┐
  │ Item │ Details                                                                                   │
  ├──────┼───────────────────────────────────────────────────────────────────────────────────────────┤
  │ Root │ 3 groups × 200MB redo logs, 162 switches in last hour (~22 sec/switch)                    │
  │ Evid │ log file switch (checkpoint incomplete) 342 waits 78.2s,                                  │
  │      │ log file switch completion 383 waits 10.5s                                                │
  └──────┴───────────────────────────────────────────────────────────────────────────────────────────┘

  Immediate fix — expand redo logs to 1GB:

  ┌─ sql ───────────────────────────────────────────┐
  │ ALTER DATABASE ADD LOGFILE GROUP 4 SIZE 1G;     │
  │ ALTER DATABASE ADD LOGFILE GROUP 5 SIZE 1G;     │
  │ ALTER DATABASE ADD LOGFILE GROUP 6 SIZE 1G;     │
  │                                                 │
  │ -- Switch logs to make old groups INACTIVE      │
  │ ALTER SYSTEM SWITCH LOGFILE;                    │
  │ ALTER SYSTEM SWITCH LOGFILE;                    │
  │ ALTER SYSTEM SWITCH LOGFILE;                    │
  │ ALTER SYSTEM CHECKPOINT;                        │
  │                                                 │
  │ -- Drop old groups after confirming INACTIVE    │
  │ ALTER DATABASE DROP LOGFILE GROUP 1;            │
  │ ALTER DATABASE DROP LOGFILE GROUP 2;            │
  │ ALTER DATABASE DROP LOGFILE GROUP 3;            │
  └─────────────────────────────────────────────────┘

  ▸ Other Notes

  ┌────────────────────┬────────────────────┬──────────────────────────────────────────┐
  │ Issue              │ Risk               │ Recommendation                           │
  ├────────────────────┼────────────────────┼──────────────────────────────────────────┤
  │ NOARCHIVELOG       │ Data loss on media │ Enable archive mode for production       │
  │                    │ failure            │                                          │
  │ No backup records  │ Unrecoverable      │ Configure RMAN scheduled backups         │
  │ cursor: pin S 5.5% │ Medium             │ Will auto-resolve after sequence fix     │
  └────────────────────┴────────────────────┴──────────────────────────────────────────┘

  ▸ Execution Priority

  1. Immediate: Increase identity column cache to 5000 → resolve 51 session blocks (70.6%)
  2. Immediate: Expand redo logs to 1GB → eliminate frequent switches (1.7%)
  3. Observe: buffer busy waits and cursor pin S — retest after first two fixes
```

Supports multiple LLM providers (OpenAI / Ollama / DeepSeek / vLLM) — hot-swap at runtime via `/model`.

### `/rule` — Deterministic Decision Engine

When LLM is unavailable or speed is critical, the Rule engine provides millisecond-level deterministic diagnosis. This isn't simple if-else rules — it's a **multi-phase decision pipeline** distilled from Claude Opus expert-level DBA diagnostic capabilities, with 273 specialized rules.

**Real diagnosis under load test (completed in 2.7s):**

```
opendb> /rule

── Rule Diagnosis ────────────────────────────────────────────────

  Root Cause: Sequence CACHE too small or NOCACHE causing SQ contention
  Severity: ■■■□ HIGH    Confidence: 78%

  ── Evidence Chain ──
  1. enq: SQ - contention in single-instance typically caused by small sequence CACHE
  2. Each CACHE exhaustion updates data dictionary seq$, causing row cache lock and SQ enqueue
  3. High-concurrency INSERT with NOCACHE or small CACHE sequences especially severe
  4. Related rule WE2-007 (enq: SQ — sequence cross-instance ordering (RAC)) check recommended
  5. Related rule WD015 (row cache lock diagnosis) check recommended

  ── Remediation ──
  [URGENT] 1. Find contended sequences and increase CACHE
           > /sql "SELECT s.sequence_owner, s.sequence_name, s.cache_size,
             s.order_flag, s.last_number FROM dba_sequences s
             WHERE s.sequence_owner NOT IN ('SYS','SYSTEM','AUDSYS','DBSNMP')
             ORDER BY s.cache_size ASC FETCH FIRST 20 ROWS ONLY"
           Risk: sequence number gaps increase (uniqueness unaffected)
  [FIX]    2. Check ASH to confirm contended sequence objects
           > /sql "SELECT h.current_obj#, o.object_name, COUNT(*) waits
             FROM v$active_session_history h LEFT JOIN dba_objects o
             ON h.current_obj# = o.object_id
             WHERE h.event = 'enq: SQ - contention'
             AND h.sample_time > SYSDATE - 1/24
             GROUP BY h.current_obj#, o.object_name
             ORDER BY waits DESC FETCH FIRST 10 ROWS ONLY"
           Risk: none
  [PREVENT]3. Consider UUID or application-level ID generation for high-frequency INSERT
           Risk: requires application changes

  ── Correlated Analysis ──
  • cursor: pin S diagnosis: downstream symptom, will resolve after fixing root cause
  • AWR Segment Statistics hotspot identification: downstream symptom, will resolve after fixing root cause
  • buffer busy waits diagnosis: downstream symptom, will resolve after fixing root cause

──────────────────────────────────────────────────────────────────
```

## Skill Catalog

OpenDB ships with 60+ built-in database management skills. One command replaces multi-step operations:

### Monitoring & Real-Time Dashboard

| Skill | Description | Oracle | MySQL | PG |
|-------|------------|:------:|:-----:|:--:|
| `/dbtop` | Real-time dashboard, 1s refresh, SGA/PGA/TPS/QPS/waits/sessions | ✅ | ✅ | ✅ |
| `/health` | Full-spectrum health check (20+ items), 7 dimensions ✓/⚠/✗ | ✅ | ✅ | ✅ |
| `/sessions` | All sessions: SID, user, status, SQL_ID, wait event | ✅ | ✅ | ✅ |
| `/activesessions` | Active sessions (top 50), wait class and seconds | ✅ | ✅ | ✅ |
| `/waits` | Wait event statistics, sorted by cumulative time, identify primary issues | ✅ | ✅ | ✅ |
| `/locks` | Row and table lock info, holder and waiter sessions | ✅ | ✅ | ✅ |
| `/blocktree` | Lock blocking chain tree: blocker → blocked | ✅ | ✅ | ✅ |
| `/latches` | Latch contention: gets, misses, wait time | ✅ | — | — |
| `/mutexes` | Mutex contention statistics | ✅ | — | — |
| `/os` | OS metrics: CPU, memory, I/O, load average | ✅ | ✅ | ✅ |
| `/bufferpool` | InnoDB Buffer Pool statistics | — | ✅ | — |
| `/innodb` | InnoDB engine-specific statistics | — | ✅ | — |
| `/deadlock` | Recent deadlock information | — | ✅ | — |
| `/replication` | Replication status | — | ✅ | ✅ |
| `/vacuum` | Vacuum / autovacuum status | — | — | ✅ |
| `/xid` | Transaction ID (XID) wraparound monitoring | — | — | ✅ |
| `/sharedbuffs` | Shared buffer pool statistics | — | — | ✅ |
| `/wal` | Write-Ahead Log status | — | — | ✅ |
| `/longtx` | Long-running transaction detection | — | — | ✅ |
| `/bloat` | Table/index bloat estimation | — | — | ✅ |
| `/slots` | Replication slot status | — | — | ✅ |

### Memory & Storage

| Skill | Description | Oracle | MySQL | PG |
|-------|------------|:------:|:-----:|:--:|
| `/sga` | SGA memory component breakdown | ✅ | — | — |
| `/pga` | PGA memory: target/allocated/used/free | ✅ | — | — |
| `/space` | Tablespace usage analysis, auto-alerts on near-full | ✅ | ✅ | ✅ |
| `/tempsess` | Temp tablespace consumption by session | ✅ | — | — |
| `/undosess` | Undo tablespace consumption by session | ✅ | — | — |
| `/redo` | Redo log status, switch frequency, size | ✅ | — | — |
| `/fra` | Flash Recovery Area usage | ✅ | — | — |
| `/asm` | ASM diskgroup status, capacity, redundancy | ✅ | — | — |
| `/segments` | Top segments (tables/indexes) by space | ✅ | ✅ | ✅ |
| `/resource` | Resource limit utilization, early issue detection | ✅ | ✅ | ✅ |
| `/sortusage` | Sort/hash area usage details | ✅ | — | — |

### SQL Analysis & Tuning

| Skill | Description | Oracle | MySQL | PG |
|-------|------------|:------:|:-----:|:--:|
| `/slowsql` | Find slow SQL (default 1000ms), auto-flags full table scans | ✅ | ✅ | ✅ |
| `/topsql` | Top SQL ranked by elapsed/executions/physical reads | ✅ | ✅ | ✅ |
| `/explain` | SQL execution plan analysis | ✅ | ✅ | ✅ |
| `/sql` | Execute custom SQL queries | ✅ | ✅ | ✅ |
| `/awr` | AWR snapshot analysis and comparison | ✅ | — | — |
| `/ash` | Active Session History sampling analysis | ✅ | ✅ | ✅ |
| `/planhistory` | SQL execution plan history tracking | ✅ | — | — |
| `/perfsnap` | Performance snapshot comparison, key metric deltas | ✅ | ✅ | ✅ |

### Administration & Maintenance

| Skill | Description | Oracle | MySQL | PG |
|-------|------------|:------:|:-----:|:--:|
| `/kill` | Terminate database session, security confirmation | ✅ | ✅ | ✅ |
| `/alter` | Modify system parameters | ✅ | ✅ | ✅ |
| `/resize` | Tablespace expansion, add datafiles | ✅ | — | — |
| `/params` | Search and display database parameters | ✅ | ✅ | ✅ |
| `/alert` | Recent alert log entries (default 24h) | ✅ | ✅ | ✅ |
| `/backup` | Backup history and status | ✅ | ✅ | ✅ |
| `/standby` | Primary-standby replication status | ✅ | ✅ | ✅ |
| `/jobs` | Database scheduler job status and history | ✅ | ✅ | ✅ |
| `/gather` | Statistics collection management | ✅ | ✅ | ✅ |
| `/users` | User accounts and password expiry | ✅ | ✅ | ✅ |

### Schema Analysis

| Skill | Description | Oracle | MySQL | PG |
|-------|------------|:------:|:-----:|:--:|
| `/tableinfo` | Table structure: columns, constraints, indexes, row count | ✅ | ✅ | ✅ |
| `/indexadvise` | Index optimization: missing and redundant detection | ✅ | ✅ | ✅ |
| `/indexhealth` | Index health: UNUSABLE, fragmentation | ✅ | ✅ | ✅ |
| `/ora` | ORA error code knowledge base (60+ entries) | ✅ | — | — |
| `/myerr` | MySQL error code knowledge base | — | ✅ | — |
| `/pgerr` | PostgreSQL error code knowledge base | — | — | ✅ |

### AI Diagnosis

| Skill | Description | Oracle | MySQL | PG |
|-------|------------|:------:|:-----:|:--:|
| `/sentinel` | Background Sentinel real-time anomaly probe | ✅ | ✅ | ✅ |
| `/rule` | Rule engine diagnosis (273 rules), millisecond-level | ✅ | ✅ | ✅ |
| `/llm` | LLM multi-round chain diagnosis, up to 20 rounds | ✅ | ✅ | ✅ |
| `/model` | LLM model management, hot-swap at runtime | ✅ | ✅ | ✅ |

---

## Architecture

OpenDB combines three diagnostic tiers into a unified pipeline:

```
                         ┌──────────────────────────┐
                         │      User Input          │
                         │  /cmd │ SQL │ Natural Lang│
                         └──────────┬───────────────┘
                                    │
                              ┌─────▼─────┐
                              │ Dispatcher │ ← Smart input classification
                              └─────┬─────┘
                   ┌────────────┼────────────┐
                   ▼            ▼            ▼
            ┌──────────┐ ┌──────────┐ ┌──────────┐
            │  Skill   │ │   SQL    │ │   LLM    │
            │ Executor │ │  Engine  │ │  Agent   │
            └──────────┘ └──────────┘ └────┬─────┘
                                           │
                              ┌────────────┼────────────┐
                              ▼            ▼            ▼
                        ┌──────────┐ ┌──────────┐ ┌──────────┐
                        │ Sentinel │ │   Rule   │ │   LLM    │
                        │  Probes  │ │  Engine  │ │ Reasoning │
                        └──────────┘ └──────────┘ └──────────┘
                         Real-time    Deterministic  Multi-round
                         detection    decision tree   chain-of-thought
```

### How Sentinel Detects Anomalies

```
   Normal operation          Anomaly detected          Burst collection
  ┌─────────────────┐    ┌─────────────────────┐    ┌──────────────────┐
  │ Collect metrics  │    │ Metric exceeds       │    │ Switch to 200ms  │
  │ every 1 second   │──▶│ adaptive 3σ baseline │──▶│ high-frequency   │
  │                  │    │                      │    │ collection       │
  │ Build baseline   │    │ Trigger alert        │    │ Capture evidence │
  │ (60s window)     │    │                      │    │ for diagnosis    │
  └─────────────────┘    └─────────────────────┘    └──────────────────┘
```

### Rule — Multi-Phase Decision Pipeline

The Rule engine isn't simple rule matching — it's a **5-phase diagnostic pipeline** that completes the journey from signal extraction to root cause identification in milliseconds:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Rule Engine 5-Phase Pipeline                          │
│                                                                         │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐            │
│  │ Phase 1  │──▶│ Phase 2  │──▶│ Phase 3  │──▶│ Phase 4  │            │
│  │ Signal   │   │ Index    │   │ Trigger  │   │ Decision │            │
│  │ Extract  │   │ Lookup   │   │ Evaluate │   │ Tree     │            │
│  │          │   │          │   │          │   │          │            │
│  │ Wait evt │   │ 5 invert │   │ AND gate │   │ Max 20   │            │
│  │ Err code │   │ index    │   │ + SkipWh │   │ levels   │            │
│  │ Metrics  │   │ O(1)     │   │ Matched  │   │ 50+ bran │            │
│  │ Category │   │ lookup   │   │ rules    │   │ 32 evid  │            │
│  │ Keywords │   │          │   │          │   │ queries  │            │
│  └──────────┘   └──────────┘   └──────────┘   └────┬─────┘            │
│                                                     │                  │
│                                                     ▼                  │
│                                              ┌──────────────┐          │
│                                              │   Phase 5    │          │
│                                              │  Conflict    │          │
│                                              │  Resolution  │          │
│                                              │              │          │
│                                              │ 1. Causal    │          │
│                                              │    chain     │          │
│                                              │ 2. Weight    │          │
│                                              │    converge  │          │
│                                              │ 3. Absorb    │          │
│                                              │    related   │          │
│                                              │              │          │
│                                              │ Output: 1-2  │          │
│                                              │ core causes  │          │
│                                              └──────────────┘          │
│                                                                         │
│  + Phase 5+: SQL Performance Enrichment                                 │
│    Full scan detection │ Plan drift │ Hot SQL analysis │ Cache advice   │
└─────────────────────────────────────────────────────────────────────────┘
```

**Key technical challenges:**

- **5-way inverted index**: O(1) lookup by wait event, error code, metric name, category, keyword — millisecond candidate selection from 273 rules instead of sequential scan
- **Decision tree nesting**: Up to 20 levels deep, 50+ branch conditions per rule. E.g. `WE007` (row lock) must judge: deadlock? → DDL blocker? → blocker idle? → TX lock mode → long transaction — each level backed by 32 predefined evidence SQL queries
- **Causal chain resolution**: When multiple rules fire (e.g. SGA exhaustion causing both buffer hit drops + I/O wait increases), CausesOf/CausedBy directed graph auto-identifies root vs downstream symptoms, scoring formula `Score = SeverityWeight × Confidence × Specificity` converges to 1-2 core causes

### LLM — Multi-Round Chain Reasoning Engine

The LLM Agent isn't one-shot Q&A — it's a **controllable multi-round reasoning loop** where each round uses function calling to invoke 30+ database query tools, progressively converging to root cause:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    LLM Agent Reasoning Loop                             │
│                                                                         │
│  ┌─────────────────────┐                                               │
│  │ BurstReport Compress│  Raw telemetry → ~2000 token structured       │
│  │ ├─ Metrics: db%,    │  summary with: classification, metrics,       │
│  │ │  wait%            │  wait distribution, Top SQL ×3,               │
│  │ ├─ Top waits ×5     │  blocking chains ×3                           │
│  │ └─ Top SQL ×3       │                                               │
│  └──────────┬──────────┘                                               │
│             │                                                           │
│             ▼                                                           │
│  ┌─────────────────────────────────────────────────┐                   │
│  │          Reasoning Loop (max 20 rounds)          │                   │
│  │                                                  │                   │
│  │  for turn = 0; turn < maxTurns; turn++           │                   │
│  │    ┌──────────────────────────────────────┐      │                   │
│  │    │ 1. Inject convergence hint (T-2)     │      │                   │
│  │    │ 2. LLM reasons → selects tool calls  │      │                   │
│  │    │ 3. No tool calls → converged, output │      │                   │
│  │    │ 4. Execute tools → security check    │      │                   │
│  │    │ 5. Evidence accumulates in context   │      │                   │
│  │    └──────────────────────────────────────┘      │                   │
│  │                                                  │                   │
│  │  Convergence guarantees:                         │                   │
│  │  ├─ T-2: "Give final diagnosis summary now"     │                   │
│  │  ├─ Timeout fallback: Auto(10) → Assist(3)      │                   │
│  │  └─ Final: "Last round, must diagnose now"       │                   │
│  └─────────────────────┬───────────────────────────┘                   │
│                        │                                                │
│                        ▼                                                │
│  ┌─────────────────────────────────────────────────┐                   │
│  │ Diagnosis Report                                 │                   │
│  │ ├─ Root cause + evidence chain                   │                   │
│  │ ├─ Executable repair SQL                         │                   │
│  │ └─ Reasoning trace (per-round) + token stats     │                   │
│  └─────────────────────────────────────────────────┘                   │
│                                                                         │
│  Anti-hallucination:                                                    │
│  ├─ Values in conclusions must come from tool results                   │
│  ├─ Unqueried metrics cannot appear in conclusions                      │
│  ├─ Repair SQL must verify object existence via sql skill first         │
│  └─ Dangerous ops (ALTER/DROP) require security confirmation            │
└─────────────────────────────────────────────────────────────────────────┘
```

**Key technical challenges:**

- **Convergence control**: LLM reasoning is inherently divergent. OpenDB ensures convergence through triple mechanisms — convergence hint injection at T-2, automatic degradation on timeout (Auto 10 rounds → Assist 3 rounds fallback), final round forced output. Prevents LLM from endlessly calling tools without concluding
- **Evidence compression**: Sentinel's BurstReport contains ~150 frames × 48 metrics of raw data — direct input would blow context. OpenDB compresses it to ~2000 token structured summary (metrics + wait distribution + Top SQL + blocking chains), preserving diagnostic signals while controlling token cost
- **Anti-hallucination**: The biggest risk of LLM diagnosis is fabricating non-existent problems. OpenDB enforces hard constraints in the system prompt: "values cited in conclusions must come from tool query results", "repair SQL must verify object existence first" — turning diagnosis from guesswork into evidence-based reasoning
- **Dual model strategy**: Small models (≤9B) use GuidedStrategy (OpenDB-orchestrated, 3 rounds read-only), large models (≥27B) use AutonomousStrategy (10 rounds full-tool autonomous diagnosis) — automatically selecting the optimal strategy based on model capability

---



## Build from Source

Five minutes from clone to a working binary.

### Prerequisites

- **Go 1.24+** (`go version` to verify)
- **Git**

Optional:
- **UPX** — compresses Linux binary ~80% (53 MB → 10 MB). `brew install upx` / `apt install upx-ucl`.
- **codesign** — required for macOS Apple Silicon binaries (built into macOS). Without re-signing after `cp`, the kernel kills the binary with no message.

### Clone & Build

```bash
git clone https://github.com/sqlrush/opendbcli-as-claude-code.git
cd opendbcli-as-claude-code

# Build for current platform with all DB drivers
go build -tags 'oracle mysql postgres opengauss gaussdb' -ldflags='-s -w' -o opendb ./cmd/opendb/

./opendb --version
# opendb v1.1.31 ...
```

### Build Tags

OpenDB uses Go build tags to pick database drivers — only the drivers you tag get linked, keeping the binary lean.

| Tag | Database |
|---|---|
| `oracle` | Oracle (go-ora pure-Go driver, no Oracle Client needed) |
| `mysql` | MySQL |
| `postgres` | PostgreSQL |
| `opengauss` | openGauss (open-source community, pgx-compatible) |
| `gaussdb` | GaussDB (Huawei commercial, gaussdb-go, SCRAM-SHA256(10)) |
| `full` | All five drivers above |

DM (达梦) driver is proprietary; out-of-scope for this open-source repo.

### Cross-Compile

```bash
# Linux x86_64
GOOS=linux GOARCH=amd64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-linux-amd64 ./cmd/opendb/

# Linux ARM64 (Kunpeng / Phytium / Apple Silicon Linux)
GOOS=linux GOARCH=arm64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-linux-arm64 ./cmd/opendb/

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-darwin-arm64 ./cmd/opendb/

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -tags 'oracle mysql postgres opengauss gaussdb' \
    -ldflags='-s -w' -o opendb-darwin-amd64 ./cmd/opendb/
```

### Compress (optional, Linux only)

```bash
upx --best --lzma opendb-linux-amd64
# Typical: 53 MB → 10 MB
```

### macOS Apple Silicon: Re-sign After Copy

```bash
codesign --force -s - opendb-darwin-arm64
chmod +x opendb-darwin-arm64
sudo cp opendb-darwin-arm64 /usr/local/bin/opendb
sudo codesign --force -s - /usr/local/bin/opendb
```

Skipping this gives "zsh: killed opendb" with no further explanation.

### One-Shot Pipeline

`scripts/publish.sh` automates the four-platform build, UPX, codesign, gzip + sha256 packaging:

```bash
./scripts/publish.sh --build-only
# Artifacts: dist/releases/<version>/opendb/upload/*.gz + .sha256
```

### Run Tests

```bash
go test -tags 'oracle mysql postgres opengauss gaussdb' ./...
```

---
<p align="center">
  <strong>One agent. All databases. Zero complexity.</strong><br>
  <a href="https://www.opendbcli.org">Website</a> · <a href="https://github.com/sqlrush/opendbcli-as-claude-code/issues">Issues</a>
</p>
