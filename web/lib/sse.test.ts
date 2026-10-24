import test from "node:test";
import assert from "node:assert/strict";
import { createSSEParser, type SSEEvent } from "./sse.ts";

function collect(): { feed: (s: string) => void; events: SSEEvent[] } {
  const events: SSEEvent[] = [];
  return { feed: createSSEParser((e) => events.push(e)), events };
}

test("parses whole events", () => {
  const { feed, events } = collect();
  feed('event: session\ndata: {"session_id":"x"}\n\nevent: text\ndata: {"delta":"Hi"}\n\n');
  assert.deepEqual(events, [
    { event: "session", data: '{"session_id":"x"}' },
    { event: "text", data: '{"delta":"Hi"}' },
  ]);
});

test("an event split across chunks, even mid-line, arrives once and whole", () => {
  const { feed, events } = collect();
  const text = 'event: text\ndata: {"delta":"Hello"}\n\n';
  for (const ch of text) feed(ch);
  assert.deepEqual(events, [{ event: "text", data: '{"delta":"Hello"}' }]);
});

test("a chunk boundary between the two newlines does not end the event early", () => {
  const { feed, events } = collect();
  feed("event: a\ndata: 1\n");
  assert.equal(events.length, 0);
  feed("\n");
  assert.equal(events.length, 1);
});

test("CRLF line endings", () => {
  const { feed, events } = collect();
  feed("event: done\r\ndata: ok\r\n\r\n");
  assert.deepEqual(events, [{ event: "done", data: "ok" }]);
});

test("multi-line data is joined with newlines; comments and data-less blocks are ignored", () => {
  const { feed, events } = collect();
  feed(": keep-alive\n\nevent: x\ndata: a\ndata: b\n\n");
  assert.deepEqual(events, [{ event: "x", data: "a\nb" }]);
});

test("an event with no name is a message; an incomplete trailing event is not delivered", () => {
  const { feed, events } = collect();
  feed("data: hi\n\nevent: partial\ndata: nope");
  assert.deepEqual(events, [{ event: "message", data: "hi" }]);
});
