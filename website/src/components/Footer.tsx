import { useTranslations } from "next-intl";

export default function Footer() {
  const t = useTranslations("footer");

  return (
    <footer
      style={{
        borderTop: "1px solid var(--border)",
        padding: "2rem 1.5rem",
      }}
    >
      <div
        style={{
          maxWidth: "1100px",
          margin: "0 auto",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          flexWrap: "wrap",
          gap: "1rem",
        }}
      >
        {/* Left */}
        <div>
          <span style={{ fontWeight: 700, color: "#26251e" }}>OpenDB</span>
          <span
            style={{
              marginLeft: "0.75rem",
              fontSize: "0.875rem",
              color: "var(--text-secondary)",
            }}
          >
            {t("tagline")}
          </span>
        </div>

        {/* Right: links */}
        <div style={{ display: "flex", alignItems: "center", gap: "1.5rem" }}>
          <a
            href="#"
            style={{
              fontSize: "0.875rem",
              color: "var(--text-secondary)",
              textDecoration: "none",
            }}
          >
            {t("docs")}
          </a>
          <a
            href="https://github.com/sqlrush/opendbcli-as-claude-code"
            target="_blank"
            rel="noopener noreferrer"
            style={{
              fontSize: "0.875rem",
              color: "var(--text-secondary)",
              textDecoration: "none",
            }}
          >
            GitHub
          </a>
          <a
            href="https://github.com/sqlrush/opendbcli-as-claude-code/issues"
            target="_blank"
            rel="noopener noreferrer"
            style={{
              fontSize: "0.875rem",
              color: "var(--text-secondary)",
              textDecoration: "none",
            }}
          >
            {t("issues")}
          </a>
          <a
            href="https://github.com/sqlrush/opendbcli-as-claude-code/blob/main/LICENSE"
            target="_blank"
            rel="noopener noreferrer"
            style={{
              fontSize: "0.875rem",
              color: "var(--text-secondary)",
              textDecoration: "none",
            }}
          >
            {t("license")}
          </a>
        </div>
      </div>
    </footer>
  );
}
