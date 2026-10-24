"use client";

import Link from "next/link";
import { Async } from "../components/Async.tsx";
import { Hero } from "../components/Hero.tsx";
import { Notes } from "../components/Notes.tsx";
import { useTeam } from "../components/TeamProvider.tsx";
import { one, signed } from "../lib/format.ts";
import type { Lineup, Status } from "../lib/types.gen.ts";
import { useApi } from "../lib/useApi.ts";

const features = [
  { href: "/lineup", icon: "①", title: "Best XI", text: "The eleven with the best expected points, the captain, and the close calls the numbers cannot settle." },
  { href: "/transfers", icon: "⇄", title: "Transfers", text: "Every move weighed against its 4-point hit. Wildcard planning and what-if free transfers." },
  { href: "/ask", icon: "💬", title: "Ask the Gaffer", text: "Plain-English answers that quote the tools and cite the news, with their numbers checked." },
  { href: "/players", icon: "☰", title: "Players", text: "Every player, sorted and filtered by expected points, form, price and availability." },
  { href: "/squad", icon: "▦", title: "Squad builder", text: "The best legal fifteen from scratch under £100m. Made for Wildcards." },
  { href: "/model", icon: "📈", title: "The model", text: "How well it predicted weeks already played, checked on gameweeks it never saw." },
];

export default function Home() {
  const { identity, ready, openModal } = useTeam();
  const status = useApi<Status>("/api/status");
  const hasTeam = identity.teamId !== null || status.data?.team_configured === true;
  const lineup = useApi<Lineup>(hasTeam ? "/api/lineup" : null);
  const s = status.data;

  return (
    <>
      <Hero>
        <p className="kicker">Fantasy Premier League · scouting room</p>
        <h1 className="herotitle">
          Let’s go, <span>Gaffer.</span>
        </h1>
        <p className="herosub">Your squad, your lineup, your transfers and the latest team news, picked apart by a model that shows its working.</p>
        <div className="ctas">
          <button type="button" className="primary big" onClick={openModal} disabled={!ready}>
            {identity.teamId === null ? "Load my team" : `Switch team (${identity.teamId})`}
          </button>
          <Link href="/players" className="ghost big">
            Browse players
          </Link>
        </div>

        {s ? (
          <div className="scoreboard" role="group" aria-label="Season status">
            <div className="cell">
              <span className="label">Next deadline</span>
              <span className="digits">{s.deadline_in}</span>
              <span className="sub">{s.gameweek_name || `GW${s.gameweek}`}</span>
            </div>
            <div className="cell">
              <span className="label">In play</span>
              <span className="digits">{s.live_gameweek > 0 ? `GW${s.live_gameweek}` : "—"}</span>
              <span className="sub">{s.live_gameweek > 0 ? "matches under way" : "between gameweeks"}</span>
            </div>
            <div className="cell">
              <span className="label">Data through</span>
              <span className="digits">GW{s.history_through_gameweek}</span>
              <span className="sub">{s.players} players</span>
            </div>
            <div className="cell">
              <span className="label">News stored</span>
              <span className="digits">{s.news_articles}</span>
              <span className="sub">{s.news_search_available ? "search on" : "search off"}</span>
            </div>
          </div>
        ) : null}
      </Hero>

      {s ? <Notes notes={s.notes} /> : null}

      {hasTeam ? (
        <>
          <h2>This week{identity.teamId !== null ? ` · team ${identity.teamId}` : ""}</h2>
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
                <p className="muted">
                  {l.source === "live" ? "Live from FPL" : "Public data"} · squad as of {l.squad_as_of}.{" "}
                  {l.source !== "live" ? "Add your login token (Load my team → Live squad) to include transfers made since the deadline." : null}
                </p>
                <Link href="/lineup">See the full lineup →</Link>
              </div>
            )}
          </Async>
        </>
      ) : null}

      <h2>The dressing room</h2>
      <div className="features">
        {features.map((f) => (
          <Link key={f.href} href={f.href} className="feature">
            <span className="icon" aria-hidden="true">{f.icon}</span>
            <strong>{f.title}</strong>
            <span className="muted">{f.text}</span>
          </Link>
        ))}
      </div>
    </>
  );
}
