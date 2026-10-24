import test from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import { captureSnippet } from "./snippet.ts";

// Runs the snippet against a stand-in for an FPL page: a fetch, an XMLHttpRequest, storage.
function page(storage: { local?: Record<string, string>; session?: Record<string, string> } = {}, opts: { noCopy?: boolean } = {}) {
  const sent: string[] = []; // everything the page's own network calls were asked to do
  const logs: string[] = [];
  const copied: string[] = [];
  let touchedDocument = false;
  class XHR {
    setRequestHeader(_k: string, _v: string) {}
  }
  const store = (items: Record<string, string> = {}) => ({
    get length() { return Object.keys(items).length; },
    key: (i: number) => Object.keys(items)[i] ?? null,
    getItem: (k: string) => items[k] ?? null,
  });
  const ctx: Record<string, unknown> = {
    atob: globalThis.atob,
    localStorage: store(storage.local),
    sessionStorage: store(storage.session),
    console: { log: (...a: unknown[]) => logs.push(a.join(" ")) },
    ...(opts.noCopy ? {} : { copy: (s: string) => copied.push(s) }), // the console's own copy() helper
    XMLHttpRequest: XHR,
    // Nothing is drawn on the page: a text box can be read by browser extensions.
    document: new Proxy({}, { get() { touchedDocument = true; return () => {}; } }),
  };
  ctx.window = ctx;
  ctx.fetch = async (input: unknown, init?: unknown) => {
    sent.push(JSON.stringify([input, init]));
    return {};
  };
  vm.createContext(ctx);
  const returned = vm.runInContext(captureSnippet, ctx);
  return { ctx, XHR, logs, copied, sent, returned, touched: () => touchedDocument };
}

const b64 = (o: object) => Buffer.from(JSON.stringify(o)).toString("base64url");
const jwtExp = (secondsFromNow: number, extra: object = {}) => `${b64({ alg: "x" })}.${b64({ exp: Math.floor(Date.now() / 1000) + secondsFromNow, ...extra })}.sig`;
const fetchWith = (p: ReturnType<typeof page>, init: object) => (p.ctx.fetch as (u: unknown, i?: object) => Promise<unknown>)("https://fantasy.premierleague.com/api/my-team/1/", init);

// The token is on a line of its own, so it can be selected and copied cleanly.
const tokenLines = (p: ReturnType<typeof page>) => p.logs.filter((l) => /^(Bearer )?[\w-]+\.[\w-]+\.[\w-]+$/.test(l));

test("a token the page sends is printed on its own line and copied", async () => {
  const p = page();
  await fetchWith(p, { headers: { "X-Api-Authorization": "Bearer abc.def.ghi" } });
  assert.deepEqual(tokenLines(p), ["Bearer abc.def.ghi"]);
});

// copy() is a helper of the console, available while it evaluates the script and not afterwards,
// so a token caught later cannot be copied for the user and the script must not claim it was.
test("it only says it copied the token when it really could", () => {
  const without = page({ local: { k: jwtExp(600) } }, { noCopy: true });
  assert.equal(tokenLines(without).length, 1);
  assert.ok(!without.logs.join("\n").includes("copied to your clipboard"), "no copy() means nothing was copied");

  const withCopy = page({ local: { k: jwtExp(600) } });
  assert.equal(withCopy.copied.length, 1);
  assert.match(withCopy.logs.join("\n"), /copied to your clipboard/);
});

test("it reads headers given as a Headers object, as pairs, on a Request, and through XMLHttpRequest", async () => {
  const a = page();
  await fetchWith(a, { headers: new Headers({ "x-api-authorization": "Bearer t.o.k" }) });
  assert.deepEqual(tokenLines(a), ["Bearer t.o.k"]);

  const b = page();
  await fetchWith(b, { headers: [["Content-Type", "x"], ["X-Api-Authorization", "Bearer p.a.irs"]] });
  assert.deepEqual(tokenLines(b), ["Bearer p.a.irs"]);

  const c = page();
  await (c.ctx.fetch as (u: unknown) => Promise<unknown>)(new Request("https://x/", { headers: { "x-api-authorization": "Bearer r.e.quest" } }));
  assert.deepEqual(tokenLines(c), ["Bearer r.e.quest"]);

  const d = page();
  new d.XHR().setRequestHeader("x-api-authorization", "Bearer x.y.z");
  assert.deepEqual(tokenLines(d), ["Bearer x.y.z"]);
});

test("only the first token caught is printed, and other headers are ignored", () => {
  const p = page();
  new p.XHR().setRequestHeader("content-type", "application/json");
  assert.deepEqual(tokenLines(p), []);
  new p.XHR().setRequestHeader("X-Api-Authorization", "Bearer one.two.three");
  new p.XHR().setRequestHeader("X-Api-Authorization", "Bearer four.five.six");
  assert.deepEqual(tokenLines(p), ["Bearer one.two.three"]);
});

