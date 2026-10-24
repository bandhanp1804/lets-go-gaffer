// Whose squad the app is showing: a team id, and optionally the manager's FPL token for a
// live squad. Both live in this browser only. The id is not a secret and is remembered; the
// token is a login for the manager's FPL account, so it is kept for this tab only and goes
// to this app's own server (which uses it for the request and keeps nothing).

export interface Identity {
  teamId: number | null;
  token: string; // bare token, without "Bearer"; empty for public data
}

export const noIdentity: Identity = { teamId: null, token: "" };

/** "1234567", or the address of any FPL page that has it, like .../entry/1234567/event/8. */
export function parseTeamId(input: string): number | null {
  const text = input.trim();
  const fromUrl = /\/entry\/(\d{1,9})(?:\/|$|\?|#)/.exec(text);
  const digits = fromUrl?.[1] ?? (/^\d{1,9}$/.test(text) ? text : undefined);
  if (digits === undefined) return null;
  const id = Number(digits);
  return id >= 1 ? id : null;
}

export type TokenCheck = { ok: true; token: string } | { ok: false; reason: string };

/**
 * Accepts what people actually paste: the bare token, "Bearer <token>", or the whole header
 * line "x-api-authorization: Bearer <token>", with stray quotes or spaces.
 */
export function cleanToken(input: string): TokenCheck {
  let t = input.trim();
  t = t.replace(/^x-api-authorization\s*:\s*/i, "").trim();
  t = t.replace(/^["'`]+|["'`]+$/g, "").trim();
  t = t.replace(/^bearer\s+/i, "").trim();
  if (t === "") return { ok: false, reason: "Paste the token." };
  if (!/^[A-Za-z0-9._~+/=-]+$/.test(t)) return { ok: false, reason: "That does not look like a token: it should be one long piece of text with no spaces." };
  if (t.split(".").length !== 3) return { ok: false, reason: "That does not look like an FPL token (it should have three parts separated by dots)." };
  if (t.length > 4000) return { ok: false, reason: "That is too long to be a token." };
  return { ok: true, token: t };
}

/** When the token expires, in milliseconds since the epoch, read from its payload (not verified). */
export function tokenExpiry(token: string): number | null {
  const payload = token.split(".")[1];
  if (!payload) return null;
  try {
    const b64 = payload.replace(/-/g, "+").replace(/_/g, "/");
    const json = atob(b64.padEnd(b64.length + ((4 - (b64.length % 4)) % 4), "="));
    const exp = (JSON.parse(json) as { exp?: unknown }).exp;
    return typeof exp === "number" ? exp * 1000 : null;
  } catch {
    return null;
  }
}

/** "47 min", "1h 5m", "expired", or null when unknown. */
export function expiresIn(token: string, now: number): string | null {
  const at = tokenExpiry(token);
  if (at === null) return null;
  const ms = at - now;
  if (ms <= 0) return "expired";
  const mins = Math.floor(ms / 60_000);
  return mins >= 60 ? `${Math.floor(mins / 60)}h ${mins % 60}m` : `${mins} min`;
}

/** The headers that tell the server whose squad a request is about. */
export function headersFor(id: Identity): Record<string, string> {
  const h: Record<string, string> = {};
  if (id.teamId !== null) h["X-Gaffer-Team"] = String(id.teamId);
  if (id.token !== "") h["X-Gaffer-Token"] = id.token;
  return h;
}

const TEAM_KEY = "gaffer.team";
const TOKEN_KEY = "gaffer.token";

/** Storage can be missing or throw (private windows, blocked site data): the app works without it. */
export function loadIdentity(): Identity {
  let teamId: number | null = null;
  let token = "";
  try {
    teamId = parseTeamId(localStorage.getItem(TEAM_KEY) ?? "");
  } catch {
    // no storage: nothing remembered
  }
  try {
    const t = cleanToken(sessionStorage.getItem(TOKEN_KEY) ?? "");
    if (t.ok && expiresIn(t.token, Date.now()) !== "expired") token = t.token;
  } catch {
    // no storage
  }
  return { teamId, token };
}

export function saveIdentity(id: Identity): void {
  try {
    if (id.teamId === null) localStorage.removeItem(TEAM_KEY);
    else localStorage.setItem(TEAM_KEY, String(id.teamId));
  } catch {
    // ignore
  }
  try {
    if (id.token === "") sessionStorage.removeItem(TOKEN_KEY);
    else sessionStorage.setItem(TOKEN_KEY, id.token);
  } catch {
    // ignore
  }
}
