import assert from "node:assert/strict";
import { test } from "node:test";
import { canSignOut, signOutURL } from "./signout.ts";

test("signOutURL signs out on production and returns there", () => {
  const cases: [string, string][] = [
    ["letsgogaffer.tech", "https://letsgogaffer.tech/oauth2/sign_out?rd=https%3A%2F%2Fletsgogaffer.tech%2Fsigned-out"],
    ["pr-12.letsgogaffer.tech", "https://letsgogaffer.tech/oauth2/sign_out?rd=https%3A%2F%2Fletsgogaffer.tech%2Fsigned-out"],
    ["gaffer.1-2-3-4.sslip.io", "https://gaffer.1-2-3-4.sslip.io/oauth2/sign_out?rd=https%3A%2F%2Fgaffer.1-2-3-4.sslip.io%2Fsigned-out"],
    ["pr-12.gaffer.1-2-3-4.sslip.io", "https://gaffer.1-2-3-4.sslip.io/oauth2/sign_out?rd=https%3A%2F%2Fgaffer.1-2-3-4.sslip.io%2Fsigned-out"],
    ["print.example.com", "https://print.example.com/oauth2/sign_out?rd=https%3A%2F%2Fprint.example.com%2Fsigned-out"], // only pr-<number>. is a preview
  ];
  for (const [host, want] of cases) assert.equal(signOutURL(host), want, host);
});

test("canSignOut only behind the login", () => {
  assert.equal(canSignOut("alice@example.com"), true);
  assert.equal(canSignOut("local"), false);
  assert.equal(canSignOut(""), false);
  assert.equal(canSignOut(undefined), false);
});
