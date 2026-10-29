"use client";

import { useState } from "react";
import { Async } from "../../components/Async.tsx";
import { WatchButton } from "../../components/WatchButton.tsx";
import { one } from "../../lib/format.ts";
import { query } from "../../lib/api.ts";
import { availability } from "../../lib/pitch.ts";
import { nextSort, pageLabel, type Sort } from "../../lib/paging.ts";
import type { Players, Status } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";
import { useWatchlist } from "../../lib/watchlist.ts";
import { ClubTag } from "../../components/ClubTag.tsx";

interface Filters {
  position: string;
  team: string;
  name: string;
  status: string;
  min_price: string;
  max_price: string;
}

const noFilters: Filters = { position: "", team: "", name: "", status: "", min_price: "", max_price: "" };

const columns: { key: string; label: string; num?: boolean; title?: string }[] = [
  { key: "name", label: "Player" },
  { key: "team", label: "Club" },
  { key: "position", label: "Pos" },
  { key: "price", label: "Price", num: true },
  { key: "xp", label: "xp", num: true, title: "expected points per gameweek, next four gameweeks" },
  { key: "form", label: "Form", num: true },
  { key: "points", label: "Points", num: true },
];

export default function PlayersPage() {
  const [draft, setDraft] = useState<Filters>(noFilters); // what the form shows
  const [filters, setFilters] = useState<Filters>(noFilters); // what was searched
  const [sort, setSort] = useState<Sort>({ by: "xp", dir: "desc" });
  const [limit, setLimit] = useState(25);
  const [offset, setOffset] = useState(0);

  const status = useApi<Status>("/api/status");
  const data = useApi<Players>(`/api/players${query({ ...filters, sort_by: sort.by, sort_dir: sort.dir, limit, offset })}`);
  const watch = useWatchlist();
  const set = (k: keyof Filters) => (e: { target: { value: string } }) => setDraft({ ...draft, [k]: e.target.value });

  return (
    <>
      <h1>Players</h1>
      <p className="muted small">Click ☆ beside a name to watch him: his news and status changes arrive on the Alerts page.</p>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          setFilters(draft);
          setOffset(0);
        }}
      >
        <label>
          Position
          <select value={draft.position} onChange={set("position")}>
            <option value="">All</option>
            <option value="GKP">Goalkeepers</option>
            <option value="DEF">Defenders</option>
            <option value="MID">Midfielders</option>
            <option value="FWD">Forwards</option>
          </select>
        </label>
        <label>
          Club
          <select value={draft.team} onChange={set("team")}>
            <option value="">All</option>
            {(status.data?.clubs ?? []).map((c) => (
              <option key={c.short} value={c.short}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Availability
          <select value={draft.status} onChange={set("status")}>
            <option value="">Available</option>
            <option value="any">Any</option>
            <option value="doubtful">Doubtful</option>
            <option value="injured">Injured</option>
            <option value="suspended">Suspended</option>
            <option value="unavailable">Unavailable</option>
          </select>
        </label>
        <label>
          Min £m
          <input type="number" step="0.1" min={0} value={draft.min_price} onChange={set("min_price")} />
        </label>
        <label>
          Max £m
          <input type="number" step="0.1" min={0} value={draft.max_price} onChange={set("max_price")} />
        </label>
        <label>
          Name
          <input value={draft.name} placeholder="exact name" onChange={set("name")} />
        </label>
        <button type="submit">Search</button>
      </form>

      <Async state={data}>
        {(page) => (
          <div className={data.loading ? "dim" : undefined}>
            {page.players.length === 0 ? (
              <p className="muted">No players match.</p>
            ) : (
              <div className="tablewrap">
                <table>
                  <thead>
                    <tr>
                      {columns.map((c) => (
                        <th
                          key={c.key}
                          className={c.num ? "num" : undefined}
                          title={c.title}
                          aria-sort={sort.by === c.key ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
                        >
                          <button
                            className="sort"
                            onClick={() => {
                              setSort(nextSort(sort, c.key));
                              setOffset(0);
                            }}
                          >
                            {c.label}
                            <span aria-hidden="true">{sort.by === c.key ? (sort.dir === "asc" ? " ▲" : " ▼") : ""}</span>
                          </button>
                        </th>
                      ))}
                      <th>Status</th>
                      <th>Fixture</th>
                    </tr>
                  </thead>
                  <tbody>
                    {page.players.map((p) => (
                      <tr key={p.id} title={p.news || undefined}>
                        <td>
                          <WatchButton id={p.id} name={p.name} watch={watch} /> {p.name}
                        </td>
                        <td>
                          <ClubTag code={p.team} />
                        </td>
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
            )}
            <div className="pager">
              <span className="muted">{pageLabel(page.offset, page.players.length, page.total)}</span>
              <button disabled={page.offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>
                ← Previous
              </button>
              <button disabled={page.offset + page.players.length >= page.total} onClick={() => setOffset(offset + limit)}>
                Next →
              </button>
              <label className="inline">
                Per page
                <select
                  value={limit}
                  onChange={(e) => {
                    setLimit(Number(e.target.value));
                    setOffset(0);
                  }}
                >
                  {[25, 50, 100].map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </label>
            </div>
          </div>
        )}
      </Async>
    </>
  );
}
