"use client";

import { useEffect, useState } from "react";
import { useTeam } from "../components/TeamProvider.tsx";
import { ApiError } from "./api.ts";
import type { ErrorBody, WhatIf, WhatIfRequest } from "./types.gen.ts";

export interface WhatIfState {
  data: WhatIf | undefined;
  error: ApiError | undefined;
  loading: boolean;
}

/** Evaluates a plan whenever it changes (after a short pause, so clicking quickly sends one request). */
export function useWhatIf(body: WhatIfRequest | null): WhatIfState {
  const { ready, headers } = useTeam();
  const key = body === null ? "" : JSON.stringify(body);
  const who = JSON.stringify(headers);
  const [state, setState] = useState<WhatIfState>({ data: undefined, error: undefined, loading: false });

  useEffect(() => {
    if (key === "" || !ready) {
      setState({ data: undefined, error: undefined, loading: false });
      return;
    }
    const ctl = new AbortController();
    setState((s) => ({ ...s, loading: true, error: undefined }));
    const timer = setTimeout(() => {
      fetch("/api/whatif", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...(JSON.parse(who) as Record<string, string>) },
        body: key,
        signal: ctl.signal,
      })
        .then(async (res) => {
          if (!res.ok) {
            const e = (await res.json().catch(() => ({}))) as Partial<ErrorBody>;
            throw new ApiError(res.status, e.error ?? `Request failed (${res.status})`, e.code ?? "");
          }
          setState({ data: (await res.json()) as WhatIf, error: undefined, loading: false });
        })
        .catch((e: unknown) => {
          if (ctl.signal.aborted) return;
          setState({ data: undefined, error: e instanceof ApiError ? e : new ApiError(0, e instanceof Error ? e.message : String(e)), loading: false });
        });
    }, 150);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [key, who, ready]);

  return state;
}
