import type { Player } from "./types.gen.ts";

export const LINES = ["GKP", "DEF", "MID", "FWD"] as const;
export type Line = (typeof LINES)[number];

/** Groups a starting XI into pitch lines, keeping the order the server gave within each. */
export function pitchLines(players: Player[]): { line: Line; players: Player[] }[] {
  return LINES.map((line) => ({ line, players: players.filter((p) => p.position === line) })).filter((l) => l.players.length > 0);
}

/** How a player's availability should look: "ok" shows nothing, the rest show a flag. */
export function availability(status: string): "ok" | "doubt" | "out" {
  const s = status.toLowerCase();
  if (s.startsWith("available")) return "ok";
  if (s.startsWith("doubtful")) return "doubt";
  return "out"; // injured, suspended, unavailable
}

/** "ARS (H)" -> "ARS (H)"; several fixtures are joined by the server already. */
export function roleOf(name: string, captain: string, vice: string): "C" | "V" | "" {
  if (name === captain) return "C";
  if (name === vice) return "V";
  return "";
}
