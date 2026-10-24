"use client";

import { Async } from "../../components/Async.tsx";
import { Notes } from "../../components/Notes.tsx";
import { Pitch } from "../../components/Pitch.tsx";
import type { MyTeam } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";

export default function Team() {
  const team = useApi<MyTeam>("/api/team");
  return (
    <>
      <h1>My team</h1>
      <Async state={team}>
        {(t) => (
          <>
            <p className="lead">
              {t.team_name} <span className="muted">· {t.manager}</span>
            </p>
            <div className="chips">
              <span className={`chip ${t.source === "live" ? "good" : "warn"}`}>{t.source === "live" ? "Live from FPL" : "Public data"}</span>
              <span className="chip">As of {t.squad_as_of}</span>
              <span className="chip">Bank {t.bank}</span>
              <span className="chip">{t.free_transfers_left} free transfer{t.free_transfers_left === 1 ? "" : "s"}</span>
              {t.points_hit_so_far > 0 ? <span className="chip warn">−{t.points_hit_so_far} hit so far</span> : null}
              {(t.chips_available ?? []).map((c) => (
                <span className="chip" key={c}>{c}</span>
              ))}
            </div>
            <Notes notes={t.notes} />
            <Pitch starting={t.starting} bench={t.bench} captain={t.captain} vice={t.vice_captain} />
          </>
        )}
      </Async>
    </>
  );
}
