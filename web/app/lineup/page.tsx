"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { PlayerDetail } from "../../components/PlayerDetail.tsx";
import { Notes } from "../../components/Notes.tsx";
import { Pitch } from "../../components/Pitch.tsx";
import { one, signed, two } from "../../lib/format.ts";
import { query } from "../../lib/api.ts";
import type { Lineup, Player } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";

export default function LineupPage() {
  const [formation, setFormation] = useState(""); // empty: the best
  const [picked, setPicked] = useState<Player | null>(null);
  const lineup = useApi<Lineup>(`/api/lineup${query({ formation })}`);
  return (
    <>
      <h1>Best XI</h1>
      <Async state={lineup}>
        {(l) => (
          <>
            <p className="lead">
              GW{l.gameweek}: <strong>{l.formation}</strong>, {one(l.expected_points)} expected points with the captain counted twice.
            </p>
            <div className="chips">
              <span className={`chip ${l.source === "live" ? "good" : "warn"}`}>{l.source === "live" ? "Live from FPL" : "Public data"}</span>
              <span className="chip">Squad as of {l.squad_as_of}</span>
            </div>
            <Notes notes={l.notes} />

            {l.close_calls.length > 0 || l.captain_lead_over_vice < 0.3 ? (
              <div className="panel attention">
                <h2>Close calls</h2>
                <p className="muted">These are within the model’s noise: the numbers cannot separate them, so use what you know.</p>
                <ul>
                  {l.captain_lead_over_vice < 0.3 ? (
                    <li>
                      Captain: <strong>{l.captain}</strong> leads <strong>{l.vice_captain}</strong> by only {two(l.captain_lead_over_vice)}.
                    </li>
                  ) : null}
                  {l.close_calls.map((c) => (
                    <li key={c.bench_player + c.would_replace}>
                      Benched <strong>{c.bench_player}</strong> is {two(c.expected_points_behind)} behind <strong>{c.would_replace}</strong>, who starts.
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}

            <p>
              {l.same_xi_as_current ? (
                <>This is the same XI as {l.compared_with}.</>
              ) : (
                <>
                  Compared with {l.compared_with}: bring in <strong>{(l.players_in ?? []).join(", ")}</strong>, drop <strong>{(l.players_out ?? []).join(", ")}</strong>.
                </>
              )}
            </p>

            <div className="formations" role="group" aria-label="Formation">
              <span className="muted">Formation</span>
              {l.all_formations.map((f) => (
                <button
                  key={f.formation}
                  type="button"
                  className={`chip button${f.formation === l.formation ? " active" : ""}`}
                  aria-pressed={f.formation === l.formation}
                  onClick={() => {
                    setFormation(f.formation === l.best_formation ? "" : f.formation);
                    setPicked(null);
                  }}
                  title={f.formation === l.best_formation ? "The best formation" : `${signed(f.expected_points_vs_best)} expected points against the best`}
                >
                  {f.formation} {f.formation === l.best_formation ? "★" : signed(f.expected_points_vs_best)}
                </button>
              ))}
            </div>
            {l.formation !== l.best_formation ? (
              <p className="muted">
                Showing {l.formation}, {signed(l.expected_points_vs_best)} expected points against the best ({l.best_formation}).{" "}
                <button type="button" className="link" onClick={() => setFormation("")}>
                  Back to the best
                </button>
              </p>
            ) : null}
            {picked ? <PlayerDetail p={picked} onClose={() => setPicked(null)} /> : null}
            <Pitch starting={l.starting} bench={l.bench} captain={l.captain} vice={l.vice_captain} onSelect={(p) => setPicked(picked?.id === p.id ? null : p)} selectedId={picked?.id} />

          </>
        )}
      </Async>
    </>
  );
}
