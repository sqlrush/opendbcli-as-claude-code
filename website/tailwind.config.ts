import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./src/pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/components/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      colors: {
        // Page colors
        page: "#f7f7f4",
        surface: "#efeee9",
        elevated: "#ffffff",
        // Text colors
        "text-primary": "#26251e",
        // Terminal colors
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
        sans: ["Noto Sans SC", "Inter", "sans-serif"],
        mono: ["JetBrains Mono", "monospace"],
        inter: ["Inter", "sans-serif"],
      },
    },
  },
  plugins: [],
};

export default config;
