#!/usr/bin/env python3
import json
import os
import shutil
import subprocess
import sys
from typing import Any, Dict, List


def as_int(value: Any, default: int) -> int:
    try:
        if value is None or value == "":
            return default
        return int(value)
    except Exception:
        return default


def db_client(params: Dict[str, Any]) -> str:
    explicit = str(params.get("dbcli") or "").strip()
    if explicit:
        return explicit
    return shutil.which("gsql") or shutil.which("psql") or ""


def run_sql(cli: str, ctx: Dict[str, Any], sql: str) -> List[List[str]]:
    host = str(ctx.get("host") or "127.0.0.1")
    port = str(ctx.get("port") or "5432")
    database = str(ctx.get("database") or "postgres")
    user = str(ctx.get("user") or "omm")
    base = os.path.basename(cli)
    if base == "gsql":
        cmd = [cli, "-X", "-q", "-t", "-A", "-F", "|", "-h", host, "-p", port, "-d", database, "-U", user, "-c", sql]
    else:
        cmd = [cli, "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-F", "|", "-h", host, "-p", port, "-d", database, "-U", user, "-c", sql]
    proc = subprocess.run(cmd, text=True, capture_output=True, timeout=45)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"{base} exited with code {proc.returncode}")
    rows: List[List[str]] = []
    for line in proc.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        rows.append(line.split("|"))
    return rows


def to_float(s: str) -> float:
    try:
        return float(s)
    except Exception:
        return 0.0


def to_int(s: str) -> int:
    try:
        return int(float(s))
    except Exception:
        return 0


def size_mb(bytes_s: str) -> float:
    return to_int(bytes_s) / 1024.0 / 1024.0


def markdown_table(headers: List[str], rows: List[List[Any]], limit: int) -> str:
    if not rows:
        return "无结果。"
    shown = rows[:limit]
    out = ["|" + "|".join(headers) + "|", "|" + "|".join(["---"] * len(headers)) + "|"]
    for row in shown:
        out.append("|" + "|".join(str(x).replace("\n", " ") for x in row) + "|")
    if len(rows) > limit:
        out.append(f"\n... 还有 {len(rows) - limit} 行已省略。")
    return "\n".join(out)


