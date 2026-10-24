import { pitchLines, roleOf } from "../lib/pitch.ts";
import type { Player } from "../lib/types.gen.ts";
import { PlayerCard, type Mark } from "./PlayerCard.tsx";

interface Props {
  starting: Player[];
  bench: Player[];
  captain: string;
  vice: string;
  /** When set, cards are buttons and this is called with the player clicked. */
  onSelect?: (p: Player) => void;
  selectedId?: number | null;
  marks?: Map<number, Mark>;
}

/** A starting XI on a pitch, goalkeeper at the top, with the bench underneath. */
export function Pitch({ starting, bench, captain, vice, onSelect, selectedId, marks }: Props) {
  const card = (p: Player) => (
    <PlayerCard
      key={p.id}
      p={p}
      role={roleOf(p.name, captain, vice)}
      onClick={onSelect ? () => onSelect(p) : undefined}
      selected={selectedId === p.id}
      mark={marks?.get(p.id)}
    />
  );
  return (
    <div className="pitch-wrap">
      <div className="pitch">
        {pitchLines(starting).map((l) => (
          <div className="line" key={l.line}>
            {l.players.map(card)}
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
                {card(p)}
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}
