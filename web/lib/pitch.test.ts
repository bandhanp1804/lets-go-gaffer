import test from "node:test";
import assert from "node:assert/strict";
import { availability, pitchLines, roleOf } from "./pitch.ts";
import type { Player } from "./types.gen.ts";

const p = (name: string, position: string): Player => ({
  id: 0, name, team: "ARS", position, price: "£5.0m", xp: 1, form: "1", total_points: 1, status: "available",
});

test("lines come out goalkeeper to forward, in server order, and empty lines are dropped", () => {
  const got = pitchLines([p("F1", "FWD"), p("D1", "DEF"), p("G", "GKP"), p("D2", "DEF"), p("F2", "FWD")]);
  assert.deepEqual(got.map((l) => l.line), ["GKP", "DEF", "FWD"]);
  assert.deepEqual(got[1]!.players.map((x) => x.name), ["D1", "D2"]);
});

test("availability reads the server's status text", () => {
  assert.equal(availability("available"), "ok");
  assert.equal(availability("doubtful (50%)"), "doubt");
  assert.equal(availability("injured"), "out");
  assert.equal(availability("suspended"), "out");
  assert.equal(availability("unavailable"), "out");
});

test("captain and vice", () => {
  assert.equal(roleOf("Haaland", "Haaland", "Salah"), "C");
  assert.equal(roleOf("Salah", "Haaland", "Salah"), "V");
  assert.equal(roleOf("Rice", "Haaland", "Salah"), "");
});
