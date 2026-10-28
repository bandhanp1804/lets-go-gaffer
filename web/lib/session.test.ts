import assert from "node:assert/strict";
import { test } from "node:test";
import { isPublicPath, sessionFrom, signInURL } from "./session.ts";

test("sessionFrom reads oauth2-proxy's answer", () => {
  const cases: [number, string][] = [
    [202, "in"], // signed in
    [200, "in"],
    [401, "out"], // not signed in
    [403, "out"],
    [404, "in"], // no login at all: the dev server, the local user
    [500, "in"], // the check itself failed: let the app's own requests decide
  ];
  for (const [status, want] of cases) assert.equal(sessionFrom(status), want, String(status));
});

test("isPublicPath matches the pages served without a login", () => {
  const cases: [string, boolean][] = [
    ["/", true],
    ["/privacy", true],
    ["/terms", true],
    ["/signed-out", true],
    ["/players", false],
    ["/privacy/x", false],
  ];
  for (const [path, want] of cases) assert.equal(isPublicPath(path), want, path);
});

test("signInURL returns to the home page", () => {
  assert.equal(signInURL, "/oauth2/start?rd=%2F");
});
