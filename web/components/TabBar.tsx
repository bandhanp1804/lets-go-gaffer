"use client";

import Link from "next/link";
import { useEffect, useState, type ReactNode } from "react";
import { AlertsBadge } from "./AlertsBadge.tsx";

/** One page in the phone navigation. */
export interface Tab {
  href: string;
  label: string;
  icon: ReactNode;
}

const stroke = { fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round", strokeLinejoin: "round" } as const;

function Icon({ children }: { children: ReactNode }) {
  return (
    <svg className="tabicon" viewBox="0 0 24 24" width="24" height="24" aria-hidden="true" {...stroke}>
      {children}
    </svg>
  );
}

/** Line icons for the pages, drawn here so the app needs no icon library. */
export const icons: Record<string, ReactNode> = {
  "/": (
    <Icon>
      <path d="M3 11 12 4l9 7" />
      <path d="M5 10v10h14V10" />
    </Icon>
  ),
  "/team": (
    <Icon>
      <path d="M8 3 3 6l2 4 2-1v12h10V9l2 1 2-4-5-3a4 4 0 0 1-8 0Z" />
    </Icon>
  ),
  "/lineup": (
    <Icon>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M3 12h18" />
      <circle cx="12" cy="12" r="3" />
    </Icon>
  ),
  "/ask": (
    <Icon>
      <path d="M4 5h16v11H9l-5 4Z" />
      <path d="M8 9h8M8 12h5" />
    </Icon>
  ),
  "/transfers": (
    <Icon>
      <path d="M4 8h14l-3-3M20 16H6l3 3" />
    </Icon>
  ),
  "/whatif": (
    <Icon>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6V14M12 17h.01" />
    </Icon>
  ),
  "/squad": (
    <Icon>
      <rect x="4" y="4" width="6" height="6" rx="1" />
      <rect x="14" y="4" width="6" height="6" rx="1" />
      <rect x="4" y="14" width="6" height="6" rx="1" />
      <rect x="14" y="14" width="6" height="6" rx="1" />
    </Icon>
  ),
  "/players": (
    <Icon>
      <circle cx="9" cy="8" r="3" />
      <path d="M3 20a6 6 0 0 1 12 0M16 5a3 3 0 0 1 0 6M18 20a6 6 0 0 0-2.5-4.9" />
    </Icon>
  ),
  "/news": (
    <Icon>
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <path d="M8 8h8M8 12h8M8 16h5" />
    </Icon>
  ),
  "/model": (
    <Icon>
      <path d="M4 20V10M10 20V4M16 20v-7M22 20H2" />
    </Icon>
  ),
  "/alerts": (
    <Icon>
      <path d="M6 16V11a6 6 0 0 1 12 0v5l2 2H4Z" />
      <path d="M10 20a2 2 0 0 0 4 0" />
    </Icon>
  ),
  "/setup": (
    <Icon>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1 7 17M17 7l2.1-2.1" />
    </Icon>
  ),
  more: (
    <Icon>
      <circle cx="5" cy="12" r="1.2" />
      <circle cx="12" cy="12" r="1.2" />
      <circle cx="19" cy="12" r="1.2" />
    </Icon>
  ),
  signout: (
    <Icon>
      <path d="M10 4H5v16h5M15 8l4 4-4 4M19 12H9" />
    </Icon>
  ),
};

interface Props {
  /** The tabs always on the bar; everything else is under More. */
  main: Tab[];
  more: Tab[];
  isActive: (href: string) => boolean;
  /** A route change closes the More panel. */
  path: string;
  signOutHref: string | null;
}

/**
 * The phone navigation, after the FPL app: the main pages as tabs along the bottom, within reach
 * of a thumb, and the rest in a panel that More opens from the bottom. Hidden on wider screens,
 * which keep the links in the top bar (see .tabbar in globals.css).
 */
export function TabBar({ main, more, isActive, path, signOutHref }: Props) {
  const [open, setOpen] = useState(false);
  useEffect(() => setOpen(false), [path]);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);
  const moreActive = more.some((t) => isActive(t.href));

  return (
    <>
      {open ? <div className="moreshade" onClick={() => setOpen(false)} aria-hidden="true" /> : null}
      {open ? (
        <div className="moresheet" id="moresheet" role="menu" aria-label="More pages">
          {more.map((t) => (
            <Link key={t.href} href={t.href} role="menuitem" className={isActive(t.href) ? "active" : undefined}>
              {t.icon}
              <span>{t.label}</span>
              {t.href === "/alerts" ? <AlertsBadge /> : null}
            </Link>
          ))}
          {signOutHref ? (
            <a href={signOutHref} role="menuitem" className="moresignout">
              {icons.signout}
              <span>Sign out</span>
            </a>
          ) : null}
        </div>
      ) : null}
      <nav className="tabbar" aria-label="Pages">
        {main.map((t) => (
          <Link key={t.href} href={t.href} aria-current={isActive(t.href) ? "page" : undefined} className={isActive(t.href) ? "active" : undefined}>
            {t.icon}
            <span>{t.label}</span>
          </Link>
        ))}
        <button
          type="button"
          className={open || moreActive ? "active" : undefined}
          aria-expanded={open}
          aria-controls="moresheet"
          onClick={() => setOpen((o) => !o)}
        >
          {icons.more}
          <span>More</span>
          <AlertsBadge />
        </button>
      </nav>
    </>
  );
}
