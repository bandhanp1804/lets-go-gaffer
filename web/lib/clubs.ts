/**
 * Club colours for the three-letter codes FPL uses: the shirt's main colour, a second colour for
 * its trim, and the text colour that reads on the main one (clubs.test.ts checks every pairing
 * meets WCAG AA contrast). It covers this season's clubs and those that often go up or down; a
 * club not listed keeps the app's own colours.
 */
export interface ClubColours {
  bg: string;
  trim: string;
  text: string;
}

const white = "#ffffff";

export const clubs: Record<string, ClubColours> = {
  ARS: { bg: "#db0007", trim: white, text: white },
  AVL: { bg: "#670e36", trim: "#95bfe5", text: white },
  BOU: { bg: "#b50e12", trim: "#000000", text: white },
  BRE: { bg: "#c8102e", trim: white, text: white },
  BHA: { bg: "#0057b8", trim: white, text: white },
  BUR: { bg: "#6c1d45", trim: "#99d6ea", text: white },
  CHE: { bg: "#034694", trim: white, text: white },
  COV: { bg: "#59cbe8", trim: white, text: "#0b2240" },
  CRY: { bg: "#1b458f", trim: "#c4122e", text: white },
  EVE: { bg: "#003399", trim: white, text: white },
  FUL: { bg: white, trim: "#000000", text: "#000000" },
  HUL: { bg: "#f5a12d", trim: "#000000", text: "#000000" },
  IPS: { bg: "#0044a9", trim: white, text: white },
  LEE: { bg: white, trim: "#ffcd00", text: "#1d428a" },
  LEI: { bg: "#003090", trim: "#fdbe11", text: white },
  LIV: { bg: "#c8102e", trim: "#00b2a9", text: white },
  LUT: { bg: "#f78f1e", trim: "#002d62", text: "#002d62" },
  MCI: { bg: "#6cabdd", trim: white, text: "#1c2c5b" },
  MID: { bg: "#c8102e", trim: white, text: white },
  MUN: { bg: "#c70101", trim: "#fbe122", text: white },
  NEW: { bg: "#241f20", trim: white, text: white },
  NFO: { bg: "#c8102e", trim: white, text: white },
  NOR: { bg: "#fff200", trim: "#00a650", text: "#00552b" },
  SHU: { bg: "#c8102e", trim: "#000000", text: white },
  SOU: { bg: "#c8102e", trim: white, text: white },
  STK: { bg: "#c8102e", trim: white, text: white },
  SUN: { bg: "#c8102e", trim: white, text: white },
  TOT: { bg: white, trim: "#132257", text: "#132257" },
  WAT: { bg: "#fbee23", trim: "#ed2127", text: "#000000" },
  WBA: { bg: "#122f67", trim: white, text: white },
  WHU: { bg: "#7a263a", trim: "#1bb1e7", text: white },
  WOL: { bg: "#fdb913", trim: "#231f20", text: "#231f20" },
};

/** The colours for a club code, or undefined to keep the app's own. */
export function clubColours(code: string): ClubColours | undefined {
  return clubs[code.trim().toUpperCase()];
}

/** WCAG relative luminance of a #rrggbb colour. */
function luminance(hex: string): number {
  const channel = (i: number) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5);
}

/** WCAG contrast ratio between two #rrggbb colours, from 1 to 21. */
export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}
