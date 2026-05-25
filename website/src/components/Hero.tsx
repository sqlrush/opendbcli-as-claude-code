"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import TypewriterTerminal from "./TypewriterTerminal";

export default function Hero() {
  const t = useTranslations("hero");
  const [os, setOs] = useState<"mac" | "linux">("mac");
  const [copied, setCopied] = useState(false);

  const installCmd =
    "$ curl -fsSL https://www.opendbcli.org/install.sh | bash";

  function handleCopy() {
    const text = installCmd.replace(/^\$ /, "");
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      });
    } else {
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      document.body.removeChild(textarea);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }

  return (
    <section
      style={{
        position: "relative",
        overflow: "hidden",
        padding: "5rem 1.5rem 4rem",
        textAlign: "center",
      }}
    >
      {/* Radial gradient glow */}
      <div
        aria-hidden
        style={{
          position: "absolute",
          top: "-120px",
          left: "50%",
          transform: "translateX(-50%)",
          width: "800px",
          height: "400px",
          background:
            "radial-gradient(ellipse at center, rgba(255,107,53,0.08) 0%, transparent 70%)",
          pointerEvents: "none",
        }}
      />

      <div style={{ position: "relative", maxWidth: "860px", margin: "0 auto" }}>
        {/* Badge */}
        <div style={{ marginBottom: "1.5rem" }}>
          <span
            style={{
              display: "inline-block",
              fontSize: "0.875rem",
              fontWeight: 500,
              borderRadius: "9999px",
              padding: "0.35rem 0.9rem",
              background: "rgba(38,37,30,0.06)",
              border: "1px solid rgba(38,37,30,0.12)",
              color: "#26251e",
              letterSpacing: "-0.01em",
            }}
          >
            {t("badge")}
          </span>
        </div>

        {/* Title */}
        <h1
          style={{
            fontSize: "4rem",
            fontWeight: 800,
            letterSpacing: "-0.156rem",
            lineHeight: 1.1,
            margin: "0 0 1.25rem",
            color: "#26251e",
          }}
        >
          {t("title1")}
          <br />
          {t("title2")}
        </h1>

        {/* Subtitle */}
        <p
          style={{
            fontSize: "1.0625rem",
            color: "var(--text-secondary)",
            margin: "0 auto 2.5rem",
            maxWidth: "600px",
            lineHeight: 1.6,
          }}
        >
          {t("subtitle")}
        </p>

        {/* Install block */}
        <div
          id="install"
          style={{
            maxWidth: "560px",
            margin: "0 auto 3rem",
            textAlign: "left",
          }}
        >
          {/* Label row */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
              marginBottom: "0.75rem",
            }}
          >
            <span style={{ fontWeight: 700, fontSize: "0.9375rem" }}>
              {t("quickInstall")}
            </span>
            <button
              onClick={() => setOs("mac")}
              style={{
                fontSize: "0.8125rem",
                padding: "0.2rem 0.65rem",
                borderRadius: "0.375rem",
                border: "1px solid transparent",
                cursor: "pointer",
                background: os === "mac" ? "#26251e" : "transparent",
                color: os === "mac" ? "#f7f7f4" : "var(--text-secondary)",
                fontWeight: os === "mac" ? 500 : 400,
              }}
            >
              macOS
            </button>
            <button
              onClick={() => setOs("linux")}
              style={{
                fontSize: "0.8125rem",
                padding: "0.2rem 0.65rem",
                borderRadius: "0.375rem",
                border: "1px solid rgba(38,37,30,0.15)",
                cursor: "pointer",
                background: os === "linux" ? "#26251e" : "transparent",
                color: os === "linux" ? "#f7f7f4" : "var(--text-secondary)",
                fontWeight: os === "linux" ? 500 : 400,
              }}
            >
              Linux
            </button>
          </div>

          {/* Command block */}
          <div
            style={{
              background: "#1a1a18",
              borderRadius: "0.625rem",
              padding: "0.75rem 1rem",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: "0.75rem",
              border: "1px solid rgba(237,236,236,0.08)",
            }}
          >
            <code
              style={{
                fontFamily: "'JetBrains Mono', monospace",
                fontSize: "0.875rem",
                color: "#edecec",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {installCmd}
            </code>
            <button
              onClick={handleCopy}
              style={{
                flexShrink: 0,
                fontSize: "0.8125rem",
                padding: "0.25rem 0.65rem",
                borderRadius: "0.375rem",
                border: "1px solid rgba(237,236,236,0.2)",
                background: "rgba(237,236,236,0.08)",
                color: copied ? "#7ee787" : "rgba(237,236,236,0.7)",
                cursor: "pointer",
                whiteSpace: "nowrap",
                fontFamily: "inherit",
              }}
            >
              {copied ? t("copied") : t("copy")}
            </button>
          </div>

          {/* GitHub button */}
          <div style={{ marginTop: "0.875rem", textAlign: "center" }}>
            <a
              href="https://github.com/sqlrush/opendbcli-as-claude-code"
              target="_blank"
              rel="noopener noreferrer"
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "0.375rem",
                fontSize: "0.875rem",
                color: "var(--text-secondary)",
                textDecoration: "none",
                padding: "0.35rem 0.75rem",
                borderRadius: "0.5rem",
                border: "1px solid var(--border)",
                background: "var(--fill)",
              }}
            >
              GitHub ★ →
            </a>
          </div>
        </div>

        {/* Typewriter Terminal */}
        <TypewriterTerminal />
      </div>
    </section>
  );
}
