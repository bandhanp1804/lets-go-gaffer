import test from "node:test";
import assert from "node:assert/strict";
import { kickoffText, scoreText } from "./kickoff.ts";

test("a score, or a v before there is one", () => {
  assert.equal(scoreText(0, 1), "0 – 1");
  assert.equal(scoreText(2, 2), "2 – 2");
  assert.equal(scoreText(null, null), "v");
  assert.equal(scoreText(1, null), "v");
});

test("a kick-off time reads as a day and a time, and bad input as nothing", () => {
  const s = kickoffText("2026-10-24T14:00:00Z");
  assert.match(s, /^[A-Z][a-z]{2} 24 Oct, \d{2}:\d{2}$|^[A-Z][a-z]{2}, 24 Oct, \d{2}:\d{2}$|24 Oct/);
  assert.equal(kickoffText(""), "");
  assert.equal(kickoffText(undefined), "");
  assert.equal(kickoffText("not a date"), "");
});
