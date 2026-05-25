# OpenDB Landing Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Cursor.com-style product landing page for OpenDB at opendbcli.org with Next.js, i18n support, and animated terminal mockups.

**Architecture:** Next.js 14 App Router with SSR, next-intl for i18n (zh/en), Framer Motion for animations. Terminal mockups are React components with typewriter animation. Deployed via PM2 + Nginx on 43.110.57.194.

**Tech Stack:** Next.js 14, React 18, TypeScript, Framer Motion, next-intl, Tailwind CSS (utility-only, custom theme), PM2, Nginx

---

## File Structure

```
website/
├── package.json
├── next.config.ts
├── tailwind.config.ts
├── tsconfig.json
├── messages/
│   ├── zh.json                    # 中文翻译
│   └── en.json                    # English translations
├── src/
│   ├── i18n/
│   │   ├── request.ts             # next-intl server config
│   │   └── routing.ts             # locale routing config
│   ├── middleware.ts               # i18n middleware
│   ├── app/
│   │   ├── globals.css            # CSS variables + base styles
│   │   ├── layout.tsx             # Root layout (fonts, metadata)
│   │   └── [locale]/
│   │       ├── layout.tsx         # Locale layout (next-intl provider)
│   │       └── page.tsx           # Main page (assembles sections)
│   └── components/
│       ├── Nav.tsx                 # Sticky navigation bar
│       ├── Hero.tsx                # Hero section (title + install + terminal)
│       ├── TerminalMockup.tsx      # Reusable terminal window chrome
│       ├── TypewriterTerminal.tsx  # Hero terminal with typewriter animation
│       ├── ThreeModes.tsx          # Three interaction modes cards
│       ├── Features.tsx            # Feature rows container
│       ├── FeatureRow.tsx          # Single feature row (info + terminal)
│       ├── feature-terminals/
│       │   ├── HealthTerminal.tsx  # /health output
│       │   ├── DbtopTerminal.tsx   # /dbtop output
│       │   ├── RuleTerminal.tsx    # /rule output
│       │   ├── LlmTerminal.tsx     # /llm output
│       │   └── SentinelTerminal.tsx # /sentinel output
│       ├── Footer.tsx              # Footer
│       └── ScrollReveal.tsx        # Scroll-triggered animation wrapper
├── public/
│   └── favicon.ico
└── ecosystem.config.js            # PM2 config
```

---

### Task 1: Next.js Project Scaffold

**Files:**
- Create: `website/package.json`
- Create: `website/next.config.ts`
- Create: `website/tailwind.config.ts`
- Create: `website/tsconfig.json`
- Create: `website/src/app/globals.css`
- Create: `website/src/app/layout.tsx`

- [ ] **Step 1: Initialize Next.js project**

```bash
cd /Users/yingjiewang/opendb
npx create-next-app@14 website --typescript --tailwind --eslint --app --src-dir --no-import-alias
```

- [ ] **Step 2: Install dependencies**

```bash
cd /Users/yingjiewang/opendb/website
npm install next-intl framer-motion
```

- [ ] **Step 3: Configure Tailwind with custom theme**

Replace `website/tailwind.config.ts`:

```typescript
import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        page: {
          bg: "#f7f7f4",
          surface: "#efeee9",
          elevated: "#ffffff",
        },
        text: {
          primary: "#26251e",
        },
        terminal: {
          bg: "#1a1a18",
          text: "#edecec",
          green: "#7ee787",
          yellow: "#ffc61c",
          orange: "#ff6b35",
          blue: "#79c0ff",
        },
      },
      fontFamily: {
        sans: ['"Noto Sans SC"', '"Inter"', "system-ui", "sans-serif"],
        mono: ['"JetBrains Mono"', "monospace"],
        inter: ['"Inter"', "system-ui", "sans-serif"],
      },
    },
  },
  plugins: [],
};

export default config;
```

- [ ] **Step 4: Write globals.css with CSS variables**

Replace `website/src/app/globals.css`:

```css
@import "tailwindcss";

@theme {
  --color-page-bg: #f7f7f4;
  --color-page-surface: #efeee9;
  --color-page-elevated: #ffffff;
  --color-text-primary: #26251e;
  --color-terminal-bg: #1a1a18;
  --color-terminal-text: #edecec;
  --color-terminal-green: #7ee787;
  --color-terminal-yellow: #ffc61c;
  --color-terminal-orange: #ff6b35;
  --color-terminal-blue: #79c0ff;
}

:root {
  --text-secondary: rgba(38, 37, 30, 0.55);
  --text-tertiary: rgba(38, 37, 30, 0.3);
  --border: rgba(38, 37, 30, 0.08);
  --fill: rgba(38, 37, 30, 0.04);
  --fill-hover: rgba(38, 37, 30, 0.07);
  --terminal-dim: rgba(237, 236, 236, 0.35);
  --terminal-border: rgba(237, 236, 236, 0.08);
}

@layer base {
  body {
    background-color: var(--color-page-bg);
    color: var(--color-text-primary);
    font-family: "Noto Sans SC", "Inter", system-ui, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
}
```

- [ ] **Step 5: Write root layout with font imports**

Replace `website/src/app/layout.tsx`:

```tsx
import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "OpenDB — DBCLI Agent as Claude Code",
  description:
    "像Claude Code一样交互的DBCLI Agent。最少交互，最优诊断。",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
```

- [ ] **Step 6: Verify dev server starts**

```bash
cd /Users/yingjiewang/opendb/website
npm run dev
```

Expected: Server starts on localhost:3000, no errors.

- [ ] **Step 7: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/
git commit -m "feat: scaffold Next.js landing page project with Tailwind + Cursor theme"
```

---

### Task 2: i18n Configuration

**Files:**
- Create: `website/src/i18n/routing.ts`
- Create: `website/src/i18n/request.ts`
- Create: `website/src/middleware.ts`
- Create: `website/messages/zh.json`
- Create: `website/messages/en.json`
- Create: `website/src/app/[locale]/layout.tsx`
- Create: `website/src/app/[locale]/page.tsx`

- [ ] **Step 1: Configure i18n routing**

Create `website/src/i18n/routing.ts`:

```typescript
import { defineRouting } from "next-intl/routing";

export const routing = defineRouting({
  locales: ["en", "zh"],
  defaultLocale: "en",
});
```

- [ ] **Step 2: Configure i18n server request**

Create `website/src/i18n/request.ts`:

```typescript
import { getRequestConfig } from "next-intl/server";
import { routing } from "./routing";

