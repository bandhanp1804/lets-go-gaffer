import test from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import { captureSnippet } from "./snippet.ts";

// Runs the snippet against a stand-in for an FPL page: a fetch, an XMLHttpRequest, a document.
function page(storage: { local?: Record<string, string>; session?: Record<string, string> } = {}) {
  const sent: string[] = []; // everything the page's own network calls were asked to do
  const appended: { value: string }[] = [];
  const logs: string[] = [];
  const box = { value: "", style: {}, focus() {}, select() {} };
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
    XMLHttpRequest: XHR,
    document: {
      createElement: () => box,
      body: { appendChild: (b: { value: string }) => appended.push(b) },
      execCommand: () => true,
    },
  };
  ctx.window = ctx;
  ctx.fetch = async (input: unknown, init?: unknown) => {
    sent.push(JSON.stringify([input, init]));
    return {};
  };
  vm.createContext(ctx);
  const returned = vm.runInContext(captureSnippet, ctx);
  return { ctx, XHR, box, appended, logs, sent, returned };
}

test("it shows the token from a fetch made with a plain headers object", async () => {
  const p = page();
  await (p.ctx.fetch as (u: string, i: object) => Promise<unknown>)("https://fantasy.premierleague.com/api/my-team/1/", { headers: { "X-Api-Authorization": "Bearer abc.def.ghi" } });
  assert.equal(p.box.value, "Bearer abc.def.ghi");
  assert.equal(p.appended.length, 1);
  assert.match(p.logs.join("\n"), /copied it|Ctrl\+C/);
});

test("it shows the token from a fetch made with a Headers object", async () => {
  const p = page();
  const h = new Headers({ "x-api-authorization": "Bearer t.o.k" });
  await (p.ctx.fetch as (u: string, i: object) => Promise<unknown>)("https://x/", { headers: h });
  assert.equal(p.box.value, "Bearer t.o.k");
});

test("it shows the token from XMLHttpRequest.setRequestHeader", () => {
  const p = page();
  new p.XHR().setRequestHeader("x-api-authorization", "Bearer x.y.z");
  assert.equal(p.box.value, "Bearer x.y.z");
});

test("other headers are ignored, and the box appears only once", async () => {
  const p = page();
  new p.XHR().setRequestHeader("content-type", "application/json");
  assert.equal(p.appended.length, 0);
  new p.XHR().setRequestHeader("X-Api-Authorization", "Bearer one.two.three");
  new p.XHR().setRequestHeader("X-Api-Authorization", "Bearer four.five.six");
  assert.equal(p.appended.length, 1);
  assert.equal(p.box.value, "Bearer one.two.three");
});

test("the page's own requests still go through unchanged", async () => {
  const p = page();
  await (p.ctx.fetch as (u: string, i: object) => Promise<unknown>)("https://fantasy.premierleague.com/api/x", { method: "GET" });
  assert.equal(p.sent.length, 1);
});

