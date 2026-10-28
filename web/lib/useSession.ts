"use client";

import { useEffect, useState } from "react";
import { sessionFrom, type Session } from "./session.ts";

// One check per page load, shared by every component that asks.
let pending: Promise<Session> | undefined;

function check(): Promise<Session> {
  pending ??= fetch("/oauth2/auth", { cache: "no-store", redirect: "manual" })
    .then((res) => sessionFrom(res.status))
    .catch((): Session => "out");
  return pending;
}

/** Whether the browser is signed in: "unknown" on the server and until the check answers. */
export function useSession(): Session {
  const [session, setSession] = useState<Session>("unknown");
  useEffect(() => {
    let cancelled = false;
    check().then((s) => {
      if (!cancelled) setSession(s);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  return session;
}
