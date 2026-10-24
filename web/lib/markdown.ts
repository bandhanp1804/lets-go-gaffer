// A small markdown reader for assistant answers. It produces data, never HTML, so the
// page renders it as React elements and nothing from the model is ever injected as markup.
// It understands what the assistant writes: paragraphs, bold, `code`, bullet and numbered
// lists, headings and pipe tables. Citations like [2] become their own inline part.

export type Inline =
  | { kind: "text"; text: string }
  | { kind: "bold"; text: string }
  | { kind: "code"; text: string }
  | { kind: "cite"; ref: number };

export type Block =
  | { kind: "p"; inline: Inline[] }
  | { kind: "h"; inline: Inline[] }
  | { kind: "ul"; items: Inline[][] }
  | { kind: "ol"; items: Inline[][] }
  | { kind: "table"; head: Inline[][]; rows: Inline[][][] };

export function parseInline(s: string): Inline[] {
  const out: Inline[] = [];
  const re = /\*\*([^*]+)\*\*|`([^`]+)`|\[(\d{1,3})\]/g;
  let last = 0;
  for (let m = re.exec(s); m; m = re.exec(s)) {
    if (m.index > last) out.push({ kind: "text", text: s.slice(last, m.index) });
    if (m[1] !== undefined) out.push({ kind: "bold", text: m[1] });
    else if (m[2] !== undefined) out.push({ kind: "code", text: m[2] });
    else out.push({ kind: "cite", ref: Number(m[3]) });
    last = m.index + m[0].length;
  }
  if (last < s.length) out.push({ kind: "text", text: s.slice(last) });
  return out;
}

const cells = (line: string): string[] => {
  let t = line.trim();
  if (t.startsWith("|")) t = t.slice(1);
  if (t.endsWith("|")) t = t.slice(0, -1);
  return t.split("|").map((c) => c.trim());
};
const isDivider = (line: string): boolean => /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line) && line.includes("-");
const bullet = /^\s*[-*•]\s+(.*)$/;
const numbered = /^\s*\d+[.)]\s+(.*)$/;

export function parseMarkdown(src: string): Block[] {
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length > 0) blocks.push({ kind: "p", inline: parseInline(para.join(" ")) });
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!;
    if (line.trim() === "") {
      flush();
      continue;
    }
    const next = lines[i + 1];
    if (line.includes("|") && next !== undefined && isDivider(next)) {
      flush();
      const head = cells(line).map(parseInline);
      const rows: Inline[][][] = [];
      i += 2;
      while (i < lines.length && lines[i]!.includes("|") && lines[i]!.trim() !== "") {
        rows.push(cells(lines[i]!).map(parseInline));
        i++;
      }
      i--;
      blocks.push({ kind: "table", head, rows });
      continue;
    }
    const heading = /^\s{0,3}#{1,6}\s+(.*)$/.exec(line);
    if (heading) {
      flush();
      blocks.push({ kind: "h", inline: parseInline(heading[1]!) });
      continue;
    }
    const b = bullet.exec(line) ?? null;
    const n = b ? null : numbered.exec(line);
    if (b || n) {
      flush();
      const kind = b ? "ul" : "ol";
      const items: Inline[][] = [];
      while (i < lines.length) {
        const m = (kind === "ul" ? bullet : numbered).exec(lines[i]!);
        if (!m) break;
        items.push(parseInline(m[1]!));
        i++;
      }
      i--;
      blocks.push({ kind, items });
      continue;
    }
    para.push(line.trim());
  }
  flush();
  return blocks;
}
