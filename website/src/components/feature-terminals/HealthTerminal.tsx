import TerminalMockup from "../TerminalMockup";

const DIM = "rgba(237,236,236,0.35)";
const GREEN = "#7ee787";
const YELLOW = "#ffc61c";
const RED = "#ff6b35";

interface DimRow {
  icon: string;
  name: string;
  status: "ok" | "warn" | "error";
  note: string;
}

const ROWS: DimRow[] = [
  { icon: "✓", name: "Instance  ", status: "ok",    note: "OPEN · 正常运行" },
  { icon: "✗", name: "Storage   ", status: "error",  note: "表空间 USERS 已用 94%" },
  { icon: "⚠", name: "Sessions  ", status: "warn",   note: "活跃 142 / 最大 300" },
  { icon: "✓", name: "Memory    ", status: "ok",    note: "SGA 16G · PGA 4.2G" },
  { icon: "⚠", name: "Logs      ", status: "warn",   note: "归档日志量 2× 基线" },
  { icon: "✓", name: "Backup    ", status: "ok",    note: "最近备份 6h 前" },
  { icon: "✓", name: "Security  ", status: "ok",    note: "审计已启用" },
];

function iconColor(status: DimRow["status"]) {
  if (status === "ok") return GREEN;
  if (status === "warn") return YELLOW;
  return RED;
}

export default function HealthTerminal() {
  return (
    <TerminalMockup>
      {/* Prompt */}
      <div>
        <span style={{ color: GREEN }}>opendb&gt;</span>{" "}
        <span>/health</span>
      </div>

      {/* Separator */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />

      {/* Header */}
      <div style={{ color: DIM, marginBottom: "0.25rem" }}>
        {"── Health Report "}
        <span style={{ color: "#edecec" }}>ORCLCDB</span>
        {" ─────────────"}
      </div>

      {/* Dimension rows */}
      {ROWS.map((row) => (
        <div key={row.name} style={{ display: "flex", gap: "0.5rem" }}>
          <span style={{ color: iconColor(row.status), width: "1rem", flexShrink: 0 }}>
            {row.icon}
          </span>
          <span style={{ color: DIM, width: "6rem", flexShrink: 0 }}>{row.name}</span>
          <span style={{ color: "#edecec" }}>{row.note}</span>
        </div>
      ))}

      {/* Separator + summary */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div>
        <span style={{ color: RED }}>✗</span>{" "}
        <span style={{ color: "#edecec" }}>发现 3 个问题</span>
        <span style={{ color: DIM }}> · 运行 /rule 获取修复建议</span>
      </div>
    </TerminalMockup>
  );
}
