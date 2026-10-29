"use client";

import { useState } from "react";
import { Async } from "./Async.tsx";
import { query } from "../lib/api.ts";
import { one } from "../lib/format.ts";
import { availability } from "../lib/pitch.ts";
import type { Player, Players } from "../lib/types.gen.ts";
import { useApi } from "../lib/useApi.ts";
import { ClubTag } from "./ClubTag.tsx";

interface Props {
  /** The player being replaced: candidates are of his position. */
  out: Player;
  /** The most a replacement can cost, in £m, or null for no limit. */
  maxPrice: number | null;
  /** Players already in the squad, who cannot be bought. */
  ownedIds: Set<number>;
  onPick: (p: Player) => void;
  onCancel: () => void;
}

/** A shortlist of players who could replace one of yours: same position, affordable, best first. */
export function Candidates({ out, maxPrice, ownedIds, onPick, onCancel }: Props) {
  const [text, setText] = useState("");
  const [sortBy, setSortBy] = useState("xp");
  const [anyone, setAnyone] = useState(false);
  const data = useApi<Players>(`/api/players${query({ position: out.position, max_price: maxPrice ?? undefined, sort_by: sortBy, status: anyone ? "any" : "", limit: 100 })}`);
  const needle = text.trim().toLowerCase();

  return (
    <div className="panel candidates" role="region" aria-label={`Replacements for ${out.name}`}>
      <div className="detailhead">
        <strong>Replace {out.name}</strong>
        <span className="muted">
          {out.position}, sells for {out.sell_price ?? out.price}
          {maxPrice !== null ? `, up to £${maxPrice.toFixed(1)}m with your bank` : ""}
        </span>
        <button type="button" className="x" aria-label="Cancel" onClick={onCancel}>
          ×
        </button>
      </div>
      <div className="form compact">
        <label className="grow">
          Search
          <input value={text} placeholder="name or club" onChange={(e) => setText(e.target.value)} autoFocus />
        </label>
        <label>
          Sort by
          <select value={sortBy} onChange={(e) => setSortBy(e.target.value)}>
            <option value="xp">Expected points</option>
            <option value="price">Price</option>
            <option value="form">Form</option>
            <option value="points">Total points</option>
          </select>
        </label>
        <label className="check">
          <input type="checkbox" checked={anyone} onChange={(e) => setAnyone(e.target.checked)} /> Include injured
        </label>
      </div>
      <Async state={data}>
        {(page) => {
          const rows = page.players.filter((p) => !ownedIds.has(p.id) && (needle === "" || p.name.toLowerCase().includes(needle) || p.team.toLowerCase().includes(needle)));
          return rows.length === 0 ? (
            <p className="muted">No one matches. Try a bigger budget or clear the search.</p>
          ) : (
            <div className="tablewrap scroll">
              <table>
                <thead>
                  <tr>
                    <th>Player</th>
                    <th>Club</th>
                    <th className="num">Price</th>
                    <th className="num" title="expected points per gameweek">xp</th>
                    <th className="num">Form</th>
                    <th>Status</th>
                    <th>Next</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {rows.map((p) => (
                    <tr key={p.id}>
                      <td>{p.name}</td>
                      <td>
                        <ClubTag code={p.team} />
                      </td>
                      <td className="num">{p.price}</td>
                      <td className="num">{one(p.xp)}</td>
                      <td className="num">{p.form}</td>
                      <td className={`status-${availability(p.status)}`}>{p.status}</td>
                      <td>{p.fixture}</td>
                      <td>
                        <button type="button" className="buy" onClick={() => onPick(p)} aria-label={`Buy ${p.name}`}>
                          Buy
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }}
      </Async>
    </div>
  );
}
