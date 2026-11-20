// Transfers the manager says they have made since the last deadline. FPL keeps those private
// until the next deadline, so without a login token this is the only way the app can see them.
// They live in this browser only, for one team and one gameweek, and travel to this app's server
// in the X-Gaffer-Made header, which applies them to the public squad with FPL's rules checked.

export interface MadeTransfer {
  out: number; // player id sold
  in: number; // player id bought
  outName: string;
  inName: string;
}

export interface MadeRecord {
  teamId: number;
  gameweek: number; // the gameweek they were made for: after its deadline FPL shows them itself
  moves: MadeTransfer[];
}

/** The header value: "101>201,113>206". */
export function madeHeader(moves: MadeTransfer[]): string {
  return moves.map((m) => `${m.out}>${m.in}`).join(",");
}

/**
 * Adds transfers to those already entered. The public squad still holds the players sold
 * before, so a player bought earlier and now sold again collapses into one transfer (A for B,
 * then B for C, is A for C), and buying back the player first sold cancels the transfer.
 */
export function addMade(existing: MadeTransfer[], moves: MadeTransfer[]): MadeTransfer[] {
  let out = [...existing];
  for (const m of moves) {
    const earlier = out.find((e) => e.in === m.out);
    if (earlier === undefined) {
      out.push(m);
      continue;
    }
    out = out.filter((e) => e !== earlier);
    if (earlier.out !== m.in) out.push({ out: earlier.out, in: m.in, outName: earlier.outName, inName: m.inName });
  }
  return out;
}

/** The transfers that apply to a team now: none from another team or an earlier gameweek. */
export function currentMade(rec: MadeRecord | null, teamId: number | null, gameweek: number | null): MadeTransfer[] {
  if (rec === null || teamId === null || rec.teamId !== teamId) return [];
  if (gameweek !== null && rec.gameweek !== gameweek) return [];
  return rec.moves;
}

const MADE_KEY = "gaffer.made";

/** Storage can be missing or throw (private windows, blocked site data): the app works without it. */
export function loadMade(): MadeRecord | null {
  try {
    const raw = localStorage.getItem(MADE_KEY);
    if (raw === null) return null;
    const rec = JSON.parse(raw) as MadeRecord;
    if (typeof rec.teamId !== "number" || typeof rec.gameweek !== "number" || !Array.isArray(rec.moves)) return null;
    return rec;
  } catch {
    return null;
  }
}

export function saveMade(rec: MadeRecord | null): void {
  try {
    if (rec === null || rec.moves.length === 0) localStorage.removeItem(MADE_KEY);
    else localStorage.setItem(MADE_KEY, JSON.stringify(rec));
  } catch {
    // ignore
  }
}
