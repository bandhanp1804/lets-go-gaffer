import { availability } from "../lib/pitch.ts";
import { one } from "../lib/format.ts";
import { kickoffText, scoreText } from "../lib/kickoff.ts";
import type { ReactNode } from "react";
import type { Player, PointsPlayer } from "../lib/types.gen.ts";
import { ClubTag } from "./ClubTag.tsx";

/**
 * What is known about one player, shown when a card is clicked. With a points line it also shows
 * where the points came from, match by match, as FPL's own panel does.
 */
export function PlayerDetail({ p, points, gameweek, actions, onClose }: { p: Player; points?: PointsPlayer; gameweek?: number; actions?: ReactNode; onClose: () => void }) {
  const a = availability(p.status);
  return (
    <div className="panel detail" role="region" aria-label={`${p.name} details`}>
      <div className="detailhead">
        <strong>{p.name}</strong>
        <span className="muted">
          <ClubTag code={p.team} /> · {p.position}
        </span>
        <button type="button" className="x" aria-label="Close details" onClick={onClose}>
          ×
        </button>
      </div>
      <dl className="facts">
        <div>
          <dt>Price</dt>
          <dd>{p.price}</dd>
        </div>
        {p.sell_price ? (
          <div>
            <dt>Sells for</dt>
            <dd>{p.sell_price}</dd>
          </div>
        ) : null}
        <div>
          <dt title="expected points per gameweek">Expected</dt>
          <dd>{one(p.xp)}</dd>
        </div>
        <div>
          <dt>Form</dt>
          <dd>{p.form}</dd>
        </div>
        <div>
          <dt>Points</dt>
          <dd>{p.total_points}</dd>
        </div>
        <div>
          <dt>Next</dt>
          <dd>{p.fixture ?? "—"}</dd>
        </div>
      </dl>
      <p className={a === "ok" ? "muted" : `status-${a}`}>
        {p.status}
        {p.news ? `: ${p.news}` : ""}
      </p>
      {actions ? <div className="detailactions">{actions}</div> : null}
      {points ? <Breakdown pl={points} gameweek={gameweek} /> : null}
    </div>
  );
}

function Breakdown({ pl, gameweek }: { pl: PointsPlayer; gameweek?: number }) {
  const mult = pl.multiplier !== 1 ? ` × ${pl.multiplier}${pl.captain ? " (captain)" : ""}` : "";
  return (
    <div className="breakdown">
      <h3>
        {gameweek ? `GW${gameweek} points` : "Points"}: {pl.points}
        {pl.counted !== pl.points ? <span className="muted"> {mult} = {pl.counted} counted</span> : null}
      </h3>
      {pl.subbed_on ? <p className="muted">Automatically substituted on.</p> : null}
      {pl.subbed_off ? <p className="muted">Did not play, so automatically substituted off.</p> : null}
      {pl.matches.length === 0 ? <p className="muted">No match this gameweek.</p> : null}
      {pl.matches.map((m, i) => (
        <section className="matchbox" key={i} aria-label={`${m.home} against ${m.away}`}>
          <div className="matchhead">
            <span className="teams">
              {m.home} <strong className="score">{scoreText(m.home_score, m.away_score)}</strong> {m.away}
            </span>
            <span className={`pill ${m.status === "Not started" ? "waiting" : m.status === "FT" ? "" : "live"}`}>
              {m.status === "Not started" ? kickoffText(m.kickoff) || m.status : m.status}
            </span>
          </div>
          {m.stats.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Statistic</th>
                  <th className="num">Value</th>
                  <th className="num">Points</th>
                </tr>
              </thead>
              <tbody>
                {m.stats.map((r) => (
                  <tr key={r.label}>
                    <td>{r.label}</td>
                    <td className="num">{r.value}</td>
                    <td className="num">{r.points} pts</td>
                  </tr>
                ))}
                <tr className="total">
                  <td colSpan={2}>Total</td>
                  <td className="num">{m.points} pts</td>
                </tr>
              </tbody>
            </table>
          ) : (
            <p className="muted small">{m.status === "Not started" ? "Not played yet." : "Nothing scored."}</p>
          )}
        </section>
      ))}
    </div>
  );
}
