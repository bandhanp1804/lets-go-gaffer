"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { query } from "../../lib/api.ts";
import { sourceLabel, webURL } from "../../lib/format.ts";
import { relevance } from "../../lib/relevance.ts";
import type { NewsSearch } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";

export default function News() {
  const [q, setQ] = useState("");
  const [player, setPlayer] = useState("");
  const [days, setDays] = useState("");
  const [applied, setApplied] = useState<{ q: string; player: string; days: string; everything: boolean } | null>(null);
  const path = applied === null ? null : `/api/news${query({ q: applied.q, player: applied.player, days: applied.days, detect: applied.everything ? "off" : undefined })}`;
  const data = useApi<NewsSearch>(path);

  return (
    <>
      <h1>News</h1>
      <p className="muted">Searches BBC Sport, Sky Sports, TalkSport, the Guardian and FPL’s own injury notes by meaning, not just keywords. Name a player and it looks at stories about him.</p>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          if (q.trim()) setApplied({ q: q.trim(), player: player.trim(), days, everything: false });
        }}
      >
        <label className="grow">
          What are you looking for?
          <input value={q} placeholder="e.g. is Saka injured?" onChange={(e) => setQ(e.target.value)} required />
        </label>
        <label>
          Player (optional)
          <input value={player} placeholder="found from your question" onChange={(e) => setPlayer(e.target.value)} />
        </label>
        <label>
          Last N days
          <input type="number" min={1} value={days} onChange={(e) => setDays(e.target.value)} />
        </label>
        <button type="submit">Search</button>
      </form>

      {applied === null ? null : (
        <Async state={data}>
          {(r) => (
            <>
              {r.player ? (
                <div className="panel playerbox">
                  <strong>{r.player.name}</strong>{" "}
                  <span className="muted">
                    {r.player.team} · {r.player.position} · {r.player.price}
                  </span>
                  <span className={`pill ${r.player.status.startsWith("available") ? "" : "waiting"}`}>FPL: {r.player.status}</span>
                  {r.player.news ? <div className="muted">{r.player.news}</div> : null}
                  {r.player_detected ? (
                    <div className="small">
                      Found in your question.{" "}
                      <button type="button" className="link" onClick={() => setApplied({ ...applied, everything: true })}>
                        Not him? Search all news
                      </button>
                    </div>
                  ) : null}
                </div>
              ) : null}

              {r.notes.length > 0 ? (
                <ul className="notes">
                  {r.notes.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              ) : null}

              {r.stories.length === 0 ? (
                <p className="muted">Nothing found.</p>
              ) : (
                r.stories.map((s) => {
                  const href = webURL(s.url);
                  const rel = relevance(s.similarity);
                  return (
                    <article className="panel story" key={s.url + s.title}>
                      <h3>{href ? <a href={href} target="_blank" rel="noopener noreferrer">{s.title}</a> : s.title}</h3>
                      <p className="muted">
                        {sourceLabel(s.source)} · {s.age} ago
                        {s.players && s.players.length > 0 ? <> · {s.players.join(", ")}</> : null}{" "}
                        <span className={`pill rel-${rel.level}`} title={`similarity ${s.similarity.toFixed(2)}`}>{rel.label}</span>
                      </p>
                      <p>{s.content}</p>
                      {href ? <p className="small"><a href={href} target="_blank" rel="noopener noreferrer">Read on {sourceLabel(s.source)} ↗</a></p> : null}
                    </article>
                  );
                })
              )}
              {r.newest_story_age ? <p className="muted small">The newest story stored is {r.newest_story_age} old.</p> : null}
            </>
          )}
        </Async>
      )}
    </>
  );
}