def main() -> int:
    req = json.load(sys.stdin)
    params = req.get("params") or {}
    ctx = req.get("context") or {}
    limit = as_int(params.get("limit"), 15)
    min_table_mb = as_int(params.get("min_table_mb"), 32)
    dead_pct_warn = as_int(params.get("dead_pct_warn"), 20)
    mod_warn = as_int(params.get("mod_since_analyze_warn"), 50000)
    min_bytes = min_table_mb * 1024 * 1024

    cli = db_client(params)
    if not cli:
        raise RuntimeError("PATH 中未找到 gsql 或 psql。请在客户沙箱安装数据库客户端，或通过参数 dbcli 指定。")

    table_sql = f"""
SELECT
  schemaname,
  relname,
  n_live_tup,
  n_dead_tup,
  ROUND(100.0 * n_dead_tup / GREATEST(n_live_tup + n_dead_tup, 1), 2) AS dead_pct,
  n_mod_since_analyze,
  COALESCE(last_vacuum::text, '-') AS last_vacuum,
  COALESCE(last_autovacuum::text, '-') AS last_autovacuum,
  COALESCE(last_analyze::text, '-') AS last_analyze,
  COALESCE(last_autoanalyze::text, '-') AS last_autoanalyze,
  seq_scan,
  idx_scan,
  pg_total_relation_size(relid) AS bytes
FROM pg_stat_user_tables
WHERE pg_total_relation_size(relid) >= {min_bytes}
   OR n_dead_tup > 0
   OR n_mod_since_analyze > 0
ORDER BY n_dead_tup DESC, n_mod_since_analyze DESC, pg_total_relation_size(relid) DESC
LIMIT {max(limit * 3, limit)};
"""
    index_sql = f"""
SELECT
  schemaname,
  relname,
  indexrelname,
  idx_scan,
  pg_relation_size(indexrelid) AS bytes
FROM pg_stat_user_indexes
WHERE pg_relation_size(indexrelid) >= {min_bytes}
ORDER BY idx_scan ASC, pg_relation_size(indexrelid) DESC
LIMIT {max(limit * 2, limit)};
"""

    table_rows = run_sql(cli, ctx, table_sql)
    index_rows = run_sql(cli, ctx, index_sql)

    bloat_findings = []
    stale_stats = []
    seq_scan_risk = []
    for row in table_rows:
        if len(row) < 13:
            continue
        schema, rel = row[0], row[1]
        live, dead = to_int(row[2]), to_int(row[3])
        dead_pct = to_float(row[4])
        mods = to_int(row[5])
        seq_scan, idx_scan = to_int(row[10]), to_int(row[11])
        mb = size_mb(row[12])
        name = f"{schema}.{rel}"
        if dead_pct >= dead_pct_warn or dead >= 100000:
            bloat_findings.append([name, f"{mb:.1f}", live, dead, f"{dead_pct:.2f}%", "建议评估 VACUUM/表膨胀原因"])
        if mods >= mod_warn:
            stale_stats.append([name, f"{mb:.1f}", mods, row[8], row[9], "建议 ANALYZE 后复测执行计划"])
        if mb >= min_table_mb and seq_scan >= 100 and idx_scan == 0:
            seq_scan_risk.append([name, f"{mb:.1f}", seq_scan, idx_scan, "大表顺序扫描偏多，需结合业务 SQL 评估索引"])

    unused_indexes = []
    for row in index_rows:
        if len(row) < 5:
            continue
        idx_scan = to_int(row[3])
        mb = size_mb(row[4])
        if idx_scan == 0 and mb >= min_table_mb:
            unused_indexes.append([f"{row[0]}.{row[2]}", f"{row[0]}.{row[1]}", f"{mb:.1f}", idx_scan, "仅候选，需结合约束/业务周期复核"])

    risk_count = len(bloat_findings) + len(stale_stats) + len(seq_scan_risk) + len(unused_indexes)
    lines = [
        "# Table Maintenance Advisor",
        "",
        f"- connection: {ctx.get('connection', '-')}",
        f"- target: {ctx.get('host', '127.0.0.1')}:{ctx.get('port', '-')}/{ctx.get('database', '-')}",
        f"- thresholds: min_table_mb={min_table_mb}, dead_pct_warn={dead_pct_warn}%, mod_since_analyze_warn={mod_warn}",
        "",
        "## 1. Dead Tuple / Bloat Candidates",
        markdown_table(["table", "MB", "live", "dead", "dead_pct", "action"], bloat_findings, limit),
        "",
        "## 2. Stale Statistics Candidates",
        markdown_table(["table", "MB", "mods", "last_analyze", "last_autoanalyze", "action"], stale_stats, limit),
        "",
        "## 3. Sequential Scan Risks",
        markdown_table(["table", "MB", "seq_scan", "idx_scan", "action"], seq_scan_risk, limit),
        "",
        "## 4. Large Unused Index Candidates",
        markdown_table(["index", "table", "MB", "idx_scan", "note"], unused_indexes, limit),
        "",
        "## Safety Notes",
        "- 本 skill 只读采集，不会执行 VACUUM/ANALYZE/REINDEX/DROP INDEX。",
        "- unused index 只代表统计周期内 idx_scan=0，不能直接删除；需确认约束、唯一性、月末/批处理周期。",
        "- stale statistics 建议优先在测试或低峰执行 ANALYZE 后复测 SQLTune。",
    ]
    rendered = "\n".join(lines)
    result = {
        "ok": True,
        "summary": f"table maintenance risks={risk_count}",
        "rendered": rendered,
        "data": {
            "bloat_count": len(bloat_findings),
            "stale_stats_count": len(stale_stats),
            "seq_scan_risk_count": len(seq_scan_risk),
            "unused_index_count": len(unused_indexes),
        },
        "metadata": {"source": "python_table_maintenance_advisor", "client": os.path.basename(cli)},
    }
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(json.dumps({
            "ok": False,
            "summary": str(exc),
            "rendered": "python_table_maintenance_advisor 执行失败：%s" % str(exc),
            "metadata": {"source": "python_table_maintenance_advisor"},
        }, ensure_ascii=False))
        raise SystemExit(1)