test("a placeholder like undefined or null is ignored and it keeps listening for the real token", () => {
  const p = page();
  for (const junk of ["undefined", "null", "Bearer undefined", "", "Bearer ", "abc", "a.b"]) new p.XHR().setRequestHeader("x-api-authorization", junk);
  assert.deepEqual(tokenLines(p), [], "a non-token must never be printed as the token");
  new p.XHR().setRequestHeader("x-api-authorization", "Bearer real.token.value");
  assert.deepEqual(tokenLines(p), ["Bearer real.token.value"]);
});

test("the page's own requests still go through unchanged, and a failure inside its call never breaks it", async () => {
  const p = page();
  await fetchWith(p, { method: "GET" });
  assert.equal(p.sent.length, 1);
  const weird = { get: () => { throw new Error("boom"); } };
  await assert.doesNotReject(() => fetchWith(p, { headers: weird }));
});

// The point of showing the script before running it: it must not send anything anywhere,
// and must not put the token into the page, where an extension could read it.
test("it makes no network request of its own and draws nothing on the page", () => {
  const p = page({ local: { k: jwtExp(600) } });
  new p.XHR().setRequestHeader("x-api-authorization", "Bearer a.b.c");
  assert.equal(p.sent.length, 0);
  assert.equal(p.touched(), false, "the script must not touch the document");
  assert.ok(!/\bfetch\s*\(|XMLHttpRequest\s*\(|\.open\(|sendBeacon|WebSocket|new Image|createElement|textarea|innerHTML/.test(captureSnippet.replace(/realFetch\.apply|window\.fetch = function/g, "")), "no call that sends data, no element created");
});

test("it returns a readable message that says not to reload", () => {
  const p = page();
  assert.equal(typeof p.returned, "string");
  assert.match(p.returned as string, /NOT reload/i);
  assert.doesNotMatch(p.returned as string, /refresh your/i);
});

test("with nothing in storage and no request yet, no token is printed", () => {
  const p = page();
  assert.deepEqual(tokenLines(p), []);
  assert.deepEqual(p.copied, []);
});

test("a stored token is printed and copied, and the others are listed by name only", () => {
  const access = jwtExp(3600, { scope: "fpl" });
  const id = jwtExp(3600, { nonce: "n" });
  const stored = JSON.stringify({ id_token: id, session_state: "s", access_token: access, token_type: "Bearer" });
  const p = page({ local: { theme: "dark", "oidc.user:https://account.example/as:abc": stored } });

  assert.deepEqual(tokenLines(p), [access], "the access token, not the id token that is stored first");
  assert.deepEqual(p.copied, [access]);
  const log = p.logs.join("\n");
  assert.match(log, /oidc\.user:https:\/\/account\.example\/as:abc > access_token/);
  assert.match(log, /oidc\.user:https:\/\/account\.example\/as:abc > id_token/);
  assert.equal(log.split(id).length - 1, 0, "the id token's value must not be printed");
  assert.equal(log.split(access).length - 1, 1, "the chosen token is printed once");
});

test("a token inside nested stored JSON is found; expired ones and lookalikes are not", () => {
  const good = jwtExp(1800);
  assert.deepEqual(tokenLines(page({ session: { auth: JSON.stringify({ user: { value: good, id: "x" } }) } })), [good]);
  assert.deepEqual(tokenLines(page({ local: { old: jwtExp(-60) } })), [], "an expired token is no use");
  assert.deepEqual(tokenLines(page({ local: { a: "a.b.c d", b: "undefined", c: "1.2.3", d: "{ not json" } })), [], "three parts and no readable expiry is not a token");
});

test("a token seen on a real request is printed after one guessed from storage", () => {
  const stored = jwtExp(1800);
  const p = page({ local: { guess: stored } });
  assert.deepEqual(tokenLines(p), [stored]);

  new p.XHR().setRequestHeader("x-api-authorization", "Bearer real.header.token");

  assert.deepEqual(tokenLines(p), [stored, "Bearer real.header.token"], "the real one is printed last, so it is the one to copy");
});

test("unreadable storage never breaks the script", () => {
  const blocked = { get length(): number { throw new Error("blocked"); }, key: () => null, getItem: () => null };
  const ctx: Record<string, unknown> = {
    atob: globalThis.atob, localStorage: blocked, sessionStorage: blocked, console: { log: () => {} },
    XMLHttpRequest: class { setRequestHeader() {} }, document: {},
  };
  ctx.window = ctx;
  ctx.fetch = async () => ({});
  vm.createContext(ctx);
  assert.doesNotThrow(() => vm.runInContext(captureSnippet, ctx));
});
