"use client";

import { useCallback, useEffect, useState } from "react";
import { ApiError, getJSON, postJSON } from "./api.ts";
import type { Watched, Watchlist } from "./types.gen.ts";

export interface WatchState {
  /** Watched player ids; empty until loaded, or when the API has no watchlist. */
  ids: Set<number>;
  players: Watched[];
  /** Alerts are being produced: a workflow server is configured. */
  live: boolean;
  available: boolean;
  error: string;
  toggle: (id: number) => Promise<void>;
}

const RETRIES = 5;
const RETRY_MS = 3000;

/** The watchlist, shared by every Watch button on a page. */
export function useWatchlist(): WatchState {
  const [list, setList] = useState<Watchlist>();
  // An API that does not know the endpoint, or a visitor (the watchlist is the owner's), hides the stars. A failed load (the API was
  // restarting, say) keeps them and tries again, so one bad moment does not hide them for good.
  const [available, setAvailable] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    const ctl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = (attempt: number) =>
      getJSON<Watchlist>("/api/watchlist", ctl.signal)
        .then(setList)
        .catch((e: unknown) => {
          if (ctl.signal.aborted) return;
          if (e instanceof ApiError && (e.status === 403 || e.status === 404 || e.status === 405)) {
            setAvailable(false);
            return;
          }
          if (attempt < RETRIES) timer = setTimeout(() => void load(attempt + 1), RETRY_MS);
        });
    void load(0);
    return () => {
      ctl.abort();
      clearTimeout(timer);
    };
  }, []);

  const ids = new Set((list?.players ?? []).map((p) => p.id));
  const toggle = useCallback(
    async (id: number) => {
      setError("");
      try {
        const on = !(list?.players ?? []).some((p) => p.id === id);
        setList(await postJSON<Watchlist>("/api/watchlist", { player_id: id, watch: on }));
      } catch (e) {
        setError(e instanceof Error ? e.message : "Could not change the watchlist.");
      }
    },
    [list],
  );
  return { ids, players: list?.players ?? [], live: list?.live ?? false, available, error, toggle };
}
