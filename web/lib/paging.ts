// Sorting and paging helpers for the players table.

export interface Sort {
  by: string;
  dir: "asc" | "desc";
}

/** Columns where the interesting end is the biggest number first. */
const bestFirst = new Set(["xp", "form", "points"]);

/** Clicking the sorted column flips it; clicking another starts with its natural direction. */
export function nextSort(current: Sort, column: string): Sort {
  if (current.by === column) return { by: column, dir: current.dir === "asc" ? "desc" : "asc" };
  return { by: column, dir: bestFirst.has(column) ? "desc" : "asc" };
}

/** "1–25 of 668", "26–50 of 668", "0 players". */
export function pageLabel(offset: number, shown: number, total: number): string {
  if (total === 0 || shown === 0) return `${total} players`;
  return `${offset + 1}–${offset + shown} of ${total}`;
}
