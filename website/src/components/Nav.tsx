"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

export default function Nav() {
  const t = useTranslations("nav");
  const pathname = usePathname();
  const router = useRouter();

  const currentLocale = pathname.startsWith("/zh") ? "zh" : "en";
  const otherLocale = currentLocale === "zh" ? "en" : "zh";
  const otherLocaleLabel = currentLocale === "zh" ? "EN" : "中文";

  function handleLocaleSwitch() {
    const newPath = pathname.replace(`/${currentLocale}`, `/${otherLocale}`);
    router.push(newPath);
  }

  return (
    <nav
      style={{
        position: "sticky",
        top: 0,
        zIndex: 50,
        backgroundColor: "rgba(247, 247, 244, 0.85)",
        borderBottom: "1px solid var(--border)",
        backdropFilter: "blur(12px)",
        WebkitBackdropFilter: "blur(12px)",
      }}
    >
      <div
        style={{
          maxWidth: "1200px",
          margin: "0 auto",
          padding: "0 1.5rem",
          height: "56px",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        {/* Left side: logo + nav links */}
        <div style={{ display: "flex", alignItems: "center", gap: "2rem" }}>
          <Link
            href={`/${currentLocale}`}
            style={{
              fontWeight: 700,
              letterSpacing: "-0.03em",
              fontSize: "1.0625rem",
              color: "#26251e",
              textDecoration: "none",
            }}
          >
            OpenDB
          </Link>

          <div style={{ display: "flex", alignItems: "center", gap: "1.5rem" }}>
            <Link
              href={`/${currentLocale}#features`}
              style={{
                fontSize: "0.875rem",
                color: "var(--text-secondary)",
                textDecoration: "none",
              }}
            >
              {t("features")}
            </Link>

            <Link
              href="https://github.com/sqlrush/opendbcli-as-claude-code"
              target="_blank"
              rel="noopener noreferrer"
              style={{
                fontSize: "0.875rem",
                color: "var(--text-secondary)",
                textDecoration: "none",
              }}
            >
              {t("github")}
            </Link>
          </div>
        </div>

        {/* Right side: locale switcher + CTA */}
        <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
          <button
            onClick={handleLocaleSwitch}
            style={{
              fontSize: "0.8125rem",
              color: "var(--text-secondary)",
              background: "none",
              border: "none",
              cursor: "pointer",
              padding: "0.25rem 0.5rem",
              borderRadius: "0.375rem",
            }}
          >
            {otherLocaleLabel}
          </button>

          <Link
            href={`/${currentLocale}#install`}
            style={{
              fontSize: "0.875rem",
              fontWeight: 500,
              backgroundColor: "#26251e",
              color: "#f7f7f4",
              textDecoration: "none",
              padding: "0.4375rem 1rem",
              borderRadius: "0.5rem",
              whiteSpace: "nowrap",
            }}
          >
            {t("getStarted")}
          </Link>
        </div>
      </div>
    </nav>
  );
}
