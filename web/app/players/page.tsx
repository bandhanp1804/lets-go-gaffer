"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { one } from "../../lib/format.ts";
import { query } from "../../lib/api.ts";
import { availability } from "../../lib/pitch.ts";
import type { Player, Status } from "../../lib/types.ts";
import { useApi } from "../../lib/useApi.ts";

interface Filters {
  position: string;
  team: string;
  name: string;
  status: string;
  sort_by: string;
  min_price: string;
  max_price: string;
  limit: string;
}

const initial: Filters = { position: "", team: "", name: "", status: "", sort_by: "", min_price: "", max_price: "", limit: "25" };

export default function Players() {
  const [f, setF] = useState<Filters>(initial);
  const [applied, setApplied] = useState(query({ limit: initial.limit }));
  const clubs = useApi<Status>("/api/status");
  const data = useApi<{ players: Player[] }>(`/api/players${applied}`);
  const set = (k: keyof Filters) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });

  return (
    <>
      <h1>Players</h1>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          setApplied(query({ ...f }));
        }}
      >
        <label>
          Position
          <select value={f.position} onChange={set("position")}>
            <option value="">All</option>
            <option value="GKP">Goalkeepers</option>
            <option value="DEF">Defenders</option>
            <option value="MID">Midfielders</option>
            <option value="FWD">Forwards</option>
          </select>
        </label>
        <label>
          Club
          <select value={f.team} onChange={set("team")}>
            <option value="">All</option>
            {(clubs.data?.clubs ?? []).map((c) => (
              <option key={c.short} value={c.short}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Availability
          <select value={f.status} onChange={set("status")}>
            <option value="">Available</option>
            <option value="doubtful">Doubtful</option>
            <option value="injured">Injured</option>
            <option value="suspended">Suspended</option>
            <option value="unavailable">Unavailable</option>
            <option value="any">Any</option>
          </select>
        </label>
        <label>
          Sort by
          <select value={f.sort_by} onChange={set("sort_by")}>
            <option value="">Expected points</option>
            <option value="form">Form</option>
            <option value="price">Price</option>
            <option value="points">Total points</option>
          </select>
        </label>
        <label>
          Min £m
          <input type="number" step="0.1" min={0} value={f.min_price} onChange={set("min_price")} />
        </label>
        <label>
          Max £m
          <input type="number" step="0.1" min={0} value={f.max_price} onChange={set("max_price")} />
        </label>
        <label>
          Name
          <input value={f.name} placeholder="exact name" onChange={set("name")} />
        </label>
        <label>
          Show
          <select value={f.limit} onChange={set("limit")}>
            {[10, 25].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <button type="submit">Search</button>
      </form>

      <Async state={data}>
        {({ players }) =>
          players.length === 0 ? (
            <p className="muted">No players match.</p>
          ) : (
            <div className="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th>Player</th>
                    <th>Club</th>
                    <th>Pos</th>
                    <th className="num">Price</th>
                    <th className="num" title="expected points per gameweek, next four gameweeks">xp</th>
                    <th className="num">Form</th>
                    <th className="num">Points</th>
                    <th>Status</th>
                    <th>Fixture</th>
                  </tr>
                </thead>
                <tbody>
                  {players.map((p) => (
                    <tr key={p.id} title={p.news || undefined}>
                      <td>{p.name}</td>
                      <td>{p.team}</td>
                      <td>{p.position}</td>
                      <td className="num">{p.price}</td>
                      <td className="num">{one(p.xp)}</td>
                      <td className="num">{p.form}</td>
                      <td className="num">{p.total_points}</td>
                      <td className={`status-${availability(p.status)}`}>{p.status}</td>
                      <td>{p.fixture}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </Async>
    </>
  );
}
