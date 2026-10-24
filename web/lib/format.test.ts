import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, sourceLabel } from "./format.ts";

test("sourceLabel names each feed", () => {
  const cases: [string, string][] = [
    ["bbc-premier-league", "BBC Sport"],
    ["sky-football-news", "Sky Sports"],
    ["talksport-football", "TalkSport"],
    ["guardian-premier-league", "The Guardian"],
    ["fpl", "Official FPL note"],
    ["bbc", "BBC Sport"],
    ["skyline-blog", "skyline-blog"], // a prefix must end at the dash
    ["some-new-feed", "some-new-feed"],
  ];
  for (const [id, want] of cases) assert.equal(sourceLabel(id), want, id);
});

test("ago reads a past time in the largest whole unit", () => {
  const now = Date.parse("2026-10-24T12:00:00Z");
  const cases: [string, string][] = [
    ["2026-10-24T12:00:00Z", "just now"],
    ["2026-10-24T11:59:30Z", "just now"],
    ["2026-10-24T11:56:00Z", "4m ago"],
    ["2026-10-24T09:00:00Z", "3h ago"],
    ["2026-10-22T11:00:00Z", "2d ago"],
    ["2026-10-24T12:05:00Z", "just now"], // a clock a little ahead is not "in the future"
    ["not a time", ""],
  ];
  for (const [iso, want] of cases) assert.equal(ago(iso, now), want, iso);
});
