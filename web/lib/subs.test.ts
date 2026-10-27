import assert from "node:assert/strict";
import { test } from "node:test";
import { clickForSubs, noSubs, subsMarks, subsRequest } from "./subs.ts";

test("a starter, then a bench player, makes a swap", () => {
  let d = clickForSubs(noSubs, 110, true);
  assert.deepEqual(d, { swaps: [], pending: 110 });
  d = clickForSubs(d, 115, false);
  assert.deepEqual(d, { swaps: [{ out: 110, in: 115 }], pending: null });
  assert.deepEqual(subsRequest(d), { swaps: [{ out: 110, in: 115 }] });
});

test("clicking the picked starter again unpicks him; a bench player alone does nothing", () => {
  assert.deepEqual(clickForSubs(clickForSubs(noSubs, 110, true), 110, true), noSubs);
  assert.equal(clickForSubs(noSubs, 115, false), noSubs);
});

test("clicking either player of a swap undoes it", () => {
  const d = { swaps: [{ out: 110, in: 115 }, { out: 102, in: 114 }], pending: null };
  assert.deepEqual(clickForSubs(d, 115, false).swaps, [{ out: 102, in: 114 }]);
  assert.deepEqual(clickForSubs(d, 102, true).swaps, [{ out: 110, in: 115 }]);
});

test("picking another starter replaces the one picked", () => {
  assert.equal(clickForSubs(clickForSubs(noSubs, 110, true), 111, true).pending, 111);
});

test("marks and the request", () => {
  const d = { swaps: [{ out: 110, in: 115 }], pending: 102 };
  assert.deepEqual([...subsMarks(d)], [[110, "off"], [115, "on"], [102, "off"]]);
  assert.equal(subsRequest(noSubs), null);
});
