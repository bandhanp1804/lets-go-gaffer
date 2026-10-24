"use client";

import { Async } from "./Async.tsx";
import { query } from "../lib/api.ts";
import { one } from "../lib/format.ts";
import type { ChipAdvice, Chips } from "../lib/types.gen.ts";
import { useApi } from "../lib/useApi.ts";

const verdictText = (c: ChipAdvice): string => {
  switch (c.verdict) {
    case "good_week_now":
      return "Fine to play this week";
    case "wait":
      return `Wait for GW${c.best_gameweek}`;
    case "unavailable":
      return "Not available";
    default:
      return "No clear week";
  }
};

function Bars({ c, first }: { c: ChipAdvice; first: number }) {
  const max = Math.max(...c.weeks.map((w) => w.value), 0.1);
  return (
    <div className="chipbars" role="img" aria-label={`Value by gameweek: ${c.weeks.map((w) => `GW${w.gameweek} ${one(w.value)}`).join(", ")}`}>
      {c.weeks.map((w) => (
        <div key={w.gameweek} className={`chipbar${w.gameweek === c.best_gameweek ? " best" : ""}${w.gameweek === first ? " now" : ""}`} title={`GW${w.gameweek}: ${one(w.value)}. ${w.detail}`}>
          <span className="val">{one(w.value)}</span>
          <span className="col" style={{ height: `${Math.max(4, (Math.max(w.value, 0) / max) * 100)}%` }} />
          <span className="gw">{w.gameweek}</span>
          {w.double ? <span className="dbl">2×</span> : null}
        </div>
      ))}
    </div>
  );
}

/** When to play each chip, from the fixtures ahead. */
/** With `captain` (a player id), the Triple Captain card is about that player. */
export function ChipPlanner({ captain }: { captain?: number }) {
  const chips = useApi<Chips>(`/api/chips${query({ captain })}`);
  return (
    <section aria-label="Chip planner">
      <h2>When to play your chips</h2>
      <p className="muted small">
        Worked out from the fixtures ahead and the model's expected points for your current squad. The bars are what the chip would be worth in each gameweek; the best week is highlighted and this week is outlined.
      </p>
      <Async state={chips}>
        {(v) => (
          <div className="chipgrid">
            {v.chips.map((c) => (
              <article key={c.chip} className={`panel chipcard ${c.verdict}`}>
                <header>
                  <strong>{c.name}</strong>
                  <span className={`pill verdict-${c.verdict}`}>{verdictText(c)}</span>
                </header>
                <p>{c.headline}</p>
                {c.weeks.length > 0 ? <Bars c={c} first={v.first_gameweek} /> : null}
                <details>
                  <summary>Why</summary>
                  <ul className="insights">
                    {c.insights.map((i) => (
                      <li key={i}>{i}</li>
                    ))}
                  </ul>
                </details>
              </article>
            ))}
            {v.availability_note ? (
              <ul className="notes chipnotes">
                <li>{v.availability_note}</li>
              </ul>
            ) : null}
          </div>
        )}
      </Async>
    </section>
  );
}
