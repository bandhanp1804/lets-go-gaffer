import test from "node:test";
import assert from "node:assert/strict";
import { nextSort, pageLabel } from "./paging.ts";

test("clicking the sorted column flips its direction", () => {
  assert.deepEqual(nextSort({ by: "xp", dir: "desc" }, "xp"), { by: "xp", dir: "asc" });
  assert.deepEqual(nextSort({ by: "price", dir: "asc" }, "price"), { by: "price", dir: "desc" });
});

test("a new column starts best-first for scores and A-to-Z or cheapest-first for the rest", () => {
  assert.deepEqual(nextSort({ by: "name", dir: "asc" }, "points"), { by: "points", dir: "desc" });
  assert.deepEqual(nextSort({ by: "xp", dir: "desc" }, "form"), { by: "form", dir: "desc" });
  assert.deepEqual(nextSort({ by: "xp", dir: "desc" }, "price"), { by: "price", dir: "asc" });
  assert.deepEqual(nextSort({ by: "xp", dir: "desc" }, "name"), { by: "name", dir: "asc" });
});

test("page labels", () => {
  assert.equal(pageLabel(0, 25, 668), "1–25 of 668");
  assert.equal(pageLabel(650, 18, 668), "651–668 of 668");
  assert.equal(pageLabel(0, 0, 0), "0 players");
  assert.equal(pageLabel(700, 0, 668), "668 players"); // an offset past the end shows no range
});