export default getRequestConfig(async ({ requestLocale }) => {
  let locale = await requestLocale;
  if (!locale || !routing.locales.includes(locale as "en" | "zh")) {
    locale = routing.defaultLocale;
  }
  return {
    locale,
    messages: (await import(`../../messages/${locale}.json`)).default,
  };
});
```

- [ ] **Step 3: Create middleware**

Create `website/src/middleware.ts`:

```typescript
import createMiddleware from "next-intl/middleware";
import { routing } from "./i18n/routing";

export default createMiddleware(routing);

export const config = {
  matcher: ["/((?!api|_next|_vercel|.*\\..*).*)"],
};
```

- [ ] **Step 4: Update next.config.ts for next-intl**

Replace `website/next.config.ts`:

```typescript
import createNextIntlPlugin from "next-intl/plugin";
import type { NextConfig } from "next";

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");

const nextConfig: NextConfig = {};

export default withNextIntl(nextConfig);
```

- [ ] **Step 5: Create Chinese translations**

Create `website/messages/zh.json`:

```json
{
  "nav": {
    "features": "功能",
    "docs": "文档",
    "github": "GitHub",
    "blog": "博客",
    "getStarted": "快速开始"
  },
  "hero": {
    "badge": "像Claude Code一样交互的DBCLI Agent",
    "title1": "最少交互",
    "title2": "最优诊断",
    "subtitle": "Oracle · MySQL · PostgreSQL — 斜杠命令交互 / 原生SQL交互 / 自然语言交互",
    "quickInstall": "快速安装",
    "copy": "复制",
    "copied": "已复制 ✓"
  },
  "modes": {
    "title": "三种模式，一个 Agent",
    "subtitle": "想怎么输入就怎么输入 — OpenDB 同时理解斜杠命令、原生 SQL 和自然语言。",
    "slash": {
      "label": "Slash Commands",
      "title": "60+ 内置 Skill",
      "desc": "复杂操作一行搞定。像 Claude Code，但专为数据库设计。"
    },
    "sql": {
      "label": "Native SQL",
      "title": "保留你的习惯",
      "desc": "直接执行 SQL。像 sqlplus、mysql、psql 一样使用，零学习成本。"
    },
    "natural": {
      "label": "Natural Language",
      "title": "用大白话提问",
      "desc": "用自然语言描述问题，AI 自动翻译为诊断动作并给出结果。"
    }
  },
  "features": {
    "title": "从监控到根因，一步到位",
    "subtitle": "三层诊断体系 — 从持续感知到 AI 深度推理。",
    "health": {
      "title": "全维度健康检查",
      "desc": "7 个维度、20+ 检查项 — 实例、存储、会话、内存、日志、备份、安全。一条命令，全局掌握。",
      "stat1Label": "检查维度",
      "stat2Label": "检查项",
      "stat3Label": "全量扫描"
    },
    "dbtop": {
      "title": "实时性能仪表盘",
      "desc": "数据库版的 top 命令。1 秒刷新，实时展示 SGA、PGA、TPS、QPS、等待事件和活跃会话。",
      "stat1Label": "刷新频率",
      "stat2Label": "实时指标"
    },
    "rule": {
      "title": "273 规则决策引擎",
      "desc": "毫秒级确定性诊断。离线可用 — 不需要 LLM、不需要网络、零 API 成本。AI 不可用时的安全兜底。",
      "stat1Label": "规则数",
      "stat2Label": "诊断耗时",
      "stat3Label": "外部依赖"
    },
    "llm": {
      "title": "AI 深度诊断",
      "desc": "LLM 多轮推理 + 函数调用。自动采集证据、关联症状、给出可执行的修复 SQL — 不是泛泛的建议。",
      "stat1Label": "最大推理轮数",
      "stat2Label": "Skill"
    },
    "sentinel": {
      "title": "7×24 异常检测",
      "desc": "后台探针持续监控 48 项指标，自适应 3-sigma 基线。在故障发生前发现异常。",
      "stat1Label": "监控指标",
      "stat2Label": "检测算法",
      "stat3Label": "持续监控"
    }
  },
  "footer": {
    "tagline": "最少交互，最优诊断。",
    "docs": "文档",
    "issues": "问题反馈",
    "license": "开源协议"
  }
}
```

- [ ] **Step 6: Create English translations**

Create `website/messages/en.json`:

```json
{
  "nav": {
    "features": "Features",
    "docs": "Docs",
    "github": "GitHub",
    "blog": "Blog",
    "getStarted": "Get Started"
  },
  "hero": {
    "badge": "DBCLI Agent with Claude Code's Interaction Model",
    "title1": "Least Interaction",
    "title2": "Optimal Diagnosis",
    "subtitle": "Oracle · MySQL · PostgreSQL — Slash Commands / Native SQL / Natural Language",
    "quickInstall": "Quick Install",
    "copy": "Copy",
    "copied": "Copied ✓"
  },
  "modes": {
    "title": "Three Modes. One Agent.",
    "subtitle": "Type however you think — OpenDB understands slash commands, native SQL, and natural language.",
    "slash": {
      "label": "Slash Commands",
      "title": "60+ Built-in Skills",
      "desc": "Complex operations as one-liners. Like Claude Code, but for databases."
    },
    "sql": {
      "label": "Native SQL",
      "title": "Your Habits, Preserved",
      "desc": "Run SQL directly. Works like sqlplus, mysql, psql — no learning curve."
    },
    "natural": {
      "label": "Natural Language",
      "title": "Ask in Plain Words",
      "desc": "Describe problems naturally. AI translates intent into diagnosis and action."
    }
  },
  "features": {
    "title": "From Monitoring to Root Cause",
    "subtitle": "A three-layer diagnosis system — from continuous sensing to AI-powered reasoning.",
    "health": {
      "title": "Full-Spectrum Health Check",
      "desc": "20+ checks across 7 dimensions — instance, storage, sessions, memory, logs, backup, and security. Get a complete picture in one command.",
      "stat1Label": "Dimensions",
      "stat2Label": "Check Items",
      "stat3Label": "Full Scan"
    },
    "dbtop": {
      "title": "Real-Time Performance Dashboard",
      "desc": "Like Linux top for databases. 1-second refresh showing SGA, PGA, TPS, QPS, wait events, and active sessions live.",
      "stat1Label": "Refresh Rate",
      "stat2Label": "Live Metrics"
    },
    "rule": {
      "title": "273-Rule Decision Engine",
      "desc": "Millisecond-level deterministic diagnosis. Works offline — no LLM, no network, no API cost. Your safety net when AI is unavailable.",
      "stat1Label": "Rules",
      "stat2Label": "Diagnosis",
      "stat3Label": "Dependencies"
    },
    "llm": {
      "title": "AI-Powered Deep Diagnosis",
      "desc": "Multi-round LLM reasoning with function calling. Automatically collects evidence, correlates symptoms, and delivers executable fix SQL — not vague suggestions.",
      "stat1Label": "Max Rounds",
      "stat2Label": "Skills"
    },
    "sentinel": {
      "title": "24/7 Anomaly Detection",
      "desc": "Background probe monitoring 48 metrics with adaptive 3-sigma baselines. Detects anomalies before they become outages.",
      "stat1Label": "Metrics",
      "stat2Label": "Detection",
      "stat3Label": "Monitoring"
    }
  },
  "footer": {
    "tagline": "Least interaction, optimal diagnosis.",
    "docs": "Docs",
    "issues": "Issues",
    "license": "License"
  }
}
```

- [ ] **Step 7: Create locale layout**

Create `website/src/app/[locale]/layout.tsx`:

```tsx
import { NextIntlClientProvider } from "next-intl";
import { getMessages } from "next-intl/server";
import { notFound } from "next/navigation";
import { routing } from "@/i18n/routing";

