import { one } from "../lib/format.ts";
import type { Subs } from "../lib/types.gen.ts";

/** The verdict on substitutions: FPL's rules first, then the points and the reasons. */
export function SubsResult({ v }: { v: Subs }) {
  if (!v.valid) {
    return (
      <div className="panel attention" role="alert">
        <h3>FPL would not allow this</h3>
        <ul className="notes">
          {v.problems.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      </div>
    );
  }
  const better = v.expected_points_change > 0.05;
  const worse = v.expected_points_change < -0.05;
  return (
    <div className="panel">
      <h3>GW{v.gameweek} with these substitutions</h3>
      <div className="subsummary">
        <div>
          <div className="label">Expected points</div>
          <div className="big">
            {one(v.expected_points_before)} → {one(v.expected_points_after)}{" "}
            <span className={better ? "status-good" : worse ? "status-out" : "muted"}>
              ({v.expected_points_change > 0 ? "+" : ""}
              {one(v.expected_points_change)})
            </span>
          </div>
        </div>
        <div>
          <div className="label">Formation</div>
          <div className="big">{v.formation}</div>
        </div>
        <div>
          <div className="label">Captain</div>
          <div className="big">{v.captain}</div>
        </div>
        <div>
          <div className="label">Against the best XI</div>
          <div className="big">{v.expected_points_vs_best > -0.05 ? "best" : one(v.expected_points_vs_best)}</div>
        </div>
      </div>
      <ul className="insights">
        {v.insights.map((i) => (
          <li key={i}>{i}</li>
        ))}
      </ul>
      <p className="muted small">Expected points are {v.xp_basis}. Nothing is changed on FPL: make the swaps on the Pick Team page.</p>
    </div>
  );
}
