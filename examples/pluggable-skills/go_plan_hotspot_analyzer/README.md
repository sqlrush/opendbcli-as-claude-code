# go_plan_hotspot_analyzer

Go + Markdown pluggable skill for EXPLAIN plan hotspot analysis.

## Build

```bash
cd go_plan_hotspot_analyzer
./build.sh
```

The runtime entry `run.sh` uses `bin/planhotspot` when present. If no binary exists and `go` is installed, it falls back to `go run ./cmd/planhotspot`.

## Install

```bash
cp -R go_plan_hotspot_analyzer ~/.opendb/skills/
dbaa -c gauss_local /skills reload
dbaa -c gauss_local /skills show go_plan_hotspot_analyzer
```

## Run With Plan Text

```bash
dbaa -c gauss_local /skills run go_plan_hotspot_analyzer '{"plan_text":"Seq Scan on bench_orders cost=59318 rows=1\\nSort cost=183866 rows=1","top_n":5}'
```

## Run With SQL

```bash
dbaa -c gauss_local /skills run go_plan_hotspot_analyzer '{"sql":"select * from bench_orders where status = ''paid''","top_n":5}'
```

The SQL mode requires `gsql` or `psql` connectivity in the customer sandbox.

