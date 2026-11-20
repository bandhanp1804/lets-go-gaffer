import assert from "node:assert/strict";
import { test } from "node:test";
import { addMade, currentMade, madeHeader, type MadeRecord, type MadeTransfer } from "./made.ts";

const t = (out: number, inn: number): MadeTransfer => ({ out, in: inn, outName: `P${out}`, inName: `P${inn}` });
const pairs = (ms: MadeTransfer[]) => ms.map((m) => `${m.out}>${m.in}`);

test("madeHeader writes sold>bought pairs", () => {
  assert.equal(madeHeader([]), "");
  assert.equal(madeHeader([t(101, 201), t(113, 206)]), "101>201,113>206");
});

test("addMade keeps the public squad's view: chains collapse, a buy-back cancels", () => {
  const cases: { name: string; existing: MadeTransfer[]; add: MadeTransfer[]; want: string[] }[] = [
    { name: "first transfers", existing: [], add: [t(1, 2), t(3, 4)], want: ["1>2", "3>4"] },
    { name: "an unrelated one is added", existing: [t(1, 2)], add: [t(3, 4)], want: ["1>2", "3>4"] },
    { name: "a player bought then sold again: one transfer from the original", existing: [t(1, 2)], add: [t(2, 5)], want: ["1>5"] },
    { name: "the original player bought back: no transfer at all", existing: [t(1, 2)], add: [t(2, 1)], want: [] },
  ];
  for (const c of cases) assert.deepEqual(pairs(addMade(c.existing, c.add)), c.want, c.name);
});

test("addMade keeps the names of the original sale and the latest buy", () => {
  const [m] = addMade([t(1, 2)], [t(2, 5)]);
  assert.equal(m?.outName, "P1");
  assert.equal(m?.inName, "P5");
});

test("currentMade applies only to the same team and gameweek", () => {
  const rec: MadeRecord = { teamId: 42, gameweek: 11, moves: [t(1, 2)] };
  const cases: [MadeRecord | null, number | null, number | null, number][] = [
    [rec, 42, 11, 1],
    [rec, 42, null, 1], // gameweek not known yet: keep them
    [rec, 42, 12, 0], // the deadline has passed: FPL shows them now
    [rec, 7, 11, 0], // another team
    [rec, null, 11, 0],
    [null, 42, 11, 0],
  ];
  for (const [r, team, gw, want] of cases) assert.equal(currentMade(r, team, gw).length, want, `${team} ${gw}`);
});
