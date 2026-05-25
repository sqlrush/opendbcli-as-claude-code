"use client";

import { useTranslations } from "next-intl";
import ScrollReveal from "./ScrollReveal";

function TerminalSnippet({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        background: "#1a1a18",
        borderRadius: "0.5rem",
        padding: "0.75rem 1rem",
        marginTop: "1rem",
        fontFamily: "'JetBrains Mono', monospace",
        fontSize: "0.875rem",
        lineHeight: 1.7,
        color: "#edecec",
        border: "1px solid rgba(237,236,236,0.08)",
      }}
    >
      {children}
    </div>
  );
}

function ModeCard({
  label,
  title,
  desc,
  terminal,
}: {
  label: string;
  title: string;
  desc: string;
  terminal: React.ReactNode;
}) {
  return (
    <div
      style={{
        background: "#fff",
        borderRadius: "0.875rem",
        border: "1px solid rgba(38,37,30,0.09)",
        padding: "1.5rem",
        transition: "box-shadow 0.2s",
      }}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLDivElement).style.boxShadow =
          "0 8px 32px rgba(38,37,30,0.1)";
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLDivElement).style.boxShadow = "none";
      }}
    >
      <div
        style={{
          display: "inline-block",
          fontFamily: "'JetBrains Mono', monospace",
          fontSize: "0.6875rem",
          fontWeight: 500,
          padding: "0.2rem 0.55rem",
          borderRadius: "0.25rem",
          background: "rgba(38,37,30,0.06)",
          color: "rgba(38,37,30,0.6)",
          marginBottom: "0.75rem",
          letterSpacing: "0.04em",
        }}
      >
        {label}
      </div>
      <h3
        style={{
          fontSize: "1.125rem",
          fontWeight: 700,
          margin: "0 0 0.5rem",
          color: "#26251e",
        }}
      >
        {title}
      </h3>
      <p
        style={{
          fontSize: "0.875rem",
          color: "var(--text-secondary)",
          margin: 0,
          lineHeight: 1.6,
        }}
      >
        {desc}
      </p>
      {terminal}
    </div>
  );
}

export default function ThreeModes() {
  const t = useTranslations("modes");

  return (
    <section style={{ padding: "5rem 1.5rem", maxWidth: "1100px", margin: "0 auto" }}>
      <ScrollReveal>
        <div style={{ textAlign: "center", marginBottom: "3rem" }}>
          <h2
            style={{
              fontSize: "2.5rem",
              fontWeight: 800,
              letterSpacing: "-0.05em",
              margin: "0 0 0.75rem",
              color: "#26251e",
            }}
          >
            {t("title")}
          </h2>
          <p
            style={{
              fontSize: "1.0625rem",
              color: "var(--text-secondary)",
              maxWidth: "520px",
              margin: "0 auto",
              lineHeight: 1.6,
            }}
          >
            {t("subtitle")}
          </p>
        </div>
      </ScrollReveal>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(3, 1fr)",
          gap: "1rem",
        }}
      >
        <ScrollReveal delay={0}>
          <ModeCard
            label={t("slash.label")}
            title={t("slash.title")}
            desc={t("slash.desc")}
            terminal={
              <TerminalSnippet>
                <div>
                  <span style={{ color: "#7ee787" }}>opendb&gt;</span>{" "}
                  <span>/health</span>
                </div>
                <div>
                  <span style={{ color: "#7ee787" }}>opendb&gt;</span>{" "}
                  <span>/dbtop</span>
                </div>
                <div>
                  <span style={{ color: "#7ee787" }}>opendb&gt;</span>{" "}
                  <span>/llm 分析慢查询</span>
                </div>
              </TerminalSnippet>
            }
          />
        </ScrollReveal>

        <ScrollReveal delay={0.1}>
          <ModeCard
            label={t("sql.label")}
            title={t("sql.title")}
            desc={t("sql.desc")}
            terminal={
              <TerminalSnippet>
                <div>
                  <span style={{ color: "#7ee787" }}>opendb&gt;</span>
                </div>
                <div style={{ paddingLeft: "0.5rem", color: "#79c0ff" }}>
                  SELECT event, count, avg_wait
                </div>
                <div style={{ paddingLeft: "0.5rem", color: "#79c0ff" }}>
                  FROM v$system_event
                </div>
                <div style={{ paddingLeft: "0.5rem", color: "#79c0ff" }}>
                  ORDER BY total_waits DESC;
                </div>
              </TerminalSnippet>
            }
          />
        </ScrollReveal>

        <ScrollReveal delay={0.2}>
          <ModeCard
            label={t("natural.label")}
            title={t("natural.title")}
            desc={t("natural.desc")}
            terminal={
              <TerminalSnippet>
                <div>
                  <span style={{ color: "#7ee787" }}>opendb&gt;</span>{" "}
                  <span>为什么今天早上 9 点数据库很慢？</span>
                </div>
                <div style={{ color: "#ffc61c", marginTop: "0.25rem" }}>
                  ⟳  正在分析历史指标...
                </div>
              </TerminalSnippet>
            }
          />
        </ScrollReveal>
      </div>
    </section>
  );
}
