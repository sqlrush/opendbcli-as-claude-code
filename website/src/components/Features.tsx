"use client";

import { useTranslations } from "next-intl";
import ScrollReveal from "./ScrollReveal";
import FeatureRow from "./FeatureRow";
import HealthTerminal from "./feature-terminals/HealthTerminal";
import DbtopTerminal from "./feature-terminals/DbtopTerminal";
import RuleTerminal from "./feature-terminals/RuleTerminal";
import LlmTerminal from "./feature-terminals/LlmTerminal";
import SentinelTerminal from "./feature-terminals/SentinelTerminal";

export default function Features() {
  const t = useTranslations("features");

  return (
    <section
      id="features"
      style={{ padding: "5rem 1.5rem", maxWidth: "1100px", margin: "0 auto" }}
    >
      {/* Section heading */}
      <ScrollReveal>
        <div style={{ textAlign: "center", marginBottom: "4rem" }}>
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

      {/* Feature rows */}
      <div style={{ display: "flex", flexDirection: "column", gap: "6rem" }}>
        {/* Health */}
        <FeatureRow
          tag="/health"
          title={t("health.title")}
          description={t("health.desc")}
          stats={[
            { value: "7", label: t("health.stat1Label") },
            { value: "20+", label: t("health.stat2Label") },
            { value: "<5s", label: t("health.stat3Label") },
          ]}
          terminal={<HealthTerminal />}
        />

        {/* Dbtop — reversed */}
        <FeatureRow
          tag="/dbtop"
          title={t("dbtop.title")}
          description={t("dbtop.desc")}
          stats={[
            { value: "1s", label: t("dbtop.stat1Label") },
            { value: "12+", label: t("dbtop.stat2Label") },
          ]}
          terminal={<DbtopTerminal />}
          reverse
        />

        {/* Rule */}
        <FeatureRow
          tag="/rule"
          title={t("rule.title")}
          description={t("rule.desc")}
          stats={[
            { value: "273", label: t("rule.stat1Label") },
            { value: "2.7s", label: t("rule.stat2Label") },
            { value: "0", label: t("rule.stat3Label") },
          ]}
          terminal={<RuleTerminal />}
        />

        {/* LLM — reversed */}
        <FeatureRow
          tag="/llm"
          title={t("llm.title")}
          description={t("llm.desc")}
          stats={[
            { value: "20", label: t("llm.stat1Label") },
            { value: "60+", label: t("llm.stat2Label") },
          ]}
          terminal={<LlmTerminal />}
          reverse
        />

        {/* Sentinel */}
        <FeatureRow
          tag="/sentinel"
          title={t("sentinel.title")}
          description={t("sentinel.desc")}
          stats={[
            { value: "48", label: t("sentinel.stat1Label") },
            { value: "3σ", label: t("sentinel.stat2Label") },
            { value: "24/7", label: t("sentinel.stat3Label") },
          ]}
          terminal={<SentinelTerminal />}
        />
      </div>
    </section>
  );
}
