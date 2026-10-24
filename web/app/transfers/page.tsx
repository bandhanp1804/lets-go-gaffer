"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { Notes } from "../../components/Notes.tsx";
import { one, signed } from "../../lib/format.ts";
import { query } from "../../lib/api.ts";
import type { Transfers } from "../../lib/types.ts";
import { useApi } from "../../lib/useApi.ts";

export default function TransfersPage() {
  const [wildcard, setWildcard] = useState(false);
  const [free, setFree] = useState("");
  const [horizon, setHorizon] = useState("");
  const [max, setMax] = useState("");
  const [applied, setApplied] = useState("");
  const data = useApi<Transfers>(`/api/transfers${applied}`);

  return (
    <>
      <h1>Transfers</h1>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(query({ wildcard, free_transfers: free, horizon, max_transfers: max }));
        }}
      >
        <label className="check">
          <input type="checkbox" checked={wildcard} onChange={(e) => setWildcard(e.target.checked)} /> Wildcard / Free Hit
        </label>
        <label>
          Free transfers (what if)
          <input type="number" min={0} value={free} placeholder="actual" onChange={(e) => setFree(e.target.value)} />
        </label>
        <label>
          Horizon (gameweeks)
          <input type="number" min={1} max={10} value={horizon} placeholder="4" onChange={(e) => setHorizon(e.target.value)} />
        </label>
        <label>
          Most moves
          <input type="number" min={1} max={5} value={max} placeholder="2" onChange={(e) => setMax(e.target.value)} />
        </label>
        <button type="submit">Update</button>
      </form>

      <Async state={data}>
        {(t) => (
          <>
            <div className="chips">
              <span className="chip">GW{t.gameweek}</span>
              <span className="chip">{t.free_transfers_left} free transfer{t.free_transfers_left === 1 ? "" : "s"}</span>
              <span className="chip">Bank {t.bank}</span>
              <span className="chip">Gain counted over {t.horizon_gameweeks} gameweeks</span>
              {t.wildcard ? <span className="chip good">Wildcard: no points hit</span> : null}
              <span className="chip">Squad as of {t.squad_as_of}</span>
            </div>
            <Notes notes={t.notes} />
            {t.suggestions.length === 0 ? (
              <div className="panel">
                <strong>Keep the squad as it is.</strong> No move is worth its cost.
              </div>
            ) : (
              t.suggestions.map((s, i) => (
                <div className="panel" key={i}>
                  <div className="suggest-head">
                    <strong>
                      {s.transfers} transfer{s.transfers === 1 ? "" : "s"}
                    </strong>
                    <span className={s.points_hit > 0 ? "warn" : "good-text"}>{s.points_hit > 0 ? `−${s.points_hit} points hit` : "no hit"}</span>
                    <span>net {one(s.net_points_over_horizon)} over {t.horizon_gameweeks} gameweeks</span>
                    <span className="muted">+{one(s.expected_gain_per_gameweek)} a gameweek</span>
                    <span className="muted">bank after {s.bank_after}</span>
                    {i > 0 ? (
                      <span className={Math.abs(s.net_points_vs_first_option) < 0.3 ? "warn" : "muted"}>
                        {signed(s.net_points_vs_first_option)} vs option 1{Math.abs(s.net_points_vs_first_option) < 0.3 ? " (toss-up)" : ""}
                      </span>
                    ) : null}
                  </div>
                  <ul className="moves">
                    {s.moves.map((m) => (
                      <li key={m.out.id}>
                        <span className="out">{m.out.name}</span> <span className="muted">({m.out.team}, {m.out.position}) sells for {m.out.sell_price}
                        {m.out.fixture ? `, plays ${m.out.fixture}` : ""}</span>
                        <span className="arrow"> → </span>
                        <span className="in">{m.in.name}</span> <span className="muted">({m.in.team}, {m.in.position}) costs {m.in.price}
                        {m.in.fixture ? `, plays ${m.in.fixture}` : ""}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              ))
            )}
          </>
        )}
      </Async>
    </>
  );
}
