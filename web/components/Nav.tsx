"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/", label: "Dashboard" },
  { href: "/team", label: "My team" },
  { href: "/lineup", label: "Best XI" },
  { href: "/transfers", label: "Transfers" },
  { href: "/squad", label: "Squad builder" },
  { href: "/players", label: "Players" },
  { href: "/news", label: "News" },
  { href: "/ask", label: "Ask" },
  { href: "/model", label: "Model" },
];

export function Nav() {
  const path = usePathname();
  return (
    <nav className="nav" aria-label="Main">
      <Link href="/" className="brand">
        Let’s Go Gaffer
      </Link>
      <ul>
        {links.map((l) => {
          const active = l.href === "/" ? path === "/" : path.startsWith(l.href);
          return (
            <li key={l.href}>
              <Link href={l.href} aria-current={active ? "page" : undefined} className={active ? "active" : undefined}>
                {l.label}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
