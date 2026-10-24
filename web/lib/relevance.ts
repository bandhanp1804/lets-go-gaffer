// How to describe a story's similarity to the search. The thresholds come from what the data showed:
// with a player named, relevant stories scored 0.49 to 0.54; unrelated ones scored 0.34 to 0.38.

export type Level = "strong" | "ok" | "loose";

export function relevance(similarity: number): { level: Level; label: string } {
  if (similarity >= 0.5) return { level: "strong", label: "strong match" };
  if (similarity >= 0.4) return { level: "ok", label: "possible match" };
  return { level: "loose", label: "loose match" };
}