export default async function LocaleLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;
  if (!routing.locales.includes(locale as "en" | "zh")) {
    notFound();
  }
  const messages = await getMessages();

  return (
    <html lang={locale}>
      <head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link
          rel="preconnect"
          href="https://fonts.gstatic.com"
          crossOrigin="anonymous"
        />
        <link
          href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&family=Noto+Sans+SC:wght@400;500;600;700;800&display=swap"
          rel="stylesheet"
        />
      </head>
      <body>
        <NextIntlClientProvider messages={messages}>
          {children}
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
```

- [ ] **Step 8: Create placeholder page**

Create `website/src/app/[locale]/page.tsx`:

```tsx
import { useTranslations } from "next-intl";

export default function Home() {
  const t = useTranslations();
  return (
    <main>
      <h1>{t("hero.title1")}</h1>
      <p>{t("hero.subtitle")}</p>
    </main>
  );
}
```

- [ ] **Step 9: Verify i18n works**

```bash
cd /Users/yingjiewang/opendb/website
npm run dev
```

Visit `localhost:3000/zh` — should show "最少交互".
Visit `localhost:3000/en` — should show "Least Interaction".

- [ ] **Step 10: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/
git commit -m "feat: add next-intl i18n with zh/en translations"
```

---

### Task 3: Navigation Component

**Files:**
- Create: `website/src/components/Nav.tsx`
- Modify: `website/src/app/[locale]/page.tsx`

- [ ] **Step 1: Create Nav component**

Create `website/src/components/Nav.tsx`:

```tsx
"use client";

import { useTranslations } from "next-intl";
import { usePathname, useRouter } from "next/navigation";

export default function Nav() {
  const t = useTranslations("nav");
  const pathname = usePathname();
  const router = useRouter();

  const currentLocale = pathname.startsWith("/zh") ? "zh" : "en";
  const targetLocale = currentLocale === "zh" ? "en" : "zh";
  const targetLabel = currentLocale === "zh" ? "中文 / EN" : "EN / 中文";

  function switchLocale() {
    const newPath = pathname.replace(`/${currentLocale}`, `/${targetLocale}`);
    router.push(newPath || `/${targetLocale}`);
  }

  return (
    <nav
      className="sticky top-0 z-50 flex items-center justify-between px-12 py-3.5"
      style={{
        borderBottom: "1px solid var(--border)",
        background: "rgba(247, 247, 244, 0.85)",
        backdropFilter: "blur(12px)",
      }}
    >
      <div className="flex items-center gap-7">
        <span className="font-inter text-lg font-bold tracking-tight">
          OpenDB
        </span>
        <div className="flex gap-7">
          <a
            href="#features"
            className="text-sm transition-colors hover:text-text-primary"
            style={{ color: "var(--text-secondary)" }}
          >
            {t("features")}
          </a>
          <a
            href="https://github.com/sqlrush/opendbcli-as-claude-code"
            target="_blank"
            rel="noopener noreferrer"
            className="text-sm transition-colors hover:text-text-primary"
            style={{ color: "var(--text-secondary)" }}
          >
            {t("github")}
          </a>
        </div>
      </div>
      <div className="flex items-center gap-3.5">
        <button
          onClick={switchLocale}
          className="cursor-pointer text-xs"
          style={{ color: "var(--text-tertiary)" }}
        >
          {targetLabel}
        </button>
        <a
          href="#install"
          className="rounded-lg px-4 py-1.5 text-sm font-medium text-page-bg transition-opacity hover:opacity-85"
          style={{ backgroundColor: "var(--color-text-primary, #26251e)" }}
        >
          {t("getStarted")}
        </a>
      </div>
    </nav>
  );
}
```

- [ ] **Step 2: Add Nav to page**

Replace `website/src/app/[locale]/page.tsx`:

```tsx
import Nav from "@/components/Nav";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <p className="p-12 text-center" style={{ color: "var(--text-secondary)" }}>
          Sections coming next...
        </p>
      </main>
    </>
  );
}
```

- [ ] **Step 3: Verify nav renders with locale switch**

```bash
cd /Users/yingjiewang/opendb/website && npm run dev
```

Visit `localhost:3000/zh` — nav shows 功能/GitHub/快速开始. Click "中文 / EN" switches to `/en`.

- [ ] **Step 4: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/src/components/Nav.tsx website/src/app/\[locale\]/page.tsx
git commit -m "feat: add sticky navigation with locale switcher"
```

---

### Task 4: Terminal Mockup Base Component

**Files:**
- Create: `website/src/components/TerminalMockup.tsx`

- [ ] **Step 1: Create reusable terminal chrome component**

Create `website/src/components/TerminalMockup.tsx`:

```tsx
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
```

- [ ] **Step 2: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/src/components/TerminalMockup.tsx
git commit -m "feat: add reusable terminal mockup component"
```

---

### Task 5: Hero Section with Typewriter Animation

**Files:**
- Create: `website/src/components/TypewriterTerminal.tsx`
- Create: `website/src/components/Hero.tsx`
- Modify: `website/src/app/[locale]/page.tsx`

- [ ] **Step 1: Create typewriter terminal component**

Create `website/src/components/TypewriterTerminal.tsx`:

```tsx
"use client";

import { useEffect, useState, useCallback } from "react";
import TerminalMockup from "./TerminalMockup";

interface Line {
  text: string;
  className?: string;
  delay?: number; // ms before this line appears
}

const LINES: Line[] = [
  { text: "opendb> ", className: "text-terminal-green", delay: 0 },
  { text: "/llm 数据库为什么慢？", className: "text-terminal-text", delay: 0 },
  { text: "── LLM 诊断 (Claude Opus) ───────────────────────", className: "opacity-20", delay: 600 },
  { text: "⟳ 第 1/3 轮 ", className: "text-terminal-yellow", delay: 400 },
  { text: "采集等待事件、活跃会话...", className: "opacity-35", delay: 0 },
  { text: "⟳ 第 2/3 轮 ", className: "text-terminal-yellow", delay: 400 },
  { text: "分析 Top SQL、执行计划...", className: "opacity-35", delay: 0 },
  { text: "⟳ 第 3/3 轮 ", className: "text-terminal-yellow", delay: 400 },
  { text: "关联根因、生成修复方案...", className: "opacity-35", delay: 0 },
  { text: "✓ 诊断完成 (1分59秒, 3 轮推理)", className: "text-terminal-green", delay: 600 },
];

export default function TypewriterTerminal() {
  const [visibleLines, setVisibleLines] = useState(0);
  const [commandTyped, setCommandTyped] = useState("");
  const fullCommand = "/llm 数据库为什么慢？";

  const resetAnimation = useCallback(() => {
    setVisibleLines(0);
    setCommandTyped("");
  }, []);

  useEffect(() => {
    // Phase 1: Type command character by character
    let charIndex = 0;
    const typeInterval = setInterval(() => {
      charIndex++;
      setCommandTyped(fullCommand.slice(0, charIndex));
      if (charIndex >= fullCommand.length) {
        clearInterval(typeInterval);
        // Phase 2: Show output lines sequentially
        let lineIndex = 0;
        const lineInterval = setInterval(() => {
          lineIndex++;
          setVisibleLines(lineIndex);
          if (lineIndex >= LINES.length - 2) {
            // -2 because first 2 are prompt+command
            clearInterval(lineInterval);
            // Phase 3: Pause then reset
            setTimeout(resetAnimation, 4000);
          }
        }, 300);
      }
    }, 50);

    return () => clearInterval(typeInterval);
  }, [commandTyped === "" ? "reset" : "typing", resetAnimation]);

  return (
    <TerminalMockup className="mx-auto mt-10 max-w-[680px]">
      {/* Command line */}
      <div>
        <span className="text-terminal-green">opendb&gt;</span>{" "}
        <span>{commandTyped}</span>
        {commandTyped.length < fullCommand.length && (
          <span className="inline-block w-2 animate-pulse bg-terminal-text">&nbsp;</span>
        )}
      </div>

      {/* Output lines */}
      {visibleLines > 0 && (
        <div className="mt-2" style={{ color: "rgba(237, 236, 236, 0.15)" }}>
          ── LLM 诊断 (Claude Opus) ───────────────────────
        </div>
      )}
      {visibleLines > 1 && (
        <div className="mt-1">
          <span className="text-terminal-yellow">⟳ 第 1/3 轮</span>{" "}
          <span style={{ color: "var(--terminal-dim)" }}>采集等待事件、活跃会话...</span>
        </div>
      )}
      {visibleLines > 2 && (
        <div>
          <span className="text-terminal-yellow">⟳ 第 2/3 轮</span>{" "}
          <span style={{ color: "var(--terminal-dim)" }}>分析 Top SQL、执行计划...</span>
        </div>
      )}
      {visibleLines > 3 && (
        <div>
          <span className="text-terminal-yellow">⟳ 第 3/3 轮</span>{" "}
          <span style={{ color: "var(--terminal-dim)" }}>关联根因、生成修复方案...</span>
        </div>
      )}
      {visibleLines > 4 && (
        <div className="mt-2 text-terminal-green">✓ 诊断完成 (1分59秒, 3 轮推理)</div>
      )}
      {visibleLines > 5 && (
        <div
          className="mt-2 rounded-r-sm py-1.5 pl-3"
          style={{
            background: "rgba(255, 107, 53, 0.08)",
            borderLeft: "2px solid rgba(255, 107, 53, 0.7)",
          }}
        >
          <div className="text-xs font-semibold text-terminal-orange">
            根因 — 严重度: ■■■□ 高
          </div>
          <div className="mt-0.5 text-xs" style={{ color: "rgba(237, 236, 236, 0.7)" }}>
            ORDERS 表全表扫描 (210 万行) — customer_id 列缺少索引
          </div>
        </div>
      )}
      {visibleLines > 6 && (
        <div
          className="mt-1.5 rounded-r-sm py-1.5 pl-3"
          style={{
            background: "rgba(126, 231, 135, 0.06)",
            borderLeft: "2px solid rgba(126, 231, 135, 0.6)",
          }}
        >
          <div className="text-xs font-semibold text-terminal-green">修复方案</div>
          <div className="mt-0.5 text-xs" style={{ color: "rgba(237, 236, 236, 0.7)" }}>
            CREATE INDEX idx_orders_cust ON orders(customer_id);
          </div>
        </div>
      )}
    </TerminalMockup>
  );
}
```

- [ ] **Step 2: Create Hero component**

Create `website/src/components/Hero.tsx`:

```tsx
"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import TypewriterTerminal from "./TypewriterTerminal";

export default function Hero() {
  const t = useTranslations("hero");
  const [copyText, setCopyText] = useState(t("copy"));

  function handleCopy() {
    navigator.clipboard.writeText(
      "curl -fsSL https://www.opendbcli.org/install.sh | bash"
    );
    setCopyText(t("copied"));
    setTimeout(() => setCopyText(t("copy")), 1500);
  }

  return (
    <section className="relative overflow-hidden px-12 pb-16 pt-20 text-center">
      {/* Subtle glow */}
      <div
        className="pointer-events-none absolute left-1/2 top-[-100px] h-[300px] w-[600px] -translate-x-1/2"
        style={{
          background: "radial-gradient(ellipse, rgba(38, 37, 30, 0.03) 0%, transparent 70%)",
        }}
      />

      {/* Badge */}
      <div
        className="mb-7 inline-block rounded-3xl px-5 py-2 text-lg font-medium"
        style={{
          background: "var(--fill)",
          border: "1px solid var(--border)",
          color: "var(--text-secondary)",
        }}
      >
        {t("badge")}
      </div>

      {/* Title */}
      <h1 className="mx-auto max-w-[720px] text-[64px] font-extrabold leading-[1.05] tracking-[-2.5px]">
        {t("title1")}
        <br />
        {t("title2")}
      </h1>

      {/* Subtitle */}
      <p
        className="mx-auto mt-5 max-w-[680px] text-[17px]"
        style={{ color: "var(--text-secondary)" }}
      >
        {t("subtitle")}
      </p>

      {/* Install block */}
      <div className="mx-auto mt-10 max-w-[620px]" id="install">
        {/* Tabs + label */}
        <div className="mb-3 flex items-center gap-3">
          <span className="text-[15px] font-bold">{t("quickInstall")}</span>
          <span
            className="rounded-md px-3 py-1 text-xs font-semibold text-page-bg"
            style={{ backgroundColor: "var(--color-text-primary, #26251e)" }}
          >
            macOS
          </span>
          <span
            className="rounded-md px-3 py-1 text-xs"
            style={{
              background: "var(--fill)",
              color: "var(--text-secondary)",
              border: "1px solid var(--border)",
            }}
          >
            Linux
          </span>
        </div>

        {/* Command */}
        <div
          className="flex cursor-pointer items-center justify-between rounded-xl px-6 py-4 font-mono text-base transition-shadow"
          style={{
            background: "var(--color-terminal-bg, #1a1a18)",
            color: "#edecec",
            border: "1px solid rgba(237, 236, 236, 0.08)",
            boxShadow: "0 8px 32px rgba(38, 37, 30, 0.1)",
            whiteSpace: "nowrap",
          }}
          onClick={handleCopy}
        >
          <div>
            <span style={{ color: "var(--terminal-dim)" }}>$</span>{" "}
            curl -fsSL https://www.opendbcli.org/install.sh | bash
          </div>
          <span
            className="ml-3.5 whitespace-nowrap border-l pl-3.5 text-xs"
            style={{
              color: "var(--terminal-dim)",
              borderColor: "rgba(237, 236, 236, 0.1)",
            }}
          >
            {copyText}
          </span>
        </div>

        {/* GitHub */}
        <div className="mt-5 flex justify-center">
          <a
            href="https://github.com/sqlrush/opendbcli-as-claude-code"
            target="_blank"
            rel="noopener noreferrer"
            className="rounded-lg px-7 py-3 text-[15px] transition-colors"
            style={{
              background: "var(--fill)",
              color: "var(--color-text-primary, #26251e)",
              border: "1px solid var(--border)",
            }}
          >
            GitHub ★ →
          </a>
        </div>
      </div>

      {/* Terminal animation */}
      <TypewriterTerminal />
    </section>
  );
}
```

- [ ] **Step 3: Wire Hero into page**

Replace `website/src/app/[locale]/page.tsx`:

```tsx
import Nav from "@/components/Nav";
import Hero from "@/components/Hero";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
      </main>
    </>
  );
}
```

- [ ] **Step 4: Verify hero renders with animation**

```bash
cd /Users/yingjiewang/opendb/website && npm run dev
```

Visit `localhost:3000/zh` — hero shows badge, title, install block, and typewriter terminal animation.

- [ ] **Step 5: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/src/components/TypewriterTerminal.tsx website/src/components/Hero.tsx website/src/app/\[locale\]/page.tsx
git commit -m "feat: add hero section with typewriter terminal animation"
```

---

### Task 6: Scroll Reveal + Three Modes Section

**Files:**
- Create: `website/src/components/ScrollReveal.tsx`
- Create: `website/src/components/ThreeModes.tsx`
- Modify: `website/src/app/[locale]/page.tsx`

- [ ] **Step 1: Create scroll reveal wrapper**

Create `website/src/components/ScrollReveal.tsx`:

```tsx
"use client";

import { motion } from "framer-motion";
import { ReactNode } from "react";

interface ScrollRevealProps {
  children: ReactNode;
  className?: string;
  delay?: number;
}

export default function ScrollReveal({
  children,
  className = "",
  delay = 0,
}: ScrollRevealProps) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 2 }}
      whileInView={{ opacity: 1, y: 0 }}
      viewport={{ once: true, margin: "-80px" }}
      transition={{ duration: 0.5, delay, ease: "easeOut" }}
      className={className}
    >
      {children}
    </motion.div>
  );
}
```

- [ ] **Step 2: Create ThreeModes component**

Create `website/src/components/ThreeModes.tsx`:

```tsx
"use client";

