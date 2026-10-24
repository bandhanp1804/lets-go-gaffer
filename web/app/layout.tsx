import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Nav } from "../components/Nav.tsx";
import "./globals.css";

export const metadata: Metadata = {
  title: "Let’s Go Gaffer",
  description: "A Fantasy Premier League assistant: squad, lineup, transfers and news.",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Nav />
        <main>{children}</main>
      </body>
    </html>
  );
}
