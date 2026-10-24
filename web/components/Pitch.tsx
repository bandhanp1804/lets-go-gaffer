import { pitchLines, roleOf } from "../lib/pitch.ts";
import type { Player } from "../lib/types.ts";
import { PlayerCard } from "./PlayerCard.tsx";

/** A starting XI on a pitch, goalkeeper at the top, with the bench underneath. */
export function Pitch({ starting, bench, captain, vice }: { starting: Player[]; bench: Player[]; captain: string; vice: string }) {
  return (
    <div className="pitch-wrap">
      <div className="pitch">
        {pitchLines(starting).map((l) => (
          <div className="line" key={l.line}>
            {l.players.map((p) => (
              <PlayerCard key={p.id} p={p} role={roleOf(p.name, captain, vice)} />
            ))}
          </div>
        ))}
      </div>
      {bench.length > 0 ? (
        <div className="bench">
          <h3>Bench</h3>
          <div className="line">
            {bench.map((p, i) => (
              <div key={p.id} className="benchslot">
                <span className="order">{i === 0 ? "GK" : i}</span>
                <PlayerCard p={p} role={roleOf(p.name, captain, vice)} />
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}
