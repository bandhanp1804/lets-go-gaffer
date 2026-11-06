"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { isPublicPath, signInURL } from "../lib/session.ts";
import { canSignOut, signOutURL } from "../lib/signout.ts";
import type { Usage } from "../lib/types.gen.ts";
import { useApi } from "../lib/useApi.ts";
import { useSession } from "../lib/useSession.ts";
import { AlertsBadge } from "./AlertsBadge.tsx";
import { useTeam } from "./TeamProvider.tsx";

const links = [
  { href: "/", label: "Home" },
  { href: "/team", label: "My team" },
  { href: "/lineup", label: "Best XI" },
  { href: "/transfers", label: "Transfers" },
  { href: "/whatif", label: "What if?" },
  { href: "/squad", label: "Squad builder" },
  { href: "/players", label: "Players" },
  { href: "/news", label: "News" },
  { href: "/ask", label: "Ask" },
  { href: "/model", label: "Model" },
  { href: "/alerts", label: "Alerts" },
  { href: "/setup", label: "Setup" },
];

export function Nav() {
  // The model's statistics are the owners'; visitors are not shown the link. An older API without
  // /api/usage leaves it shown.
  const usage = useApi<Usage>("/api/usage");
  const owner = usage.data === undefined || usage.data.unlimited;
  const path = usePathname();
  const { identity, ready, live, openModal, settings } = useTeam();
  const session = useSession();
  // On a narrow screen the links fold into a menu (see .navtoggle in globals.css). It closes when
  // a page is chosen, and on Escape.
  const [menuOpen, setMenuOpen] = useState(false);
  useEffect(() => setMenuOpen(false), [path]);
  useEffect(() => {
    if (!menuOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [menuOpen]);
  // The pages served without a login show only the way in until the browser is known to be
  // signed in: every other link would only ask for the login.
  if (isPublicPath(path) && session !== "in") {
    return (
      <nav className="nav" aria-label="Main">
        <Link href="/" className="brand">
          <span className="ball" aria-hidden="true">⚽</span> Let’s Go Gaffer
        </Link>
        {session === "out" ? (
          <a className="signin" href={signInURL}>
            Sign in
          </a>
        ) : null}
      </nav>
    );
  }
  const signOut = canSignOut(settings?.user) ? (
    <a className="signout" href={signOutURL(window.location.host)} title={`Signed in as ${settings?.user}`}>
      Sign out
    </a>
  ) : null;
  return (
    <nav className={menuOpen ? "nav open" : "nav"} aria-label="Main">
      <Link href="/" className="brand">
        <span className="ball" aria-hidden="true">⚽</span> <span className="brandname">Let’s Go Gaffer</span>
      </Link>
      <ul id="navlinks">
        {links.filter((l) => owner || l.href !== "/model").map((l) => {
          const active = l.href === "/" ? path === "/" : path.startsWith(l.href);
          return (
            <li key={l.href}>
              <Link href={l.href} aria-current={active ? "page" : undefined} className={active ? "active" : undefined}>
                {l.label}
                {l.href === "/alerts" ? <AlertsBadge /> : null}
              </Link>
            </li>
          );
        })}
        {signOut ? <li className="navsignout">{signOut}</li> : null}
      </ul>
      <button type="button" className="teamchip" onClick={openModal} aria-haspopup="dialog">
        {!ready ? "…" : identity.teamId === null ? "Pick your team" : `Team ${identity.teamId}`}
        {ready && identity.teamId !== null ? <span className={live ? "tag live" : "tag"}>{live ? "live" : "public"}</span> : null}
      </button>
      <button type="button" className="navtoggle" aria-expanded={menuOpen} aria-controls="navlinks" onClick={() => setMenuOpen((o) => !o)}>
        {menuOpen ? "Close" : "Menu"}
      </button>
      {signOut}
    </nav>
  );
}
