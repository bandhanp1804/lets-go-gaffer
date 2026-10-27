import type { SubsRequest } from "./types.gen.ts";

export interface Swap {
  out: number; // a starter
  in: number; // a bench player
}

/** The substitutions being tried, and a starter picked but not yet given a replacement. */
export interface SubsDraft {
  swaps: Swap[];
  pending: number | null;
}

export const noSubs: SubsDraft = { swaps: [], pending: null };

/**
 * What a click on a player does: a player already in a swap undoes it; a starter is picked (or
 * unpicked) to come off; a bench player comes on for the picked starter. A bench player clicked
 * with no starter picked does nothing. The rules themselves are checked by the server.
 */
export function clickForSubs(d: SubsDraft, id: number, starter: boolean): SubsDraft {
  if (d.swaps.some((s) => s.out === id || s.in === id)) {
    return { swaps: d.swaps.filter((s) => s.out !== id && s.in !== id), pending: null };
  }
  if (starter) return { swaps: d.swaps, pending: d.pending === id ? null : id };
  if (d.pending === null) return d;
  return { swaps: [...d.swaps, { out: d.pending, in: id }], pending: null };
}

/** The marks on the pitch: who comes off (and the picked starter), who comes on. */
export function subsMarks(d: SubsDraft): Map<number, "off" | "on"> {
  const m = new Map<number, "off" | "on">();
  for (const s of d.swaps) {
    m.set(s.out, "off");
    m.set(s.in, "on");
  }
  if (d.pending !== null) m.set(d.pending, "off");
  return m;
}

/** The request for the server: null while nothing has been tried, so nothing is sent. */
export function subsRequest(d: SubsDraft): SubsRequest | null {
  return d.swaps.length === 0 ? null : { swaps: d.swaps.map((s) => ({ out: s.out, in: s.in })) };
}
