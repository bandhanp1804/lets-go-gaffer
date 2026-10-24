"use client";

import { useCallback, useEffect, useState } from "react";
import { getJSON, ApiError } from "./api.ts";

export interface ApiState<T> {
  data: T | undefined;
  error: ApiError | undefined;
  loading: boolean;
  reload: () => void;
}

/** Fetches a JSON endpoint. Pass null to wait (for example until a form is submitted). */
export function useApi<T>(path: string | null): ApiState<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<ApiError>();
  const [loading, setLoading] = useState(path !== null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (path === null) {
      setLoading(false);
      return;
    }
    const ctl = new AbortController();
    setLoading(true);
    setError(undefined);
    getJSON<T>(path, ctl.signal)
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
  }, [path, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}
