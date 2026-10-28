import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Footer } from "../components/Footer.tsx";
import { Nav } from "../components/Nav.tsx";
import { TeamModal } from "../components/TeamModal.tsx";
import { TeamProvider } from "../components/TeamProvider.tsx";
import "./globals.css";

export const metadata: Metadata = {
  title: "Let’s Go Gaffer",
  description: "A Fantasy Premier League assistant: squad, lineup, transfers and news.",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <TeamProvider>
          <Nav />
          <main>{children}</main>
          <Footer />
          <TeamModal />
        </TeamProvider>
      </body>
    </html>
  );
}