import { useTranslations } from "next-intl";
import ScrollReveal from "./ScrollReveal";

const EXAMPLES = {
  slash: (
    <>
      <span className="text-terminal-green">opendb&gt;</span> /health{"\n"}
      <span className="text-terminal-green">opendb&gt;</span> /dbtop{"\n"}
      <span className="text-terminal-green">opendb&gt;</span> /llm 诊断
    </>
  ),
  sql: (
    <>
      <span className="text-terminal-green">opendb&gt;</span> SELECT count(*){"\n"}
      {"  "}FROM orders{"\n"}
      {"  "}WHERE status=&apos;pending&apos;;
    </>
  ),
  natural: (
    <>
      <span className="text-terminal-green">opendb&gt;</span> 为什么现在{"\n"}
      有这么多锁等待？
    </>
  ),
};

export default function ThreeModes() {
  const t = useTranslations("modes");

  const cards = [
    { key: "slash" as const, example: EXAMPLES.slash },
    { key: "sql" as const, example: EXAMPLES.sql },
    { key: "natural" as const, example: EXAMPLES.natural },
  ];

  return (
    <section className="mx-auto max-w-[1100px] px-12 py-24">
      <ScrollReveal>
        <h2 className="text-center text-[40px] font-extrabold tracking-tight">
          {t("title")}
        </h2>
        <p
          className="mx-auto mt-3.5 max-w-[500px] text-center text-[17px]"
          style={{ color: "var(--text-secondary)" }}
        >
          {t("subtitle")}
        </p>
      </ScrollReveal>

      <div className="mt-13 grid grid-cols-3 gap-4">
        {cards.map((card, i) => (
          <ScrollReveal key={card.key} delay={i * 0.1}>
            <div
              className="rounded-[14px] p-7 transition-shadow hover:shadow-md"
              style={{
                background: "var(--color-page-elevated, #fff)",
                border: "1px solid var(--border)",
              }}
            >
              <div
                className="mb-3 font-inter text-[11px] uppercase tracking-widest"
                style={{ color: "var(--text-tertiary)" }}
              >
                {t(`${card.key}.label`)}
              </div>
              <h3 className="mb-2 text-[17px] font-bold">{t(`${card.key}.title`)}</h3>
              <p className="text-sm leading-relaxed" style={{ color: "var(--text-secondary)" }}>
                {t(`${card.key}.desc`)}
              </p>
              <pre
                className="mt-3.5 whitespace-pre-wrap rounded-lg px-3.5 py-3 font-mono text-sm leading-[1.7]"
                style={{
                  background: "var(--color-terminal-bg, #1a1a18)",
                  color: "#edecec",
                }}
              >
                {card.example}
              </pre>
            </div>
          </ScrollReveal>
        ))}
      </div>
    </section>
  );
}
```

- [ ] **Step 3: Add to page**

Update `website/src/app/[locale]/page.tsx` — add `<ThreeModes />` after `<Hero />`:

```tsx
import Nav from "@/components/Nav";
import Hero from "@/components/Hero";
import ThreeModes from "@/components/ThreeModes";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
        <div className="mx-auto h-px max-w-[1100px]" style={{ background: "var(--border)" }} />
        <ThreeModes />
      </main>
    </>
  );
}
```

- [ ] **Step 4: Verify three modes section renders**

```bash
cd /Users/yingjiewang/opendb/website && npm run dev
```

Visit `localhost:3000/zh` — scroll down, three cards fade in with terminal examples.

- [ ] **Step 5: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/src/components/ScrollReveal.tsx website/src/components/ThreeModes.tsx website/src/app/\[locale\]/page.tsx
git commit -m "feat: add three modes section with scroll reveal animations"
```

