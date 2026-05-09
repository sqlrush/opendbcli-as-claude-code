import TerminalMockup from "../TerminalMockup";

const DIM = "rgba(237,236,236,0.35)";
const GREEN = "#7ee787";
const YELLOW = "#ffc61c";
const ORANGE = "#ff6b35";
const BLUE = "#79c0ff";

const WAIT_EVENTS = [
  { name: "db file sequential read", pct: 42, color: ORANGE },
  { name: "log file sync          ", pct: 28, color: YELLOW },
  { name: "buffer busy waits      ", pct: 18, color: YELLOW },
  { name: "CPU                    ", pct: 12, color: GREEN },
];

const SESSIONS = [
  { sid: "142", user: "APPSVC", sql: "SELECT * FROM orders WHERE sta...", wait: "db file seq" },
  { sid: "209", user: "BATCH ", sql: "UPDATE inventory SET qty=qty-1 ...", wait: "log sync" },
  { sid: "317", user: "REPORT", sql: "SELECT COUNT(*) FROM audit_log ...", wait: "buffer busy" },
];

function bar(pct: number) {
  const filled = Math.round(pct / 7);
  return "▇".repeat(filled) + " ".repeat(14 - filled);
}

export default function DbtopTerminal() {
  return (
    <TerminalMockup>
      {/* Prompt */}
      <div>
        <span style={{ color: GREEN }}>opendb&gt;</span>{" "}
        <span>/dbtop</span>
      </div>

      {/* Status line */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div style={{ display: "flex", gap: "1rem" }}>
        <span style={{ color: "#edecec", fontWeight: 600 }}>ORCLCDB</span>
        <span style={{ color: YELLOW }}>● 警告</span>
        <span style={{ color: DIM }}>刷新 1s · {new Date().toLocaleTimeString("zh-CN", { hour12: false })}</span>
      </div>

      {/* Metrics row */}
      <div style={{ display: "flex", gap: "1.5rem", margin: "0.4rem 0", flexWrap: "wrap" }}>
        <span><span style={{ color: DIM }}>SGA </span><span style={{ color: BLUE }}>16G</span></span>
        <span><span style={{ color: DIM }}>PGA </span><span style={{ color: BLUE }}>4.2G</span></span>
        <span><span style={{ color: DIM }}>db% </span><span style={{ color: ORANGE }}>88%</span></span>
        <span><span style={{ color: DIM }}>TPS </span><span style={{ color: GREEN }}>2,841</span></span>
        <span><span style={{ color: DIM }}>QPS </span><span style={{ color: GREEN }}>9,120</span></span>
      </div>

      {/* Wait events */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div style={{ color: DIM, marginBottom: "0.25rem" }}>Wait Events</div>
      {WAIT_EVENTS.map((ev) => (
        <div key={ev.name} style={{ display: "flex", gap: "0.5rem", alignItems: "center" }}>
          <span style={{ color: DIM, width: "22ch", flexShrink: 0 }}>{ev.name}</span>
          <span style={{ color: ev.color }}>{bar(ev.pct)}</span>
          <span style={{ color: "#edecec", width: "3ch", textAlign: "right" }}>{ev.pct}%</span>
        </div>
      ))}

      {/* Active sessions */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div style={{ color: DIM, marginBottom: "0.25rem" }}>Active Sessions (3)</div>
      {SESSIONS.map((s) => (
        <div key={s.sid} style={{ display: "flex", gap: "0.5rem" }}>
          <span style={{ color: BLUE, width: "3ch", flexShrink: 0 }}>{s.sid}</span>
          <span style={{ color: GREEN, width: "7ch", flexShrink: 0 }}>{s.user}</span>
          <span style={{ color: "#edecec", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.sql}</span>
          <span style={{ color: YELLOW, width: "11ch", textAlign: "right", flexShrink: 0 }}>{s.wait}</span>
        </div>
      ))}
    </TerminalMockup>
  );
}
