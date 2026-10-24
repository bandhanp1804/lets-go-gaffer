import { one, signed } from "../lib/format.ts";
import type { WhatIf } from "../lib/types.gen.ts";
import { Notes } from "./Notes.tsx";
import { Pitch } from "./Pitch.tsx";

const verdictText: Record<string, string> = {
  worth_it: "Worth doing",
  toss_up: "A toss-up",
  not_worth_it: "Not worth it",
};

/** The verdict on a plan: what it does to the points, the money and the lineup. */
export function WhatIfResult({ v }: { v: WhatIf }) {
  if (!v.valid && v.problems.length > 0) {
    return (
      <div className="panel verdict bad" role="alert">
        <strong>This plan breaks a rule</strong>
        <ul>
          {v.problems.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      </div>
    );
  }
  if (!v.valid) {
    return (
      <div className="panel">
        {v.insights.map((i) => (
          <p key={i} className="muted">
            {i}
          </p>
        ))}
      </div>
    );
  }
  const cls = v.verdict === "worth_it" ? "good" : v.verdict === "not_worth_it" ? "bad" : "warn";
  return (
    <>
      <div className={`panel verdict ${cls}`}>
        <div className="verdicthead">
          <strong>{verdictText[v.verdict] ?? v.verdict}</strong>
          <span className="bignum">{signed(v.net_points_over_horizon)}</span>
          <span className="muted">points over {v.horizon_gameweeks} {v.horizon_gameweeks === 1 ? "gameweek" : "gameweeks"}, after the cost of the transfers</span>
        </div>
        <dl className="facts">
          <div>
            <dt>Gain a gameweek</dt>
            <dd>{signed(v.expected_gain_per_gameweek)}</dd>
          </div>
          <div>
            <dt>Points hit</dt>
            <dd>{v.points_hit > 0 ? `−${v.points_hit}` : "none"}</dd>
          </div>
          <div>
            <dt>Transfers</dt>
            <dd>
              {v.transfers} ({v.free_transfers_left} free)
            </dd>
          </div>
          <div>
            <dt>Bank</dt>
            <dd>
              {v.bank_before} → {v.bank_after}
            </dd>
          </div>
          <div>
            <dt>Free next week</dt>
            <dd>{v.free_transfers_next_week}</dd>
          </div>
        </dl>
      </div>

      <h3>What this tells you</h3>
      <ul className="insights">
        {v.insights.map((i) => (
          <li key={i}>{i}</li>
        ))}
      </ul>

      <h3>The fixtures behind it</h3>
      <div className="tablewrap">
        <table>
          <thead>
            <tr>
              <th>Out</th>
              <th>In</th>
            </tr>
          </thead>
          <tbody>
            {v.moves.map((m) => (
              <tr key={m.out.id}>
                <td>
                  <strong>{m.out.name}</strong> <span className="muted">({m.out.team}) sells for {m.out.sell_price}</span>
                  <div className="run">{m.out_run.join("  ·  ")}</div>
                </td>
                <td>
                  <strong>{m.in.name}</strong> <span className="muted">({m.in.team}) costs {m.in.price}</span>
                  <div className="run">{m.in_run.join("  ·  ")}</div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h3>Your best XI, week by week</h3>
      <div className="tablewrap">
        <table>
          <thead>
            <tr>
              <th>Gameweek</th>
              <th className="num">Before</th>
              <th className="num">After</th>
              <th className="num">Change</th>
            </tr>
          </thead>
          <tbody>
            {v.weeks.map((w) => (
              <tr key={w.gameweek}>
                <td>GW{w.gameweek}</td>
                <td className="num">{one(w.before)}</td>
                <td className="num">{one(w.after)}</td>
                <td className={`num ${w.delta > 0 ? "good-text" : w.delta < 0 ? "status-out" : ""}`}>{signed(w.delta)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted">Points are for the best XI with the captain counted twice, so a player on your bench adds nothing until he starts.</p>

      <details className="afterpitch">
        <summary>See your lineup after these moves ({v.after.formation}, {one(v.after.expected_points)} expected points, captain {v.after.captain})</summary>
        <Pitch starting={v.after.starting} bench={v.after.bench} captain={v.after.captain} vice={v.after.vice_captain} />
      </details>
      <Notes notes={v.notes} />
    </>
  );
}