---

### Task 7: Feature Row Component + 5 Terminal Outputs

**Files:**
- Create: `website/src/components/FeatureRow.tsx`
- Create: `website/src/components/Features.tsx`
- Create: `website/src/components/feature-terminals/HealthTerminal.tsx`
- Create: `website/src/components/feature-terminals/DbtopTerminal.tsx`
- Create: `website/src/components/feature-terminals/RuleTerminal.tsx`
- Create: `website/src/components/feature-terminals/LlmTerminal.tsx`
- Create: `website/src/components/feature-terminals/SentinelTerminal.tsx`
- Modify: `website/src/app/[locale]/page.tsx`

- [ ] **Step 1: Create FeatureRow component**

Create `website/src/components/FeatureRow.tsx`:

```tsx
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
  return (
    <div
      className={`mt-22 flex items-center gap-14 ${reverse ? "flex-row-reverse" : ""}`}
    >
      <ScrollReveal className="flex-1">
        <span
          className="inline-block rounded-md px-2.5 py-0.5 font-mono text-sm font-semibold"
          style={{ background: "var(--fill)" }}
        >
          {tag}
        </span>
        <h3 className="mt-1 text-[26px] font-bold tracking-tight">{title}</h3>
        <p
          className="mt-3 text-[15px] leading-[1.7]"
          style={{ color: "var(--text-secondary)" }}
        >
          {description}
        </p>
        <div className="mt-5 flex gap-7">
          {stats.map((stat) => (
            <div key={stat.label}>
              <div className="font-inter text-[32px] font-extrabold tracking-tight">
                {stat.value}
              </div>
              <div className="mt-0.5 text-xs" style={{ color: "var(--text-tertiary)" }}>
                {stat.label}
              </div>
            </div>
          ))}
        </div>
      </ScrollReveal>

      <ScrollReveal className="flex-1" delay={0.15}>
        <div
          className="overflow-hidden rounded-xl p-4 font-mono text-[13.5px] leading-[1.75]"
          style={{
            background: "var(--color-terminal-bg, #1a1a18)",
            color: "#edecec",
            border: "1px solid rgba(0, 0, 0, 0.08)",
            boxShadow: "0 12px 48px rgba(38, 37, 30, 0.1)",
          }}
        >
          {terminal}
        </div>
      </ScrollReveal>
    </div>
  );
}
```

