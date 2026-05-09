import TerminalMockup from "../TerminalMockup";

const DIM = "rgba(237,236,236,0.35)";
const GREEN = "#7ee787";
const YELLOW = "#ffc61c";
const ORANGE = "#ff6b35";

export default function RuleTerminal() {
  return (
    <TerminalMockup>
      {/* Prompt */}
      <div>
        <span style={{ color: GREEN }}>opendb&gt;</span>{" "}
        <span>/rule</span>
      </div>

      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />

      {/* Category */}
      <div style={{ color: DIM, marginBottom: "0.25rem" }}>
        Category: <span style={{ color: "#edecec" }}>I/O Performance</span>
      </div>

      {/* Root cause */}
      <div
        style={{
          borderLeft: "3px solid #ff6b35",
          paddingLeft: "0.75rem",
          margin: "0.5rem 0",
        }}
      >
        <div style={{ color: ORANGE }}>根因  全表扫描 → I/O 冲高</div>
        <div>
          <span style={{ color: DIM }}>严重度  </span>
          <span style={{ color: ORANGE }}>■■■</span>
          <span style={{ color: DIM }}>□</span>
        </div>
        <div>
          <span style={{ color: DIM }}>置信度  </span>
          <span style={{ color: YELLOW }}>78%</span>
        </div>
      </div>

      {/* Evidence chain */}
      <div style={{ color: DIM, marginBottom: "0.25rem" }}>证据链</div>
      {[
        "· orders.status 字段无索引",
        "· 全表扫描 12M 行 × 每秒 340 次",
        "· 物理读 82K blk/s (阈值 20K)",
        "· 等待事件 db file sequential read 42%",
      ].map((ev) => (
        <div key={ev} style={{ color: "#edecec", paddingLeft: "0.5rem" }}>
          {ev}
        </div>
      ))}

      {/* Fix */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div
        style={{
          borderLeft: "3px solid #7ee787",
          paddingLeft: "0.75rem",
        }}
      >
        <div style={{ color: GREEN }}>建议修复</div>
        <div style={{ color: "#edecec", whiteSpace: "pre" }}>
          {`CREATE INDEX CONCURRENTLY
  idx_orders_status ON orders(status);`}
        </div>
      </div>
    </TerminalMockup>
  );
}
