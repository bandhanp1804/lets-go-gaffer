"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { WatchButton } from "../../components/WatchButton.tsx";
import { postJSON } from "../../lib/api.ts";
import { webURL } from "../../lib/format.ts";
import type { Alerts } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";
import { useWatchlist, type WatchState } from "../../lib/watchlist.ts";

const kindLabel: Record<string, string> = { watch: "Watchlist", briefing: "Briefing", health: "Model health" };

export default function AlertsPage() {
  const data = useApi<Alerts>("/api/alerts");
  const watch = useWatchlist();
  const [problem, setProblem] = useState("");

  async function markAllRead(upTo: number) {
    setProblem("");
    try {
      await postJSON("/api/alerts/read", { up_to: upTo });
      window.dispatchEvent(new Event("gaffer:alerts-read"));
      data.reload();
    } catch (e) {
      setProblem(e instanceof Error ? e.message : "Could not mark them read.");
    }
  }

  return (
    <>
      <h1>Alerts</h1>
      <p className="muted">
        News and status changes for the players you watch, in your own inbox. (The site’s owners also get the gameweek briefing and the model’s health warnings here.)
      </p>
      <Async state={data}>
        {(a) => (
          <>
            {!a.live ? (
              <p className="panel attention small">
                Nothing new will arrive until the background worker is running: set <code>TEMPORAL_ADDRESS</code> in <code>.env</code>, start Temporal (
                <code>docker compose -f deploy/docker/docker-compose.yml up -d</code>) and run <code>gaffer worker</code>.
              </p>
            ) : a.temporal_ui ? (
              <p className="small muted">
                Background work runs in Temporal:{" "}
                <a href={a.temporal_ui} target="_blank" rel="noopener noreferrer">
                  see the workflows ↗
                </a>
              </p>
            ) : null}

            <section className="panel">
              <h2>Watching</h2>
              {watch.ids.size === 0 ? (
                <p className="muted small">No one yet. Star a player on the Players or News page to watch him.</p>
              ) : (
                <WatchedList watch={watch} />
              )}
              {watch.error ? <p className="error small">{watch.error}</p> : null}
            </section>

            <div className="alerts-head">
              <h2>
                Inbox{a.unread > 0 ? <span className="unread">{a.unread}</span> : null}
              </h2>
              {a.unread > 0 && a.alerts.length > 0 ? (
                <button type="button" onClick={() => void markAllRead(Math.max(...a.alerts.map((x) => x.id)))}>
                  Mark all read
                </button>
              ) : null}
            </div>
            {problem ? <p className="error small">{problem}</p> : null}
            {a.alerts.length === 0 ? (
              <p className="muted">No alerts yet.</p>
            ) : (
              <ul className="alerts">
                {a.alerts.map((al) => {
                  const href = webURL(al.url);
                  return (
                    <li key={al.id} className={`panel alert kind-${al.kind} ${al.read ? "" : "unread-row"}`}>
                      <div className="small muted">
                        {kindLabel[al.kind] ?? al.kind} · {al.age} ago
                      </div>
                      <strong>{al.title}</strong>
                      {al.body ? <p className="alert-body">{al.body}</p> : null}
                      {href ? (
                        <a className="small" href={href} target="_blank" rel="noopener noreferrer">
                          Read the story ↗
                        </a>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            )}
          </>
        )}
      </Async>
    </>
  );
}

function WatchedList({ watch }: { watch: WatchState }) {
  return (
    <ul className="watched">
      {watch.players.map((p) => (
        <li key={p.id}>
          <WatchButton id={p.id} name={p.name} watch={watch} /> <strong>{p.name}</strong> <span className="muted">{p.team}</span>{" "}
          <span className={`pill ${p.status.startsWith("available") ? "" : "waiting"}`}>{p.status}</span>
        </li>
      ))}
    </ul>
  );
}