- [ ] **Step 2: Create 5 feature terminal components**

Create `website/src/components/feature-terminals/HealthTerminal.tsx`:

```tsx
export default function HealthTerminal() {
  const dim = { color: "var(--terminal-dim)" };
  const sep = { color: "rgba(237, 236, 236, 0.15)" };
  return (
    <>
      <div className="mb-2 text-terminal-green">opendb&gt; /health</div>
      <div style={sep}>┌──────────────────────────────────────────┐</div>
      <div>&nbsp; <span className="font-semibold">ORCLCDB</span> <span className="text-terminal-orange">● 严重</span></div>
      <div style={sep}>├──────────────────────────────────────────┤</div>
      <div>&nbsp; <span style={dim}>实例</span>{"    "}<span className="text-terminal-green">✓</span> OPEN READ WRITE  运行 47 天</div>
      <div>&nbsp; <span style={dim}>存储</span>{"    "}<span className="text-terminal-orange">✗</span> SYSTEM 表空间使用率 94.2%</div>
      <div>&nbsp; <span style={dim}>会话</span>{"    "}<span className="text-terminal-yellow">⚠</span> 活跃 108/500 (21.6%)</div>
      <div>&nbsp; <span style={dim}>内存</span>{"    "}<span className="text-terminal-green">✓</span> SGA 2.4G  PGA 847M</div>
      <div>&nbsp; <span style={dim}>日志</span>{"    "}<span className="text-terminal-yellow">⚠</span> 24h 内 3 条 ORA 错误</div>
      <div>&nbsp; <span style={dim}>备份</span>{"    "}<span className="text-terminal-green">✓</span> 上次 RMAN 备份 6h 前</div>
      <div>&nbsp; <span style={dim}>安全</span>{"    "}<span className="text-terminal-green">✓</span> 无默认密码</div>
      <div style={sep}>├──────────────────────────────────────────┤</div>
      <div>&nbsp; <span className="text-terminal-orange">发现 3 个问题</span> — 执行 /llm 深度诊断</div>
      <div style={sep}>└──────────────────────────────────────────┘</div>
    </>
  );
}
```

Create `website/src/components/feature-terminals/DbtopTerminal.tsx`:

