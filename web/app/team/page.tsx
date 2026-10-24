"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { PlayerDetail } from "../../components/PlayerDetail.tsx";
import { PointsPanel } from "../../components/PointsPanel.tsx";
import { Notes } from "../../components/Notes.tsx";
import { Pitch } from "../../components/Pitch.tsx";
import type { MyTeam, Player } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";

export default function Team() {
  const team = useApi<MyTeam>("/api/team");
  const [picked, setPicked] = useState<Player | null>(null);
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
                <span className="chip" key={c} title="a chip you can still play">chip: {c}</span>
              ))}
            </div>
            {t.points ? <PointsPanel p={t.points} /> : null}
            <Notes notes={t.notes} />
            <p className="muted">Click a player for details and where his points came from.</p>
            {picked ? (
              <PlayerDetail p={picked} points={t.points?.players.find((x) => x.id === picked.id)} gameweek={t.points?.gameweek} onClose={() => setPicked(null)} />
            ) : null}
            <Pitch starting={t.starting} bench={t.bench} captain={t.captain} vice={t.vice_captain} onSelect={(p) => setPicked(picked?.id === p.id ? null : p)} selectedId={picked?.id} />
          </>
        )}
      </Async>
    </>
  );
}
