// The what-if builder's state and the small rules around it. Whether a plan is legal is
// decided by the server (in Go); this only keeps the draft tidy.

import type { WhatIfRequest } from "./types.gen.ts";

/** A move being built: a player of yours to sell, and (once chosen) the player to buy. */
export interface DraftMove {
  out: number;
  in: number | null;
}

/** Clicking a squad player marks him to be sold; clicking again takes the mark off. */
export function toggleOut(moves: DraftMove[], outId: number): DraftMove[] {
  return moves.some((m) => m.out === outId) ? moves.filter((m) => m.out !== outId) : [...moves, { out: outId, in: null }];
}

/** Sets (or, with null, clears) the player bought for a sale. */
export function setIn(moves: DraftMove[], outId: number, inId: number | null): DraftMove[] {
  return moves.map((m) => (m.out === outId ? { ...m, in: inId } : m));
}

/** The first sale that still needs a player to buy, if any. */
export function pendingSlot(moves: DraftMove[]): number | null {
  return moves.find((m) => m.in === null)?.out ?? null;
}

/** The request for the server: only complete moves, or null when there are none yet. */
export function requestFor(moves: DraftMove[], horizon: number, free: number | null): WhatIfRequest | null {
  const done = moves.filter((m): m is { out: number; in: number } => m.in !== null);
  if (done.length === 0) return null;
  return { moves: done.map((m) => ({ out: m.out, in: m.in })), horizon, free_transfers: free };
}

/** "101:201,102:202" is a plan in an address, so a suggestion can open pre-filled. */
export function movesToParam(moves: { out: number; in: number }[]): string {
  return moves.map((m) => `${m.out}:${m.in}`).join(",");
}

export function movesFromParam(param: string | null): DraftMove[] {
  if (!param) return [];
  const out: DraftMove[] = [];
  const seen = new Set<number>();
  for (const part of param.split(",").slice(0, 15)) {
    const m = /^(\d{1,9}):(\d{1,9})$/.exec(part);
    if (!m) continue;
    const o = Number(m[1]);
    if (o < 1 || Number(m[2]) < 1 || seen.has(o)) continue;
    seen.add(o);
    out.push({ out: o, in: Number(m[2]) });
  }
  return out;
}

/** "£5.9m" -> 5.9. */
export function parsePrice(text: string | undefined): number | null {
  const m = /^£(\d+(?:\.\d)?)m$/.exec((text ?? "").trim());
  return m ? Number(m[1]) : null;
}

/**
 * The most a replacement can cost: what the sale brings in plus the bank. It is a guide
 * for the search list; the server checks the whole plan's money.
 */
export function maxReplacementPrice(sellPrice: string | undefined, bank: string): number | null {
  const sell = parsePrice(sellPrice);
  const cash = parsePrice(bank);
  if (sell === null || cash === null) return null;
  return Math.round((sell + cash) * 10) / 10;
}
