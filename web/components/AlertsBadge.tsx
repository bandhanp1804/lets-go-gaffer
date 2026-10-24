"use client";

import { useEffect, useState } from "react";
import { getJSON } from "../lib/api.ts";
import type { Alerts } from "../lib/types.gen.ts";

const POLL_MS = 60_000;

/** The unread count beside the Alerts link, checked once a minute. Nothing shows at zero. */
export function AlertsBadge() {
  const [unread, setUnread] = useState(0);
  useEffect(() => {
    const ctl = new AbortController();
    const load = () =>
      getJSON<Alerts>("/api/alerts", ctl.signal)
        .then((a) => setUnread(a.unread))
        .catch(() => setUnread(0));
    void load();
    const id = setInterval(load, POLL_MS);
    // A page that marks alerts read tells the badge at once instead of waiting a minute.
    window.addEventListener("gaffer:alerts-read", load);
    return () => {
      ctl.abort();
      clearInterval(id);
      window.removeEventListener("gaffer:alerts-read", load);
    };
  }, []);
  if (unread === 0) return null;
  return (
    <span className="unread" aria-label={`${unread} unread`}>
      {unread > 99 ? "99+" : unread}
    </span>
  );
}
