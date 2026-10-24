"use client";

import type { WatchState } from "../lib/watchlist.ts";

/** A star that watches a player: his news and status changes become alerts. */
export function WatchButton({ id, name, watch }: { id: number; name: string; watch: WatchState }) {
  if (!watch.available) return null;
  const on = watch.ids.has(id);
  return (
    <button
      type="button"
      className={`watch ${on ? "on" : ""}`}
      aria-pressed={on}
      title={on ? `Stop watching ${name}` : `Watch ${name}: his news and status changes become alerts`}
      onClick={() => void watch.toggle(id)}
    >
      {on ? "★" : "☆"}
      <span className="sr-only">{on ? `Stop watching ${name}` : `Watch ${name}`}</span>
    </button>
  );
}
