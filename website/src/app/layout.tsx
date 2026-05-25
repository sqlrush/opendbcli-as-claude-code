import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "OpenDB — DBCLI Agent as Claude Code",
  description: "像Claude Code一样交互的DBCLI Agent。最少交互，最优诊断。",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  // html/body tags are handled by the locale layout
  return children as React.ReactElement;
}
