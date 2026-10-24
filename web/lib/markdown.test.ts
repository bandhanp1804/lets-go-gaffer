import test from "node:test";
import assert from "node:assert/strict";
import { parseInline, parseMarkdown } from "./markdown.ts";

test("inline: bold, code and citations", () => {
  assert.deepEqual(parseInline("**Haaland** has `6.7` xp [1][2]."), [
    { kind: "bold", text: "Haaland" },
    { kind: "text", text: " has " },
    { kind: "code", text: "6.7" },
    { kind: "text", text: " xp " },
    { kind: "cite", ref: 1 },
    { kind: "cite", ref: 2 },
    { kind: "text", text: "." },
  ]);
});

test("inline: markup the model might emit stays inert text", () => {
  const got = parseInline('<img src=x onerror=alert(1)> [link](javascript:alert(1))');
  assert.ok(got.every((p) => p.kind === "text"), "no element other than text may come out of HTML or links");
});

test("paragraphs are separated by blank lines and wrapped lines are joined", () => {
  const got = parseMarkdown("one\ntwo\n\nthree");
  assert.equal(got.length, 2);
  assert.deepEqual(got[0], { kind: "p", inline: [{ kind: "text", text: "one two" }] });
});

test("bullet and numbered lists", () => {
  const got = parseMarkdown("- a\n- b\n\n1. x\n2. y");
  assert.deepEqual(got.map((b) => b.kind), ["ul", "ol"]);
  assert.equal((got[0] as { items: unknown[] }).items.length, 2);
});

test("a pipe table with a divider row", () => {
  const got = parseMarkdown("| Player | xp |\n|---|---|\n| Rice | 5.6 |\n| Gomez | 5.4 |\n\nafter");
  assert.equal(got[0]!.kind, "table");
  const t = got[0] as { head: unknown[]; rows: unknown[][] };
  assert.equal(t.head.length, 2);
  assert.equal(t.rows.length, 2);
  assert.equal(got[1]!.kind, "p");
});

test("a pipe in ordinary text is not a table", () => {
  assert.equal(parseMarkdown("a | b")[0]!.kind, "p");
});

test("headings", () => {
  assert.equal(parseMarkdown("## Captain")[0]!.kind, "h");
});
