import type { CSSProperties } from "react";
import { clubColours } from "../lib/clubs.ts";

/**
 * A club's colours as CSS variables (--club-bg, --club-trim, --club-text), for the shirt on a
 * player card and the tag in tables. An unknown club sets none, so the app's own colours apply.
 */
export function clubStyle(code: string): CSSProperties | undefined {
  const c = clubColours(code);
  if (!c) return undefined;
  return { "--club-bg": c.bg, "--club-trim": c.trim, "--club-text": c.text } as CSSProperties;
}

/** A club's three-letter code in its colours. */
export function ClubTag({ code }: { code: string }) {
  return (
    <span className="clubtag" style={clubStyle(code)}>
      {code}
    </span>
  );
}
