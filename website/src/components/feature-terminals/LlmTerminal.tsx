import TerminalMockup from "../TerminalMockup";

const DIM = "rgba(237,236,236,0.35)";
const GREEN = "#7ee787";
const YELLOW = "#ffc61c";
const ORANGE = "#ff6b35";

const ROUNDS = [
  { id: "R1", call: "get_wait_events()", result: "db file seq 42% · log sync 28%" },
  { id: "R2", call: "get_sql_stats(top=5)", result: "FTS on orders: 12M rows/exec" },
  { id: "R3", call: "get_index_info(table='orders')", result: "0 indexes on status col" },
];

const PROBLEMS = [
  { rank: 1, color: ORANGE, text: "全表扫描 — orders.status 无索引" },
  { rank: 2, color: YELLOW, text: "归档日志量 2× 基线 — 大批量 DML" },
  { rank: 3, color: DIM,    text: "内存排序溢出 — sort_area_size 偏小" },
];

export default function LlmTerminal() {
  return (
    <TerminalMockup>
      {/* Prompt */}
      <div>
        <span style={{ color: GREEN }}>opendb&gt;</span>{" "}
        <span>/llm 分析慢查询根因</span>
      </div>

      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />

      {/* Reasoning rounds */}
      {ROUNDS.map((r) => (
        <div key={r.id} style={{ marginBottom: "0.15rem" }}>
          <span style={{ color: YELLOW }}>⟳  {r.id}</span>{" "}
          <span style={{ color: DIM }}>{r.call}</span>
          <div style={{ paddingLeft: "2rem", color: "#edecec" }}>→ {r.result}</div>
        </div>
      ))}

      {/* Completion */}
      <div style={{ color: GREEN, margin: "0.4rem 0" }}>
        ✓  分析完成 · 找到 3 个问题
      </div>

      {/* Ranked problems */}
      {PROBLEMS.map((p) => (
        <div key={p.rank} style={{ display: "flex", gap: "0.5rem" }}>
          <span style={{ color: p.color, width: "1.5rem", flexShrink: 0 }}>#{p.rank}</span>
          <span style={{ color: "#edecec" }}>{p.text}</span>
        </div>
      ))}

      {/* Fix SQL */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div
        style={{
          borderLeft: "3px solid #7ee787",
          paddingLeft: "0.75rem",
        }}
      >
        <div style={{ color: GREEN }}>修复 SQL (2 条)</div>
        <div style={{ color: "#edecec" }}>
          CREATE INDEX CONCURRENTLY idx_orders_status
        </div>
        <div style={{ color: "#edecec" }}>
          {'  ON orders(status);'}
        </div>
        <div style={{ color: DIM, marginTop: "0.25rem" }}>
          ALTER SYSTEM SET sort_area_size=524288;
        </div>
      </div>
    </TerminalMockup>
  );
}
