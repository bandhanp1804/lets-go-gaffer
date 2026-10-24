import { availability } from "../lib/pitch.ts";
import { one } from "../lib/format.ts";
import type { Player } from "../lib/types.gen.ts";

export function PlayerCard({ p, role }: { p: Player; role?: "C" | "V" | "" }) {
  const a = availability(p.status);
  const title = [p.status !== "available" ? p.status : "", p.news ?? ""].filter(Boolean).join(": ");
  return (
    <div className={`card pos-${p.position}`} title={title || undefined}>
      <div className="shirt">
        {p.team}
        {role ? <span className={`badge role-${role}`}>{role}</span> : null}
        {a !== "ok" ? <span className={`flag flag-${a}`} aria-label={p.status} /> : null}
      </div>
      <div className="pname">{p.name}</div>
      <div className="pmeta">
        <span title="expected points per gameweek">{one(p.xp)} xp</span>
        <span>{p.price}</span>
      </div>
      {p.fixture ? <div className="pfix">{p.fixture}</div> : null}
    </div>
  );
}