```tsx
export default function DbtopTerminal() {
  const dim = { color: "var(--terminal-dim)" };
  return (
    <>
      <div className="mb-2 text-terminal-green">opendb&gt; /dbtop</div>
      <div className="font-semibold">ORCLCDB <span className="text-terminal-yellow">● 警告</span> <span style={dim} className="font-normal">↻ 1s</span></div>
      <div className="mt-2 flex flex-wrap gap-4">
        <span><span style={dim}>SGA</span> 2.4G</span>
        <span><span style={dim}>PGA</span> 847M</span>
        <span><span style={dim}>db%</span> <span className="text-terminal-orange">78.3</span></span>
        <span><span style={dim}>TPS</span> 1,247</span>
        <span><span style={dim}>QPS</span> 3,891</span>
      </div>
      <div className="mt-2.5 text-[10px]" style={dim}>TOP 等待事件</div>
      <div className="mt-1"><span className="text-terminal-orange">▇▇▇▇▇▇▇▇▇░</span> db file seq read <span style={dim}>70.6%</span></div>
      <div><span className="text-terminal-yellow">▇▇▇░░░░░░░</span> log file sync    <span style={dim}>21.4%</span></div>
      <div><span className="text-terminal-green">▇░░░░░░░░░</span> latch free       <span style={dim}>3.2%</span></div>
      <div className="mt-2.5 text-[10px]" style={dim}>活跃会话 (108)</div>
      <div className="mt-1" style={dim}>SID   用户       SQL       等待事件            耗时</div>
      <div> 142  APP_USER   SELECT    db file seq read   12.3s</div>
      <div> 287  APP_USER   UPDATE    log file sync       8.7s</div>
      <div> 053  BATCH      INSERT    db file seq read    6.1s</div>
    </>
  );
}
```

Create `website/src/components/feature-terminals/RuleTerminal.tsx`:

```tsx
export default function RuleTerminal() {
  const dim = { color: "var(--terminal-dim)" };
  const sep = { color: "rgba(237, 236, 236, 0.15)" };
  return (
    <>
      <div className="mb-2 text-terminal-green">opendb&gt; /rule</div>
      <div style={sep}>── 规则引擎 (273 条规则, 2.7s) ─────────────────</div>
      <div className="mt-2"><span style={dim}>分类:</span> I/O 性能</div>
      <div className="mt-1"><span className="font-semibold text-terminal-orange">根因:</span> 全表扫描导致过量 I/O</div>
      <div><span style={dim}>严重度:</span> <span className="text-terminal-orange">■■■□</span> 高  <span style={dim}>置信度:</span> 78%</div>
      <div className="mt-2.5 text-[10px]" style={dim}>证据链</div>
      <div className="mt-1">
        <span style={dim}>1.</span> db file sequential read 占总等待 70.6%<br />
        <span style={dim}>2.</span> SQL_ID: 4qr83a1n2k — 210 万行全表扫描<br />
        <span style={dim}>3.</span> ORDERS 表 customer_id 列缺少索引<br />
        <span style={dim}>4.</span> Buffer Cache 命中率降至 67.2%
      </div>
      <div className="mt-2.5 text-[10px]" style={dim}>推荐操作</div>
      <div className="mt-1 text-terminal-green">CREATE INDEX idx_orders_cust ON orders(customer_id);</div>
    </>
  );
}
```

Create `website/src/components/feature-terminals/LlmTerminal.tsx`:

```tsx
export default function LlmTerminal() {
  const dim = { color: "var(--terminal-dim)" };
  const sep = { color: "rgba(237, 236, 236, 0.15)" };
  return (
    <>
      <div className="mb-2 text-terminal-green">opendb&gt; /llm</div>
      <div style={sep}>── LLM 深度诊断 ──────────────────────────────</div>
      <div className="mt-1.5"><span className="text-terminal-yellow">⟳ R1</span> <span style={dim}>get_wait_events() → 12 个事件</span></div>
      <div><span className="text-terminal-yellow">⟳ R2</span> <span style={dim}>get_active_sessions() → 108 个会话</span></div>
      <div><span className="text-terminal-yellow">⟳ R3</span> <span style={dim}>get_sql_plan(4qr83a1n2k) → 全表扫描</span></div>
      <div className="mt-2 text-terminal-green">✓ 完成 (1分59秒)</div>
      <div className="mt-2.5 text-[10px]" style={dim}>发现问题 (3)</div>
      <div className="mt-1">
        <span className="text-terminal-orange">■■■□</span> 全表扫描 — 占等待 70.6%<br />
        <span className="text-terminal-yellow">■■□□</span> 日志文件争用 — 占等待 21.4%<br />
        <span style={dim}>■□□□</span> Latch 争用 — 占等待 3.2%
      </div>
      <div className="mt-2.5 text-[10px]" style={dim}>修复方案 (按优先级)</div>
      <div className="mt-1 leading-[1.9] text-terminal-green">
        1. CREATE INDEX idx_orders_cust ON orders(customer_id);<br />
        2. ALTER SYSTEM SET log_buffer = 64M SCOPE=SPFILE;
      </div>
    </>
  );
}
```

Create `website/src/components/feature-terminals/SentinelTerminal.tsx`:

```tsx
export default function SentinelTerminal() {
  const dim = { color: "var(--terminal-dim)" };
  return (
    <>
      <div className="mb-2 text-terminal-green">opendb&gt; /sentinel start</div>
      <div style={dim}>哨兵已启动，正在监控 48 项指标...</div>
      <div className="mt-3 font-semibold text-terminal-orange">⚡ 检测到异常</div>
      <div className="mt-1.5">
        <span style={dim}>指标:</span> 活跃会话数<br />
        <span style={dim}>基线:</span> 8.0  →  <span className="text-terminal-orange">当前: 41</span><br />
        <span style={dim}>阈值:</span> 3σ = 23.5  <span className="text-terminal-orange">已突破</span><br />
        <span style={dim}>持续:</span> 39.8 秒
      </div>
      <div className="mt-2.5 text-[10px]" style={dim}>已触发突发采集</div>
      <div className="mt-1">
        <span style={dim}>→ 等待事件、活跃 SQL、阻塞链已采集</span><br />
        <span style={dim}>→ 执行 /llm 或 /rule 进行诊断</span>
      </div>
    </>
  );
}
```

- [ ] **Step 3: Create Features container**

Create `website/src/components/Features.tsx`:

```tsx
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
    <section id="features" className="mx-auto max-w-[1100px] px-12 py-24">
      <ScrollReveal>
        <h2 className="text-center text-[40px] font-extrabold tracking-tight">
          {t("title")}
        </h2>
        <p
          className="mx-auto mt-3.5 max-w-[480px] text-center text-[17px]"
          style={{ color: "var(--text-secondary)" }}
        >
          {t("subtitle")}
        </p>
      </ScrollReveal>

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
    </section>
  );
}
```

