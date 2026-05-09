"use client";

import { useEffect, useState } from "react";
import TerminalMockup from "./TerminalMockup";

const COMMAND = "/llm 数据库为什么慢？";
const CHAR_DELAY = 50;
const LINE_DELAY = 300;
const LOOP_PAUSE = 4000;

type OutputLine =
  | { kind: "separator" }
  | { kind: "progress"; text: string }
  | { kind: "success"; text: string }
  | { kind: "root-cause"; title: string; severity: string; detail: string }
  | { kind: "fix"; label: string; sql: string };

const OUTPUT_LINES: OutputLine[] = [
  { kind: "separator" },
  { kind: "progress", text: "⟳  R1 采集等待事件 / 活跃会话..." },
  { kind: "progress", text: "⟳  R2 分析 SQL 执行计划..." },
  { kind: "progress", text: "⟳  R3 检查锁与 I/O 分布..." },
  { kind: "success", text: "✓  诊断完成 · 3 个问题已发现" },
  {
    kind: "root-cause",
    title: "根因  全表扫描导致 I/O 冲高",
    severity: "严重度  ■■■□",
    detail: "orders.status 无索引，每次查询扫描 12M 行",
  },
  {
    kind: "fix",
    label: "修复",
    sql: "CREATE INDEX CONCURRENTLY idx_orders_status\n  ON orders(status);",
  },
];

export default function TypewriterTerminal() {
  const [typedChars, setTypedChars] = useState(0);
  const [visibleLines, setVisibleLines] = useState(0);
  const [phase, setPhase] = useState<"typing" | "output" | "done">("typing");

  useEffect(() => {
    if (phase === "typing") {
      if (typedChars < COMMAND.length) {
        const t = setTimeout(() => setTypedChars((n) => n + 1), CHAR_DELAY);
        return () => clearTimeout(t);
      } else {
        const t = setTimeout(() => setPhase("output"), LINE_DELAY);
        return () => clearTimeout(t);
      }
    }

    if (phase === "output") {
      if (visibleLines < OUTPUT_LINES.length) {
        const t = setTimeout(
          () => setVisibleLines((n) => n + 1),
          LINE_DELAY
        );
        return () => clearTimeout(t);
      } else {
        const t = setTimeout(() => setPhase("done"), LOOP_PAUSE);
        return () => clearTimeout(t);
      }
    }

    if (phase === "done") {
      const t = setTimeout(() => {
        setTypedChars(0);
        setVisibleLines(0);
        setPhase("typing");
      }, 100);
      return () => clearTimeout(t);
    }
  }, [phase, typedChars, visibleLines]);

  return (
    <TerminalMockup>
      {/* Prompt line */}
      <div className="flex items-center gap-2">
        <span style={{ color: "#7ee787" }}>opendb&gt;</span>
        <span style={{ color: "#edecec" }}>{COMMAND.slice(0, typedChars)}</span>
        {phase === "typing" && (
          <span
            style={{
              display: "inline-block",
              width: "0.55em",
              height: "1.1em",
              background: "#edecec",
              verticalAlign: "text-bottom",
              animation: "blink 1s step-end infinite",
            }}
          />
        )}
      </div>

      {/* Output lines */}
      {OUTPUT_LINES.slice(0, visibleLines).map((line, i) => (
        <OutputLineEl key={i} line={line} />
      ))}

      <style>{`@keyframes blink { 0%,100%{opacity:1} 50%{opacity:0} }`}</style>
    </TerminalMockup>
  );
}

function OutputLineEl({ line }: { line: OutputLine }) {
  if (line.kind === "separator") {
    return (
      <div
        style={{
          borderTop: "1px solid rgba(237,236,236,0.15)",
          margin: "0.5rem 0",
        }}
      />
    );
  }

  if (line.kind === "progress") {
    return (
      <div style={{ color: "#ffc61c" }} className="font-mono text-sm">
        {line.text}
      </div>
    );
  }

  if (line.kind === "success") {
    return (
      <div style={{ color: "#7ee787" }} className="font-mono text-sm">
        {line.text}
      </div>
    );
  }

  if (line.kind === "root-cause") {
    return (
      <div
        className="font-mono text-sm"
        style={{
          borderLeft: "3px solid #ff6b35",
          paddingLeft: "0.75rem",
          margin: "0.5rem 0",
        }}
      >
        <div style={{ color: "#ff6b35" }}>{line.title}</div>
        <div style={{ color: "rgba(237,236,236,0.55)" }}>{line.severity}</div>
        <div style={{ color: "#edecec" }}>{line.detail}</div>
      </div>
    );
  }

  if (line.kind === "fix") {
    return (
      <div
        className="font-mono text-sm"
        style={{
          borderLeft: "3px solid #7ee787",
          paddingLeft: "0.75rem",
          margin: "0.5rem 0",
        }}
      >
        <div style={{ color: "#7ee787" }}>{line.label}</div>
        <div style={{ color: "#edecec", whiteSpace: "pre" }}>{line.sql}</div>
      </div>
    );
  }

  return null;
}
