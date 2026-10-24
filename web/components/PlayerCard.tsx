import { availability } from "../lib/pitch.ts";
import { one } from "../lib/format.ts";
import type { Player } from "../lib/types.gen.ts";

export type Mark = "out" | "in";

interface Props {
  p: Player;
  role?: "C" | "V" | "";
  /** Makes the card a button. */
  onClick?: () => void;
  selected?: boolean;
  /** "out" for a player being sold in a what-if, "in" for one being bought. */
  mark?: Mark;
}

export function PlayerCard({ p, role, onClick, selected, mark }: Props) {
  const a = availability(p.status);
  const title = [p.status !== "available" ? p.status : "", p.news ?? ""].filter(Boolean).join(": ");
  const cls = `card pos-${p.position}${selected ? " selected" : ""}${mark ? ` mark-${mark}` : ""}${onClick ? " clickable" : ""}`;
  const body = (
    <>
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
      {mark === "out" ? <div className="markline">selling</div> : null}
    </>
  );
  if (onClick) {
    return (
      <button type="button" className={cls} title={title || undefined} onClick={onClick} aria-pressed={selected ?? false} aria-label={`${p.name}, ${p.team}, ${p.position}`}>
        {body}
      </button>
    );
  }
  return (
    <div className={cls} title={title || undefined}>
      {body}
    </div>
  );
}