- [ ] **Step 4: Add Features to page**

Update `website/src/app/[locale]/page.tsx`:

```tsx
import Nav from "@/components/Nav";
import Hero from "@/components/Hero";
import ThreeModes from "@/components/ThreeModes";
import Features from "@/components/Features";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
        <div className="mx-auto h-px max-w-[1100px]" style={{ background: "var(--border)" }} />
        <ThreeModes />
        <div className="mx-auto h-px max-w-[1100px]" style={{ background: "var(--border)" }} />
        <Features />
      </main>
    </>
  );
}
```

- [ ] **Step 5: Verify all 5 features render**

```bash
cd /Users/yingjiewang/opendb/website && npm run dev
```

Visit `localhost:3000/zh` — scroll down, 5 feature rows alternate left/right with terminal mockups.

- [ ] **Step 6: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/src/components/
git commit -m "feat: add features section with 5 terminal output demos"
```

---

### Task 8: Footer Component

**Files:**
- Create: `website/src/components/Footer.tsx`
- Modify: `website/src/app/[locale]/page.tsx`

- [ ] **Step 1: Create Footer component**

Create `website/src/components/Footer.tsx`:

```tsx
import { useTranslations } from "next-intl";

export default function Footer() {
  const t = useTranslations("footer");

  return (
    <footer
      className="mx-auto flex max-w-[1100px] items-center justify-between px-12 py-10"
      style={{ borderTop: "1px solid var(--border)" }}
    >
      <div className="text-sm" style={{ color: "var(--text-secondary)" }}>
        <strong className="text-sm text-text-primary">OpenDB</strong>
        <br />
        {t("tagline")}
      </div>
      <div className="flex gap-6">
        {[
          { label: t("docs"), href: "#" },
          { label: "GitHub", href: "https://github.com/sqlrush/opendbcli-as-claude-code" },
          { label: t("issues"), href: "https://github.com/sqlrush/opendbcli-as-claude-code/issues" },
          { label: t("license"), href: "https://github.com/sqlrush/opendbcli-as-claude-code/blob/main/LICENSE" },
        ].map((link) => (
          <a
            key={link.label}
            href={link.href}
            target={link.href.startsWith("http") ? "_blank" : undefined}
            rel={link.href.startsWith("http") ? "noopener noreferrer" : undefined}
            className="text-sm transition-colors hover:text-text-primary"
            style={{ color: "var(--text-secondary)" }}
          >
            {link.label}
          </a>
        ))}
      </div>
    </footer>
  );
}
```

- [ ] **Step 2: Add Footer to page**

Update `website/src/app/[locale]/page.tsx` — add `<Footer />` after `<Features />`:

```tsx
import Nav from "@/components/Nav";
import Hero from "@/components/Hero";
import ThreeModes from "@/components/ThreeModes";
import Features from "@/components/Features";
import Footer from "@/components/Footer";

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
        <div className="mx-auto h-px max-w-[1100px]" style={{ background: "var(--border)" }} />
        <ThreeModes />
        <div className="mx-auto h-px max-w-[1100px]" style={{ background: "var(--border)" }} />
        <Features />
      </main>
      <Footer />
    </>
  );
}
```

- [ ] **Step 3: Verify full page renders top to bottom**

```bash
cd /Users/yingjiewang/opendb/website && npm run dev
```

Visit `localhost:3000/zh` and `localhost:3000/en` — full page: Nav → Hero → Three Modes → Features → Footer.

- [ ] **Step 4: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/src/components/Footer.tsx website/src/app/\[locale\]/page.tsx
git commit -m "feat: add footer, complete all page sections"
```

---

### Task 9: Production Build + Deployment Config

**Files:**
- Create: `website/ecosystem.config.js`
- Modify: `website/next.config.ts` (production settings)

- [ ] **Step 1: Verify production build succeeds**

```bash
cd /Users/yingjiewang/opendb/website
npm run build
```

Expected: Build completes without errors.

- [ ] **Step 2: Create PM2 config**

Create `website/ecosystem.config.js`:

```javascript
module.exports = {
  apps: [
    {
      name: "opendb-website",
      script: "node_modules/.bin/next",
      args: "start -p 3000",
      cwd: "/opt/opendb-website",
      env: {
        NODE_ENV: "production",
      },
      instances: 1,
      autorestart: true,
      max_memory_restart: "256M",
    },
  ],
};
```

- [ ] **Step 3: Create Nginx config reference**

Create `website/nginx.conf.example`:

```nginx
server {
    listen 80;
    server_name opendbcli.org www.opendbcli.org;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name opendbcli.org www.opendbcli.org;

    ssl_certificate /etc/letsencrypt/live/opendbcli.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/opendbcli.org/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_cache_bypass $http_upgrade;
    }
}
```

- [ ] **Step 4: Commit**

```bash
cd /Users/yingjiewang/opendb
git add website/ecosystem.config.js website/nginx.conf.example
git commit -m "feat: add PM2 and Nginx deployment configs"
```

---

### Task 10: Deploy to Production Server

- [ ] **Step 1: Build and push code**

```bash
cd /Users/yingjiewang/opendb
git push origin main
```

- [ ] **Step 2: SSH to server and clone**

```bash
ssh root@43.110.57.194
git clone https://github.com/sqlrush/opendbcli-as-claude-code.git /opt/opendb-website
cd /opt/opendb-website/website
```

- [ ] **Step 3: Install Node.js 18+ if not present**

```bash
curl -fsSL https://deb.nodesource.com/setup_18.x | bash -
apt-get install -y nodejs
npm install -g pm2
```

- [ ] **Step 4: Build and start**

```bash
cd /opt/opendb-website/website
npm install
npm run build
pm2 start ecosystem.config.js
pm2 save
pm2 startup
```

- [ ] **Step 5: Configure Nginx + SSL**

```bash
apt-get install -y nginx certbot python3-certbot-nginx
cp /opt/opendb-website/website/nginx.conf.example /etc/nginx/sites-available/opendbcli.org
ln -s /etc/nginx/sites-available/opendbcli.org /etc/nginx/sites-enabled/
certbot --nginx -d opendbcli.org -d www.opendbcli.org
nginx -t && systemctl reload nginx
```

- [ ] **Step 6: Verify site is live**

Visit `https://opendbcli.org` — full landing page should render.
Visit `https://opendbcli.org/zh` — Chinese version.
Visit `https://opendbcli.org/en` — English version.
