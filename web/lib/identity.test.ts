import test from "node:test";
import assert from "node:assert/strict";
import { cleanToken, expiresIn, headersFor, parseTeamId, tokenExpiry } from "./identity.ts";

const b64url = (o: object) => Buffer.from(JSON.stringify(o)).toString("base64url");
const jwt = (exp: number) => `${b64url({ alg: "none" })}.${b64url({ exp })}.sig_nature-1`;

test("team id: a number, or any FPL address that contains it", () => {
  assert.equal(parseTeamId("1234567"), 1234567);
  assert.equal(parseTeamId("  1234567 "), 1234567);
  assert.equal(parseTeamId("https://fantasy.premierleague.com/entry/1234567/event/8"), 1234567);
  assert.equal(parseTeamId("https://fantasy.premierleague.com/entry/1234567/history"), 1234567);
  assert.equal(parseTeamId("fantasy.premierleague.com/entry/1234567"), 1234567);
  assert.equal(parseTeamId("https://fantasy.premierleague.com/entry/1234567?x=1"), 1234567);
});

test("team id: rejects what is not one", () => {
  for (const bad of ["", "abc", "0", "-5", "12.5", "1234567890123", "https://fantasy.premierleague.com/", "entry/abc"]) {
    assert.equal(parseTeamId(bad), null, JSON.stringify(bad));
  }
});

test("token: accepts the forms people paste", () => {
  const t = jwt(2_000_000_000);
  for (const pasted of [t, `Bearer ${t}`, `bearer   ${t}`, `x-api-authorization: Bearer ${t}`, `"${t}"`, `  ${t}\n`, `X-Api-Authorization:${t}`]) {
    assert.deepEqual(cleanToken(pasted), { ok: true, token: t }, pasted.slice(0, 30));
  }
});

test("token: rejects text that cannot be one, with a reason", () => {
  for (const bad of ["", "   ", "not a token", "<script>alert(1)</script>", "one.two", "a.b.c d", "a".repeat(5000) + ".b.c"]) {
    const r = cleanToken(bad);
    assert.equal(r.ok, false, JSON.stringify(bad.slice(0, 20)));
    if (!r.ok) assert.ok(r.reason.length > 0);
  }
});

test("token expiry is read from the payload", () => {
  assert.equal(tokenExpiry(jwt(1_700_000_000)), 1_700_000_000_000);
  assert.equal(tokenExpiry("garbage"), null);
  assert.equal(tokenExpiry("a.!!!.c"), null);
  assert.equal(tokenExpiry(`a.${b64url({ nope: 1 })}.c`), null);
});

test("expiresIn: minutes, hours, expired, unknown", () => {
  const now = 1_000_000_000_000;
  assert.equal(expiresIn(jwt(now / 1000 + 47 * 60 + 10), now), "47 min");
  assert.equal(expiresIn(jwt(now / 1000 + 65 * 60), now), "1h 5m");
  assert.equal(expiresIn(jwt(now / 1000 - 1), now), "expired");
  assert.equal(expiresIn("garbage", now), null);
});

test("headers: only what is set, and the token never goes in a URL", () => {
  assert.deepEqual(headersFor({ teamId: null, token: "" }), {});
  assert.deepEqual(headersFor({ teamId: 42, token: "" }), { "X-Gaffer-Team": "42" });
  assert.deepEqual(headersFor({ teamId: 42, token: "abc.def.ghi" }), { "X-Gaffer-Team": "42", "X-Gaffer-Token": "abc.def.ghi" });
});
