import test from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import { captureSnippet } from "./snippet.ts";

// Runs the snippet against a stand-in for an FPL page: a fetch, an XMLHttpRequest, a document.
function page() {
  const sent: string[] = []; // everything the page's own network calls were asked to do
  const appended: { value: string }[] = [];
  const logs: string[] = [];
  const box = { value: "", style: {}, focus() {}, select() {} };
  class XHR {
    setRequestHeader(_k: string, _v: string) {}
  }
  const ctx: Record<string, unknown> = {
    console: { log: (s: string) => logs.push(s) },
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
  vm.runInContext(captureSnippet, ctx);
  return { ctx, XHR, box, appended, logs, sent };
}

test("it shows the token from a fetch made with a plain headers object", async () => {
  const p = page();
  await (p.ctx.fetch as (u: string, i: object) => Promise<unknown>)("https://fantasy.premierleague.com/api/my-team/1/", { headers: { "X-Api-Authorization": "Bearer abc.def.ghi" } });
  assert.equal(p.box.value, "Bearer abc.def.ghi");
  assert.equal(p.appended.length, 1);
  assert.match(p.logs.join("\n"), /token copied/);
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
