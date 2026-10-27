"use client";

import { Suspense, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { Async } from "../../components/Async.tsx";
import { Candidates } from "../../components/Candidates.tsx";
import { Notes } from "../../components/Notes.tsx";
import { Pitch } from "../../components/Pitch.tsx";
import type { Mark } from "../../components/PlayerCard.tsx";
import { SubsResult } from "../../components/SubsResult.tsx";
import { WhatIfResult } from "../../components/WhatIfResult.tsx";
import { clickForSubs, noSubs, subsMarks, subsRequest, type SubsDraft } from "../../lib/subs.ts";
import type { MyTeam, Player, Subs } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";
import { usePlan, useWhatIf } from "../../lib/useWhatIf.ts";
import { maxReplacementPrice, movesFromParam, pendingSlot, requestFor, setIn, toggleOut, type DraftMove } from "../../lib/whatif.ts";

function intParam(v: string | null, min: number, max: number): number | null {
  if (v === null || !/^\d{1,2}$/.test(v)) return null;
  const n = Number(v);
  return n >= min && n <= max ? n : null;
}

/** Substitutions: a starter swapped with a bench player, weighed for the upcoming gameweek. */
function SubsBuilder({ t }: { t: MyTeam }) {
  const [draft, setDraft] = useState<SubsDraft>(noSubs);
  const result = usePlan<Subs>("/api/whatif/subs", subsRequest(draft));
  const starters = useMemo(() => new Set(t.starting.map((p) => p.id)), [t.starting]);
  const name = (id: number) => [...t.starting, ...t.bench].find((p) => p.id === id)?.name ?? `player ${id}`;

  return (
    <>
      <Pitch starting={t.starting} bench={t.bench} captain={t.captain} vice={t.vice_captain} onSelect={(p) => setDraft(clickForSubs(draft, p.id, starters.has(p.id)))} marks={subsMarks(draft)} />
      {draft.swaps.length > 0 || draft.pending !== null ? (
        <div className="panel">
          <h3>Your substitutions</h3>
          <ul className="plan">
            {draft.swaps.map((s) => (
              <li key={s.out}>
                <span className="out">{name(s.out)}</span>
                <span className="arrow"> off, </span>
                <span className="in">{name(s.in)}</span>
                <span className="arrow"> on </span>
                <button type="button" className="link" onClick={() => setDraft(clickForSubs(draft, s.out, true))}>
                  undo
                </button>
              </li>
            ))}
            {draft.pending !== null ? (
              <li>
                <span className="out">{name(draft.pending)}</span> <span className="muted">off: now click a bench player to bring on</span>
              </li>
            ) : null}
          </ul>
          <button type="button" onClick={() => setDraft(noSubs)}>
            Clear
          </button>
        </div>
      ) : null}
      {result.error ? (
        <div className="error" role="alert">
          {result.error.message}
        </div>
      ) : null}
      {result.data ? (
        <div className={result.loading ? "dim" : undefined}>
          <SubsResult v={result.data} />
        </div>
      ) : result.loading ? (
        <p className="muted">Weighing the substitutions…</p>
      ) : draft.swaps.length === 0 && draft.pending === null ? (
        <p className="muted">Click a starter to take off, then a bench player to bring on.</p>
      ) : null}
    </>
  );
}

function Builder() {
  const params = useSearchParams();
  const team = useApi<MyTeam>("/api/team");
  const [mode, setMode] = useState<"transfers" | "subs">(() => (params.get("mode") === "subs" ? "subs" : "transfers"));
  const [moves, setMoves] = useState<DraftMove[]>(() => movesFromParam(params.get("moves")));
  const [horizon, setHorizon] = useState(() => intParam(params.get("horizon"), 1, 10) ?? 4);
  const [free, setFree] = useState(() => (intParam(params.get("free"), 0, 5) ?? "").toString());
  const [editing, setEditing] = useState<number | null>(null); // a sale whose replacement is being chosen
  const [bought, setBought] = useState<Map<number, Player>>(new Map()); // players chosen from the list, by id

  const result = useWhatIf(requestFor(moves, horizon, free === "" ? null : Number(free)));

  const squad = useMemo(() => (team.data ? [...team.data.starting, ...team.data.bench] : []), [team.data]);
  const byId = useMemo(() => {
    const m = new Map<number, Player>(squad.map((p) => [p.id, p]));
    for (const [id, p] of bought) m.set(id, p);
    for (const mv of result.data?.moves ?? []) m.set(mv.in.id, mv.in);
    return m;
  }, [squad, bought, result.data]);
  const marks = useMemo(() => new Map<number, Mark>(moves.map((m) => [m.out, "out"])), [moves]);
  const slot = editing ?? pendingSlot(moves);
  const slotPlayer = slot === null ? undefined : byId.get(slot);

  function clickSquadPlayer(p: Player) {
    const next = toggleOut(moves, p.id);
    setMoves(next);
    setEditing(next.some((m) => m.out === p.id) ? p.id : null); // opening a sale opens the list of replacements
  }

  return (
    <>
      <h1>What if?</h1>
      <div className="modeswitch" role="group" aria-label="What to try">
        <button type="button" aria-pressed={mode === "transfers"} onClick={() => setMode("transfers")}>
          Transfers
        </button>
        <button type="button" aria-pressed={mode === "subs"} onClick={() => setMode("subs")}>
          Subs
        </button>
      </div>
      <p className="muted">
        {mode === "transfers"
          ? "Try your own transfers before you make them. Click a player of yours to sell him, then pick who comes in."
          : "Try your own substitutions: click a starter to take off, then a bench player to bring on."}{" "}
        The rules and the points are checked here, and nothing is sent to FPL.
      </p>

      <Async state={team}>
        {(t) => (
          <>
            <div className="chips">
              <span className="chip">Bank {t.bank}</span>
              <span className="chip">
                {t.free_transfers_left} free transfer{t.free_transfers_left === 1 ? "" : "s"}
              </span>
              <span className={`chip ${t.source === "live" ? "good" : "warn"}`}>{t.source === "live" ? "Live from FPL" : `Public data, as of ${t.squad_as_of}`}</span>
            </div>
            <Notes notes={t.notes} />

            {mode === "subs" ? (
              <SubsBuilder t={t} />
            ) : (
              <>
              <Pitch starting={t.starting} bench={t.bench} captain={t.captain} vice={t.vice_captain} onSelect={clickSquadPlayer} marks={marks} />

              {moves.length > 0 ? (
                <div className="panel">
                  <h3>Your plan</h3>
                  <ul className="plan">
                    {moves.map((m) => {
                      const out = byId.get(m.out);
                      const inn = m.in === null ? undefined : byId.get(m.in);
                      return (
                        <li key={m.out}>
                          <span className="out">{out?.name ?? `player ${m.out}`}</span>
                          <span className="arrow"> → </span>
                          {m.in === null ? (
                            <span className="muted">choose a replacement</span>
                          ) : (
                            <span className="in">{inn?.name ?? `player ${m.in}`}</span>
                          )}
                          <button type="button" className="link" onClick={() => setEditing(m.out)}>
                            {m.in === null ? "choose" : "change"}
                          </button>
                          <button type="button" className="link" onClick={() => { setMoves(toggleOut(moves, m.out)); setEditing(null); }}>
                            remove
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                  <div className="form compact">
                    <label>
                      Count the gain over
                      <select value={horizon} onChange={(e) => setHorizon(Number(e.target.value))}>
                        {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => (
                          <option key={n} value={n}>
                            {n} gameweek{n === 1 ? "" : "s"}
                          </option>
                        ))}
                      </select>
                    </label>
                    <label>
                      Free transfers (what if)
                      <input type="number" min={0} max={5} value={free} placeholder={String(t.free_transfers_left)} onChange={(e) => setFree(e.target.value)} />
                    </label>
                    <button type="button" onClick={() => { setMoves([]); setEditing(null); }}>
                      Clear plan
                    </button>
                  </div>
                </div>
              ) : null}

              {slotPlayer ? (
                <Candidates
                  key={slotPlayer.id}
                  out={slotPlayer}
                  maxPrice={maxReplacementPrice(slotPlayer.sell_price, t.bank)}
                  ownedIds={new Set([...squad.map((p) => p.id), ...moves.flatMap((m) => (m.in !== null && m.out !== slot ? [m.in] : []))])}
                  onPick={(p) => {
                    setBought(new Map(bought).set(p.id, p));
                    setMoves(setIn(moves, slotPlayer.id, p.id));
                    setEditing(null);
                  }}
                  onCancel={() => {
                    if (moves.find((m) => m.out === slotPlayer.id)?.in === null) setMoves(toggleOut(moves, slotPlayer.id));
                    setEditing(null);
                  }}
                />
              ) : null}

              {result.error ? (
                <div className="error" role="alert">
                  {result.error.message}
                </div>
              ) : null}
              {result.data ? (
                <div className={result.loading ? "dim" : undefined}>
                  <WhatIfResult v={result.data} />
                </div>
              ) : result.loading ? (
                <p className="muted">Weighing the plan…</p>
              ) : moves.length === 0 ? (
                <p className="muted">Click a player above to start a plan.</p>
              ) : null}
              </>
            )}
          </>
        )}
      </Async>
    </>
  );
}

export default function WhatIfPage() {
  // useSearchParams needs a Suspense boundary so the page can be prepared ahead of time.
  return (
    <Suspense fallback={<p className="muted">Loading…</p>}>
      <Builder />
    </Suspense>
  );
}
