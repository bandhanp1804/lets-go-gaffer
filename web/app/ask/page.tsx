"use client";

import { useEffect, useRef, useState } from "react";
import { Markdown } from "../../components/Markdown.tsx";
import { ApiError, ask } from "../../lib/api.ts";
import { webURL } from "../../lib/format.ts";
import type { AskDone } from "../../lib/types.gen.ts";

interface Turn {
  question: string;
  text: string; // streamed so far
  done?: AskDone;
  error?: string;
}

const examples = ["Who should I captain this week?", "Should I make any transfers this week?", "Which goalkeepers are injured?", "What's the latest on Saka?"];

export default function Ask() {
  const [turns, setTurns] = useState<Turn[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const session = useRef<string | undefined>(undefined);
  const abort = useRef<AbortController | undefined>(undefined);
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    return () => {
      abort.current?.abort();
    };
  }, []);
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [turns]);

  const patchLast = (f: (t: Turn) => Turn) => setTurns((ts) => ts.map((t, i) => (i === ts.length - 1 ? f(t) : t)));

  async function send(question: string) {
    const q = question.trim();
    if (!q || busy) return;
    setInput("");
    setBusy(true);
    setTurns((ts) => [...ts, { question: q, text: "" }]);
    abort.current = new AbortController();
    try {
      await ask(
        q,
        session.current,
        {
          onSession: (id) => (session.current = id),
          onText: (d) => patchLast((t) => ({ ...t, text: t.text + d })),
          onDone: (done) => patchLast((t) => ({ ...t, done, text: done.text })),
          onError: (message) => patchLast((t) => ({ ...t, error: message })),
        },
        abort.current.signal,
      );
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) session.current = undefined; // the conversation expired on the server
      if (!abort.current.signal.aborted) patchLast((t) => ({ ...t, error: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <h1>Ask</h1>
      <p className="muted">
        Questions about your squad, lineup, transfers, players and the news. The assistant can read but cannot change anything on FPL, and the numbers it quotes are checked against what the tools returned.
      </p>

      {turns.length === 0 ? (
        <div className="chips">
          {examples.map((e) => (
            <button key={e} className="chip button" onClick={() => send(e)}>
              {e}
            </button>
          ))}
        </div>
      ) : null}

      <div className="chat">
        {turns.map((t, i) => (
          <div key={i} className="turn">
            <div className="q">{t.question}</div>
            <div className="a">
              {t.text ? <Markdown text={t.text} /> : t.error ? null : <span className="muted">Thinking…</span>}
              {t.error ? <div className="error" role="alert">{t.error}</div> : null}
              {t.done && t.done.warnings.length > 0 ? (
                <div className="warnbox">
                  <strong>Checks: verify these before relying on the answer</strong>
                  <ul>
                    {t.done.warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                </div>
              ) : null}
              {t.done && t.done.sources.length > 0 ? (
                <ol className="sources">
                  {t.done.sources.map((s) => {
                    const href = webURL(s.url);
                    return (
                      <li key={s.ref} id={`source-${s.ref}`} value={s.ref}>
                        {href ? <a href={href} target="_blank" rel="noopener noreferrer">{s.title}</a> : s.title}{" "}
                        <span className="muted">
                          {s.source === "fpl" ? "official FPL note" : s.source}, {s.age} ago
                        </span>
                      </li>
                    );
                  })}
                </ol>
              ) : null}
              {t.done ? (
                <details className="meta">
                  <summary>
                    {t.done.tools.length} tool call{t.done.tools.length === 1 ? "" : "s"} · {t.done.usage.input_tokens + t.done.usage.output_tokens} tokens
                  </summary>
                  <ul>
                    {t.done.tools.map((c, j) => (
                      <li key={j}>
                        <code>{c.name}</code> {c.input} <span className="muted">{c.duration_ms} ms{c.error ? `, error: ${c.error}` : ""}</span>
                      </li>
                    ))}
                  </ul>
                </details>
              ) : null}
            </div>
          </div>
        ))}
        <div ref={bottom} />
      </div>

      <form
        className="askbar"
        onSubmit={(e) => {
          e.preventDefault();
          void send(input);
        }}
      >
        <input value={input} onChange={(e) => setInput(e.target.value)} placeholder="Ask about your squad, a player, or the news…" aria-label="Your question" maxLength={2000} autoFocus />
        <button type="submit" disabled={busy || !input.trim()}>
          {busy ? "Answering…" : "Ask"}
        </button>
      </form>
    </>
  );
}
