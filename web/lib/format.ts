export const one = (n: number): string => n.toFixed(1);
export const two = (n: number): string => n.toFixed(2);
export const three = (n: number): string => n.toFixed(3);
/** +0.4 / -0.8 / 0.0 */
export const signed = (n: number): string => (n > 0 ? `+${n.toFixed(1)}` : n.toFixed(1));

/** Only web addresses become links; anything else (such as fpl://) is not a page. */
export function webURL(url: string): string | undefined {
  return /^https?:\/\//i.test(url) ? url : undefined;
}

const sourceNames: [prefix: string, label: string][] = [
  ["bbc", "BBC Sport"],
  ["sky", "Sky Sports"],
  ["talksport", "TalkSport"],
  ["guardian", "The Guardian"],
  ["fpl", "Official FPL note"],
];

/** A reader's name for a stored source id such as "sky-football-news"; unknown ids are shown as they are. */
export function sourceLabel(source: string): string {
  const id = source.toLowerCase();
  return sourceNames.find(([prefix]) => id === prefix || id.startsWith(prefix + "-"))?.[1] ?? source;
}
