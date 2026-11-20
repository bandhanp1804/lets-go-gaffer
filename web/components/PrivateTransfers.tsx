"use client";

import Link from "next/link";
import { useTeam } from "./TeamProvider.tsx";

/** How a page's squad is labelled, from the API's source field. */
export function sourceLabel(source: string, asOf: string): string {
  switch (source) {
    case "live":
      return "Live from FPL";
    case "entered":
      return "Public data + your transfers";
  }
  return `Public data, as of ${asOf}`;
}

/**
 * Why a squad may be missing recent transfers, and the two ways to include them. FPL keeps a
 * manager's transfers for the upcoming gameweek private until its deadline: the public squad is
 * the one from the last deadline. Shown wherever the squad is used, unless it is live.
 */
export function PrivateTransfersNote({ source, gameweek, onWhatIf = false }: { source: string; gameweek: number; onWhatIf?: boolean }) {
  const { openModal } = useTeam();
  if (source === "live") return null;
  const enter = onWhatIf ? (
    <>plan them below and press <strong>I’ve made these on FPL</strong></>
  ) : (
    <>
      enter them on <Link href="/whatif">What if</Link>
    </>
  );
  if (source === "entered") {
    return (
      <div className="privatenote" role="note">
        <strong>Includes the transfers you entered.</strong> FPL keeps them private until the GW{gameweek} deadline, so they are your word, not FPL’s: the app checks them
        against FPL’s rules, not against your account. To check them against your account instead, add your login token (
        <button type="button" className="link" onClick={openModal}>
          Load my team → Live squad
        </button>
        ).
      </div>
    );
  }
  return (
    <div className="privatenote" role="note">
      <strong>Transfers you’ve made for GW{gameweek} aren’t shown yet.</strong> FPL keeps them private until the GW{gameweek} deadline, so this is your squad as of the last
      deadline. To include them, add your login token (
      <button type="button" className="link" onClick={openModal}>
        Load my team → Live squad
      </button>
      ), or {enter}.
    </div>
  );
}

/** The transfers entered for this gameweek, each removable. Shown even when the squad fails to
 * load, because a wrong entry is the usual reason it fails. */
export function EnteredTransfers() {
  const { made, removeMade, clearMade, live } = useTeam();
  if (made.length === 0) return null;
  return (
    <div className="panel">
      <h3>Transfers you’ve made</h3>
      {live ? <p className="muted small">Not used now: your live squad from FPL already includes every transfer.</p> : null}
      <ul className="plan">
        {made.map((m) => (
          <li key={m.out}>
            <span className="out">{m.outName}</span>
            <span className="arrow"> → </span>
            <span className="in">{m.inName}</span>
            <button type="button" className="link" onClick={() => removeMade(m.out)}>
              remove
            </button>
          </li>
        ))}
      </ul>
      <p className="muted small">Every page uses your squad with these until the deadline, when FPL shows them itself and they are dropped.</p>
      <button type="button" onClick={clearMade}>
        Clear all
      </button>
    </div>
  );
}
