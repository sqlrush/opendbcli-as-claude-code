import { ReactNode } from "react";

interface TerminalMockupProps {
  title?: string;
  children: ReactNode;
  className?: string;
}

export default function TerminalMockup({
  title = "opendb — ORCLCDB@prod-db-01",
  children,
  className = "",
}: TerminalMockupProps) {
  return (
    <div
      className={`overflow-hidden rounded-xl ${className}`}
      style={{
        background: "var(--color-terminal-bg, #1a1a18)",
        border: "1px solid rgba(0, 0, 0, 0.1)",
        boxShadow: "0 24px 80px rgba(38, 37, 30, 0.12), 0 4px 16px rgba(38, 37, 30, 0.06)",
      }}
    >
      {/* Title bar */}
      <div
        className="flex items-center px-3.5 py-2.5"
        style={{
          background: "rgba(255, 255, 255, 0.04)",
          borderBottom: "1px solid var(--terminal-border)",
        }}
      >
        <div className="flex gap-1.5">
          <span className="h-2.5 w-2.5 rounded-full" style={{ background: "#ff5f57" }} />
          <span className="h-2.5 w-2.5 rounded-full" style={{ background: "#febc2e" }} />
          <span className="h-2.5 w-2.5 rounded-full" style={{ background: "#28c840" }} />
        </div>
        <span
          className="flex-1 text-center font-mono text-xs"
          style={{ color: "rgba(237, 236, 236, 0.3)" }}
        >
          {title}
        </span>
      </div>
      {/* Body */}
      <div className="px-5 py-4 font-mono text-sm leading-[1.8]" style={{ color: "#edecec" }}>
        {children}
      </div>
    </div>
  );
}
