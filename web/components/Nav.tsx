"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { isPublicPath, signInURL } from "../lib/session.ts";
import { canSignOut, signOutURL } from "../lib/signout.ts";
import type { Usage } from "../lib/types.gen.ts";
import { useApi } from "../lib/useApi.ts";
import { useSession } from "../lib/useSession.ts";
import { AlertsBadge } from "./AlertsBadge.tsx";
import { icons, TabBar } from "./TabBar.tsx";
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

// On a phone these are the tabs along the bottom; the rest go under More.
const tabbed = ["/", "/team", "/lineup", "/ask"];

export function Nav() {
  // The model's statistics are the owners'; visitors are not shown the link. An older API without
  // /api/usage leaves it shown.
  const usage = useApi<Usage>("/api/usage");
  const owner = usage.data === undefined || usage.data.unlimited;
  const path = usePathname();
  const { identity, ready, live, openModal, settings } = useTeam();
  const session = useSession();
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
  const shown = links.filter((l) => owner || l.href !== "/model");
  const isActive = (href: string) => (href === "/" ? path === "/" : path.startsWith(href));
  const signOutHref = canSignOut(settings?.user) ? signOutURL(window.location.host) : null;
  const withIcon = (l: (typeof links)[number]) => ({ ...l, icon: icons[l.href] });
  return (
    <>
      <nav className="nav" aria-label="Main">
        <Link href="/" className="brand">
          <span className="ball" aria-hidden="true">⚽</span> <span className="brandname">Let’s Go Gaffer</span>
        </Link>
        <ul>
          {shown.map((l) => (
            <li key={l.href}>
              <Link href={l.href} aria-current={isActive(l.href) ? "page" : undefined} className={isActive(l.href) ? "active" : undefined}>
                {l.label}
                {l.href === "/alerts" ? <AlertsBadge /> : null}
              </Link>
            </li>
          ))}
        </ul>
        <button type="button" className="teamchip" onClick={openModal} aria-haspopup="dialog">
          {!ready ? "…" : identity.teamId === null ? "Pick your team" : `Team ${identity.teamId}`}
          {ready && identity.teamId !== null ? <span className={live ? "tag live" : "tag"}>{live ? "live" : "public"}</span> : null}
        </button>
        {signOutHref ? (
          <a className="signout" href={signOutHref} title={`Signed in as ${settings?.user}`}>
            Sign out
          </a>
        ) : null}
      </nav>
      <TabBar
        main={shown.filter((l) => tabbed.includes(l.href)).map(withIcon)}
        more={shown.filter((l) => !tabbed.includes(l.href)).map(withIcon)}
        isActive={isActive}
        path={path}
        signOutHref={signOutHref}
      />
    </>
  );
}
