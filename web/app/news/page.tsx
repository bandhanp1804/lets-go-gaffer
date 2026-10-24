"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { query } from "../../lib/api.ts";
import { webURL } from "../../lib/format.ts";
import type { News } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";

export default function News() {
  const [q, setQ] = useState("");
  const [player, setPlayer] = useState("");
  const [days, setDays] = useState("");
  const [applied, setApplied] = useState<string | null>(null);
  const data = useApi<{ stories: News[] }>(applied === null ? null : `/api/news${applied}`);

  return (
    <>
      <h1>News</h1>
      <p className="muted">Searches BBC Sport, Sky Sports and FPL’s own injury notes by meaning, not just keywords.</p>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          if (q.trim()) setApplied(query({ q: q.trim(), player: player.trim(), days }));
        }}
      >
        <label className="grow">
          What are you looking for?
          <input value={q} placeholder="e.g. who is injured at Arsenal" onChange={(e) => setQ(e.target.value)} required />
        </label>
        <label>
          Player (exact)
          <input value={player} onChange={(e) => setPlayer(e.target.value)} />
        </label>
        <label>
          Last N days
          <input type="number" min={1} value={days} onChange={(e) => setDays(e.target.value)} />
        </label>
        <button type="submit">Search</button>
      </form>

      {applied === null ? null : (
        <Async state={data}>
          {({ stories }) =>
            stories.length === 0 ? (
              <p className="muted">Nothing found.</p>
            ) : (
              stories.map((s) => {
                const href = webURL(s.url);
                return (
                  <article className="panel story" key={s.url + s.title}>
                    <h3>{href ? <a href={href} target="_blank" rel="noopener noreferrer">{s.title}</a> : s.title}</h3>
                    <p className="muted">
                      {s.source === "fpl" ? "Official FPL note" : s.source} · {s.age} ago
                      {s.players && s.players.length > 0 ? <> · {s.players.join(", ")}</> : null}
                    </p>
                    <p>{s.content}</p>
                  </article>
                );
              })
            )
          }
        </Async>
      )}
    </>
  );
}
