import test from "node:test";
import assert from "node:assert/strict";
import { relevance } from "./relevance.ts";

test("scores read as strong, possible or loose, with the boundaries on the stronger side", () => {
  assert.equal(relevance(0.54).level, "strong");
  assert.equal(relevance(0.5).level, "strong");
  assert.equal(relevance(0.49).level, "ok");
  assert.equal(relevance(0.4).level, "ok");
  assert.equal(relevance(0.38).level, "loose");
  assert.equal(relevance(0).level, "loose");
});
