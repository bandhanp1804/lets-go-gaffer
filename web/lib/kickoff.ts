/** "Sat 24 Oct, 15:00" in the reader's own time zone; "" if the time is missing or not a date. */
export function kickoffText(iso: string | undefined, locale = "en-GB"): string {
  if (!iso) return "";
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "";
  const day = t.toLocaleDateString(locale, { weekday: "short", day: "numeric", month: "short" });
  const time = t.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", hour12: false });
  return `${day}, ${time}`;
}

/** "0 – 1", or "v" before a match has a score. */
export function scoreText(home: number | null, away: number | null): string {
  return home === null || away === null ? "v" : `${home} – ${away}`;
}
