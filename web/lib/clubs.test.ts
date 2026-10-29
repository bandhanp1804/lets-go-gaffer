import assert from "node:assert/strict";
import { test } from "node:test";
import { clubColours, clubs, contrast } from "./clubs.ts";

test("every club's text reads on its shirt (WCAG AA, 4.5:1)", () => {
  for (const [code, c] of Object.entries(clubs)) {
    assert.match(code, /^[A-Z]{3}$/, code);
    for (const v of [c.bg, c.trim, c.text]) assert.match(v, /^#[0-9a-f]{6}$/, `${code} ${v}`);
    const ratio = contrast(c.bg, c.text);
    assert.ok(ratio >= 4.5, `${code}: ${c.text} on ${c.bg} is ${ratio.toFixed(2)}:1`);
  }
});

test("clubColours looks codes up loosely and leaves unknown clubs alone", () => {
  const cases: [string, string | undefined][] = [
    ["ARS", "#db0007"],
    ["ars", "#db0007"],
    [" TOT ", "#ffffff"],
    ["XYZ", undefined],
    ["", undefined],
  ];
  for (const [code, want] of cases) assert.equal(clubColours(code)?.bg, want, JSON.stringify(code));
});

test("contrast matches WCAG's reference values", () => {
  const cases: [string, string, number][] = [
    ["#000000", "#ffffff", 21],
    ["#ffffff", "#ffffff", 1],
    ["#777777", "#ffffff", 4.48],
  ];
  for (const [a, b, want] of cases) assert.ok(Math.abs(contrast(a, b) - want) < 0.01, `${a} on ${b}: ${contrast(a, b)}`);
});
