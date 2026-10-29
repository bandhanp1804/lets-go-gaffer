import { one } from "../lib/format.ts";
import type { Lineup } from "../lib/types.gen.ts";
import { ClubTag } from "./ClubTag.tsx";

interface Props {
  l: Lineup;
  onCaptain: (id: number) => void;
  onVice: (id: number) => void;
  onReset: () => void;
}

/** Choose who wears the armband, and see what each choice is worth. The numbers come from the server. */
export function CaptainPicker({ l, onCaptain, onVice, onReset }: Props) {
  return (
    <section aria-label="Captain" className="captainpicker">
      <h2>Your captain</h2>
      <p className="lead">
        Captain <strong>{l.captain}</strong>, vice-captain <strong>{l.vice_captain}</strong>.{" "}
        {l.captain_chosen ? (
          l.captain_cost > 0 ? (
            <span className="warn">
              This choice gives up {one(l.captain_cost)} expected points against the recommended captain, {l.recommended_captain}.
            </span>
          ) : (
            <span className="good-text">That is the recommended captain.</span>
          )
        ) : (
          <span className="muted">This is the recommendation.</span>
        )}{" "}
        {l.captain_chosen ? (
          <button type="button" className="link" onClick={onReset}>
            Back to the recommendation
          </button>
        ) : null}
      </p>
      <div className="tablewrap">
        <table>
          <thead>
            <tr>
              <th>Starter</th>
              <th>Club</th>
              <th className="num" title="expected points this gameweek">xp</th>
              <th className="num">As captain (×2)</th>
              <th className="num">Gives up</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {l.captain_options.map((o) => (
              <tr key={o.id} className={o.is_captain ? "iscaptain" : undefined}>
                <td>
                  {o.name}
                  {o.is_captain ? " (C)" : o.is_vice_captain ? " (V)" : ""}
                </td>
                <td>
                  <ClubTag code={o.team} />
                </td>
                <td className="num">{one(o.xp)}</td>
                <td className="num">{one(o.as_captain)}</td>
                <td className="num">{o.cost > 0 ? `−${one(o.cost)}` : "—"}</td>
                <td className="actions">
                  <button type="button" disabled={o.is_captain} onClick={() => onCaptain(o.id)} aria-label={`Make ${o.name} captain`}>
                    Captain
                  </button>
                  <button type="button" disabled={o.is_vice_captain || o.is_captain} onClick={() => onVice(o.id)} aria-label={`Make ${o.name} vice-captain`}>
                    Vice
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted small">
        The captain's points count twice, so the choice only changes the lineup's total, never who starts. The Triple Captain card below follows your captain.
      </p>
    </section>
  );
}
