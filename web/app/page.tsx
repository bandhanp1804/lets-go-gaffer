"use client";

import Link from "next/link";
import { Async } from "../components/Async.tsx";
import { Notes } from "../components/Notes.tsx";
import { one, signed } from "../lib/format.ts";
import type { Lineup, Status } from "../lib/types.gen.ts";
import { useApi } from "../lib/useApi.ts";

export default function Dashboard() {
  const status = useApi<Status>("/api/status");
  const lineup = useApi<Lineup>("/api/lineup");
  return (
    <>
      <h1>Dashboard</h1>
      <Async state={status}>
        {(s) => (
          <>
            <div className="tiles">
              <div className="tile">
                <span className="label">Next deadline</span>
                <span className="big">{s.gameweek_name || `GW${s.gameweek}`}</span>
                <span className="sub">
                  in {s.deadline_in}
                  {s.live_gameweek > 0 ? ` · GW${s.live_gameweek} is being played now` : ""}
                </span>
              </div>
              <div className="tile">
                <span className="label">Player data</span>
                <span className="big">through GW{s.history_through_gameweek}</span>
                <span className="sub">
                  {s.players} players, last finished GW{s.last_finished_gameweek}
                </span>
              </div>
              <div className="tile">
                <span className="label">News stored</span>
                <span className="big">{s.news_articles}</span>
                <span className="sub">{s.news_search_available ? "search on" : "search off (no VOYAGE_API_KEY)"}</span>
              </div>
              <div className="tile">
                <span className="label">FPL session</span>
                <span className="big">{s.session.state}</span>
                <span className="sub">{s.session.expires_in ? `expires in ${s.session.expires_in}` : s.session.state === "expired" ? "public data only: paste a new token into .env, no restart needed" : " "}</span>
              </div>
            </div>
            <Notes notes={s.notes} />
          </>
        )}
      </Async>

      <h2>This week</h2>
      <Async state={lineup}>
        {(l) => (
          <div className="panel">
            <p className="lead">
              Captain <strong>{l.captain}</strong>, vice-captain <strong>{l.vice_captain}</strong>, in a <strong>{l.formation}</strong> for GW{l.gameweek}.
            </p>
            <p>
              Expected points for the XI: <strong>{one(l.expected_points)}</strong>.{" "}
              {l.captain_lead_over_vice < 0.3 ? (
                <span className="warn">The captain choice is a toss-up (lead {signed(l.captain_lead_over_vice)}).</span>
              ) : (
                <>Captain leads the vice by {signed(l.captain_lead_over_vice)}.</>
              )}
            </p>
            <p className="muted">Squad as of {l.squad_as_of}.</p>
            <Link href="/lineup">See the full lineup →</Link>
          </div>
        )}
      </Async>
    </>
  );
}
