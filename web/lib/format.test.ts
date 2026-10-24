import assert from "node:assert/strict";
import { test } from "node:test";
import { sourceLabel } from "./format.ts";

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
