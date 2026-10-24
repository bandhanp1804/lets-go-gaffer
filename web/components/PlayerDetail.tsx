import { availability } from "../lib/pitch.ts";
import { one } from "../lib/format.ts";
import type { Player } from "../lib/types.gen.ts";

/** What is known about one player, shown when a card is clicked. */
export function PlayerDetail({ p, onClose }: { p: Player; onClose: () => void }) {
  const a = availability(p.status);
  return (
    <div className="panel detail" role="region" aria-label={`${p.name} details`}>
      <div className="detailhead">
        <strong>{p.name}</strong>
        <span className="muted">
          {p.team} · {p.position}
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
    </div>
  );
}
