import test from "node:test";
import assert from "node:assert/strict";
import { maxReplacementPrice, movesFromParam, movesToParam, parsePrice, pendingSlot, requestFor, setIn, toggleOut } from "./whatif.ts";

test("clicking a player marks him for sale, and clicking again unmarks him", () => {
  let m = toggleOut([], 101);
  assert.deepEqual(m, [{ out: 101, in: null }]);
  m = toggleOut(m, 102);
  assert.equal(m.length, 2);
  m = toggleOut(m, 101);
  assert.deepEqual(m, [{ out: 102, in: null }]);
});

test("choosing and clearing the player to buy", () => {
  const m = setIn(toggleOut(toggleOut([], 101), 102), 102, 202);
  assert.deepEqual(m, [{ out: 101, in: null }, { out: 102, in: 202 }]);
  assert.deepEqual(setIn(m, 102, null)[1], { out: 102, in: null });
  assert.deepEqual(setIn(m, 999, 5), m, "an unknown sale changes nothing");
});

test("the next slot to fill is the first sale without a buy", () => {
  assert.equal(pendingSlot([]), null);
  assert.equal(pendingSlot([{ out: 1, in: 2 }]), null);
  assert.equal(pendingSlot([{ out: 1, in: 2 }, { out: 3, in: null }, { out: 4, in: null }]), 3);
});

test("only complete moves are sent, and nothing at all when there are none", () => {
  assert.equal(requestFor([{ out: 1, in: null }], 4, null), null);
  assert.deepEqual(requestFor([{ out: 1, in: 2 }, { out: 3, in: null }], 6, 2), { moves: [{ out: 1, in: 2 }], horizon: 6, free_transfers: 2 });
  assert.equal(requestFor([{ out: 1, in: 2 }], 4, null)?.free_transfers, null);
});

test("a plan survives the trip through an address", () => {
  const moves = [{ out: 101, in: 201 }, { out: 102, in: 202 }];
  assert.equal(movesToParam(moves), "101:201,102:202");
  assert.deepEqual(movesFromParam("101:201,102:202"), moves);
});

test("a hand-edited address cannot smuggle in anything but pairs of ids", () => {
  assert.deepEqual(movesFromParam(null), []);
  assert.deepEqual(movesFromParam(""), []);
  assert.deepEqual(movesFromParam("101:201,abc,1:2:3,0:5,5:0,-1:4,<script>:1,102:202"), [{ out: 101, in: 201 }, { out: 102, in: 202 }]);
  assert.deepEqual(movesFromParam("101:201,101:202"), [{ out: 101, in: 201 }], "a player is sold once");
  assert.equal(movesFromParam(Array.from({ length: 40 }, (_, i) => `${i + 1}:${i + 100}`).join(",")).length, 15);
});

test("prices", () => {
  assert.equal(parsePrice("£5.9m"), 5.9);
  assert.equal(parsePrice("£10.0m"), 10);
  assert.equal(parsePrice("5.9"), null);
  assert.equal(parsePrice(undefined), null);
  assert.equal(maxReplacementPrice("£5.0m", "£0.9m"), 5.9);
  assert.equal(maxReplacementPrice("£4.3m", "£0.2m"), 4.5);
  assert.equal(maxReplacementPrice(undefined, "£0.9m"), null);
});