// The point of showing the script before running it: it must not send anything anywhere.
test("it makes no network request of its own", () => {
  const p = page();
  new p.XHR().setRequestHeader("x-api-authorization", "Bearer a.b.c");
  assert.equal(p.sent.length, 0);
  assert.ok(!/\bfetch\s*\(|XMLHttpRequest\s*\(|\.open\(|sendBeacon|WebSocket|new Image/.test(captureSnippet.replace(/realFetch\.apply|window\.fetch = function/g, "")), "the script must not contain a call that sends data");
});

test("it returns a readable message, so the console does not just echo undefined", () => {
  const p = page();
  assert.equal(typeof p.returned, "string");
  assert.match(p.returned as string, /listening/);
});

// FPL's page may send the header before its login state has loaded, with a placeholder.
test("a placeholder like undefined or null is ignored and it keeps listening for the real token", () => {
  const p = page();
  for (const junk of ["undefined", "null", "Bearer undefined", "", "Bearer ", "abc", "a.b"]) new p.XHR().setRequestHeader("x-api-authorization", junk);
  assert.equal(p.appended.length, 0, "a non-token must never be shown as the token");

  new p.XHR().setRequestHeader("x-api-authorization", "Bearer real.token.value");

  assert.equal(p.box.value, "Bearer real.token.value");
});

test("it reads headers given as pairs and as a Request's headers", async () => {
  const pairs = page();
  await (pairs.ctx.fetch as (u: string, i: object) => Promise<unknown>)("https://x/", { headers: [["Content-Type", "x"], ["X-Api-Authorization", "Bearer p.a.irs"]] });
  assert.equal(pairs.box.value, "Bearer p.a.irs");

  const req = page();
  await (req.ctx.fetch as (u: unknown) => Promise<unknown>)(new Request("https://x/", { headers: { "x-api-authorization": "Bearer r.e.quest" } }));
  assert.equal(req.box.value, "Bearer r.e.quest");
});

test("a failure inside the page's own call never breaks the page", async () => {
  const p = page();
  const weird = { get: () => { throw new Error("boom"); } };
  await assert.doesNotReject(() => (p.ctx.fetch as (u: string, i: object) => Promise<unknown>)("https://x/", { headers: weird }));
});

const b64 = (o: object) => Buffer.from(JSON.stringify(o)).toString("base64url");
const jwtExp = (secondsFromNow: number) => `${b64({ alg: "x" })}.${b64({ exp: Math.floor(Date.now() / 1000) + secondsFromNow })}.sig`;

test("it must not tell the manager to reload: that removes the script", () => {
  const p = page();
  assert.match(p.returned as string, /NOT reload/i);
  assert.doesNotMatch(p.returned as string, /refresh your/i);
});

test("with nothing in storage and no request yet, no box appears", () => {
  assert.equal(page().appended.length, 0);
});

test("a token-shaped value in storage is offered, and its key is named in the console but never its value", () => {
  const t = jwtExp(1800);
  const p = page({ local: { theme: "dark", "oidc.user:abc": t } });
  assert.equal(p.box.value, t);
  assert.equal(p.appended.length, 1);
  const log = p.logs.join("\n");
  assert.match(log, /oidc\.user:abc/);
  assert.ok(!log.includes(t), "the token itself must not be printed to the console log");
});

test("a token inside stored JSON is found, an expired one and a lookalike are not", () => {
  const good = jwtExp(1800);
  const nested = page({ session: { auth: JSON.stringify({ user: { access_token: good, id: "x" } }) } });
  assert.equal(nested.box.value, good);

  const expired = page({ local: { old: jwtExp(-60) } });
  assert.equal(expired.appended.length, 0, "an expired token is no use");

  const junk = page({ local: { a: "a.b.c d", b: "undefined", c: "1.2.3", d: "{ not json" } });
  assert.equal(junk.appended.length, 0, "a value with three parts but no readable expiry is not a token");
});

test("the token seen on a real request replaces one guessed from storage", () => {
  const stored = jwtExp(1800);
  const p = page({ local: { guess: stored } });
  assert.equal(p.box.value, stored);

  new p.XHR().setRequestHeader("x-api-authorization", "Bearer real.header.token");

  assert.equal(p.box.value, "Bearer real.header.token");
  assert.equal(p.appended.length, 1, "the same box is reused");
});

test("unreadable storage never breaks the script", () => {
  const ctxStore = { get length(): number { throw new Error("blocked"); }, key: () => null, getItem: () => null };
  const sent: unknown[] = [];
  const ctx: Record<string, unknown> = {
    atob: globalThis.atob, localStorage: ctxStore, sessionStorage: ctxStore, console: { log: () => {} },
    XMLHttpRequest: class { setRequestHeader() {} }, document: { createElement: () => ({ style: {} }), body: { appendChild: () => {} }, execCommand: () => true },
  };
  ctx.window = ctx;
  ctx.fetch = async (...a: unknown[]) => sent.push(a);
  vm.createContext(ctx);
  assert.doesNotThrow(() => vm.runInContext(captureSnippet, ctx));
});
