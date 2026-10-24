import type { Points } from "../lib/types.gen.ts";

const signedInt = (n: number): string => (n > 0 ? `+${n}` : n < 0 ? `−${Math.abs(n)}` : "0");

/** How the team is scoring in the gameweek being played, or the last one finished. */
export function PointsPanel({ p }: { p: Points }) {
  const state = p.in_play ? (p.started ? "live" : "waiting") : "final";
  return (
    <section className={`panel pointspanel ${state}`} aria-label={`Gameweek ${p.gameweek} points`}>
      <div className="pointshead">
        <span className="bignum" aria-label={`${p.net_points} points`}>
          {p.net_points}
        </span>
        <div>
          <strong>GW{p.gameweek} points</strong>
          <span className={`pill ${state}`}>{state === "live" ? "● live, provisional" : state === "waiting" ? "waiting for kick-off" : "final"}</span>
          <div className="muted">{p.note}</div>
        </div>
      </div>
      <dl className="facts">
        <div>
          <dt>Players scored</dt>
          <dd>{p.gross_points}</dd>
        </div>
        <div>
          <dt>Transfer hit</dt>
          <dd>{p.transfer_hit > 0 ? `−${p.transfer_hit}` : "none"}</dd>
        </div>
        <div>
          <dt>Left on the bench</dt>
          <dd>{p.points_on_bench}</dd>
        </div>
        {p.chip ? (
          <div>
            <dt>Chip</dt>
            <dd>{p.chip}</dd>
          </div>
        ) : null}
        <div>
          <dt>Season total</dt>
          <dd>{p.season_points}</dd>
        </div>
        <div>
          <dt>Overall rank</dt>
          <dd>{p.overall_rank.toLocaleString("en-GB")}</dd>
        </div>
      </dl>
      <p className="muted small">This is the team you picked for GW{p.gameweek}. Transfers made since count from the next gameweek.</p>
      <details>
        <summary>How the points add up</summary>
        <div className="tablewrap">
          <table>
            <thead>
              <tr>
                <th>Player</th>
                <th>Club</th>
                <th className="num">Mins</th>
                <th className="num">Points</th>
                <th className="num">×</th>
                <th className="num">Counts</th>
              </tr>
            </thead>
            <tbody>
              {p.players.map((pl) => (
                <tr key={pl.id} className={pl.bench ? "benchrow" : undefined}>
                  <td>
                    {pl.name}
                    {pl.captain ? " (C)" : pl.vice_captain ? " (V)" : ""}
                    {pl.subbed_on ? " ↑ subbed on" : ""}
                    {pl.subbed_off ? " ↓ subbed off" : ""}
                    {pl.bench ? " (bench)" : ""}
                  </td>
                  <td>{pl.team}</td>
                  <td className="num">{pl.minutes}</td>
                  <td className="num">{pl.points}</td>
                  <td className="num">{pl.multiplier}</td>
                  <td className="num">
                    <strong>{pl.counted}</strong>
                  </td>
                </tr>
              ))}
              <tr className="total">
                <td colSpan={5}>
                  Players scored{p.transfer_hit > 0 ? `, less the ${p.transfer_hit}-point hit` : ""}
                </td>
                <td className="num">{signedInt(p.net_points)}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </section>
  );
}
