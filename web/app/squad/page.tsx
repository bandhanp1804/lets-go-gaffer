"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { Notes } from "../../components/Notes.tsx";
import { Pitch } from "../../components/Pitch.tsx";
import { one } from "../../lib/format.ts";
import { query } from "../../lib/api.ts";
import type { Squad } from "../../lib/types.ts";
import { useApi } from "../../lib/useApi.ts";

export default function SquadPage() {
  const [horizon, setHorizon] = useState(4);
  const data = useApi<Squad>(`/api/squad${query({ horizon })}`);
  return (
    <>
      <h1>Squad builder</h1>
      <p className="muted">The best legal squad from scratch under the budget, ignoring the players you own. For a Wildcard, Free Hit or a fresh start.</p>
      <div className="form">
        <label>
          Look ahead
          <select value={horizon} onChange={(e) => setHorizon(Number(e.target.value))}>
            {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => (
              <option key={n} value={n}>
                {n} gameweek{n === 1 ? "" : "s"}
              </option>
            ))}
          </select>
        </label>
      </div>
      <Async state={data}>
        {(s) => (
          <>
            <div className="chips">
              <span className="chip">GW{s.gameweek}</span>
              <span className="chip">{s.formation}</span>
              <span className="chip">
                Cost {s.cost} of {s.budget}
              </span>
              <span className="chip">{one(s.expected_points)} xp for the XI</span>
            </div>
            <Notes notes={s.notes} />
            <Pitch starting={s.starting} bench={s.bench} captain={s.captain} vice={s.vice_captain} />
          </>
        )}
      </Async>
    </>
  );
}
