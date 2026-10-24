"use client";

import { useCallback, useEffect, useState } from "react";
import { getJSON, ApiError } from "./api.ts";
import { useTeam } from "../components/TeamProvider.tsx";

export interface ApiState<T> {
  data: T | undefined;
  error: ApiError | undefined;
  loading: boolean;
  reload: () => void;
}

/**
 * Fetches a JSON endpoint for the chosen team, and again when the team or token changes.
 * Pass null to wait (for example until a form is submitted). Nothing is fetched until the
 * saved team has been read, so a page never flashes another team's data.
 */
export function useApi<T>(path: string | null): ApiState<T> {
  const { ready, headers } = useTeam();
  const who = JSON.stringify(headers); // a change of team or token is a change of who
  const [data, setData] = useState<T>();
  const [error, setError] = useState<ApiError>();
  const [loading, setLoading] = useState(path !== null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (path === null || !ready) {
      setLoading(path !== null);
      return;
    }
    const ctl = new AbortController();
    setLoading(true);
    setError(undefined);
    getJSON<T>(path, ctl.signal, JSON.parse(who) as Record<string, string>)
      .then((d) => {
        setData(d);
        setLoading(false);
      })
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        setError(e instanceof ApiError ? e : new ApiError(0, e instanceof Error ? e.message : String(e)));
        setData(undefined);
        setLoading(false);
      });
    return () => ctl.abort();
  }, [path, tick, ready, who]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}
