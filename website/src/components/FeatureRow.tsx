import { ReactNode } from "react";
import ScrollReveal from "./ScrollReveal";

interface Stat {
  value: string;
  label: string;
}

interface FeatureRowProps {
  tag: string;
  title: string;
  description: string;
  stats: Stat[];
  terminal: ReactNode;
  reverse?: boolean;
}

export default function FeatureRow({
  tag,
  title,
  description,
  stats,
  terminal,
  reverse = false,
}: FeatureRowProps) {
  const infoSide = (
    <ScrollReveal delay={0}>
      <div>
        {/* Tag */}
        <div
          style={{
            display: "inline-block",
            fontFamily: "'JetBrains Mono', monospace",
            fontSize: "0.75rem",
            fontWeight: 500,
            padding: "0.25rem 0.65rem",
            borderRadius: "0.25rem",
            background: "rgba(38,37,30,0.06)",
            color: "rgba(38,37,30,0.6)",
            marginBottom: "1rem",
            letterSpacing: "0.04em",
          }}
        >
          {tag}
        </div>

        {/* Title */}
        <h3
          style={{
            fontSize: "1.625rem",
            fontWeight: 700,
            margin: "0 0 0.75rem",
            color: "#26251e",
            letterSpacing: "-0.03em",
          }}
        >
          {title}
        </h3>

        {/* Description */}
        <p
          style={{
            fontSize: "0.9375rem",
            color: "var(--text-secondary)",
            lineHeight: 1.7,
            margin: "0 0 1.5rem",
          }}
        >
          {description}
        </p>

        {/* Stats */}
        <div style={{ display: "flex", gap: "2rem", flexWrap: "wrap" }}>
          {stats.map((s) => (
            <div key={s.label}>
              <div
                style={{
                  fontSize: "2rem",
                  fontWeight: 700,
                  color: "#26251e",
                  lineHeight: 1.1,
                  letterSpacing: "-0.04em",
                }}
              >
                {s.value}
              </div>
              <div
                style={{
                  fontSize: "0.75rem",
                  color: "var(--text-secondary)",
                  marginTop: "0.2rem",
                }}
              >
                {s.label}
              </div>
            </div>
          ))}
        </div>
      </div>
    </ScrollReveal>
  );

  const terminalSide = (
    <ScrollReveal delay={0.1}>
      <div
        style={{
          background: "#1a1a18",
          borderRadius: "0.875rem",
          overflow: "hidden",
          border: "1px solid rgba(0,0,0,0.1)",
          boxShadow: "0 16px 48px rgba(38,37,30,0.1)",
        }}
      >
        {terminal}
      </div>
    </ScrollReveal>
  );

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "1fr 1fr",
        gap: "3.5rem",
        alignItems: "center",
        direction: reverse ? "rtl" : "ltr",
      }}
    >
      <div style={{ direction: "ltr" }}>{reverse ? terminalSide : infoSide}</div>
      <div style={{ direction: "ltr" }}>{reverse ? infoSide : terminalSide}</div>
    </div>
  );
}
