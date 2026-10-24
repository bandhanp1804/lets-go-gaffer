"use client";

import { useEffect, useRef, useState } from "react";
import { cleanToken, expiresIn, parseTeamId } from "../lib/identity.ts";
import { captureSnippet } from "../lib/snippet.ts";
import { useTeam } from "./TeamProvider.tsx";

/** The "which team?" dialog: a team id, and optionally a token for the live squad. */
export function TeamModal() {
  const { identity, modalOpen, closeModal, setIdentity, clear } = useTeam();
  const dialog = useRef<HTMLDialogElement>(null);
  const [teamText, setTeamText] = useState("");
  const [live, setLive] = useState(false);
  const [tokenText, setTokenText] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [copied, setCopied] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  // Show or hide the native dialog, which brings focus trapping and Esc for free.
  useEffect(() => {
    const d = dialog.current;
    if (!d) return;
    if (modalOpen && !d.open) {
      setTeamText(identity.teamId === null ? "" : String(identity.teamId));
      setLive(identity.token !== "");
      setTokenText(identity.token);
      setShowToken(false);
      setNow(Date.now());
      d.showModal();
    } else if (!modalOpen && d.open) {
      d.close();
    }
  }, [modalOpen, identity]);

  const teamId = parseTeamId(teamText);
  const teamError = teamText.trim() !== "" && teamId === null ? "That is not a team id. Use the number from your Points page address, or paste the whole address." : "";
  const token = live && tokenText.trim() !== "" ? cleanToken(tokenText) : null;
  const left = token?.ok ? expiresIn(token.token, now) : null;
  const tokenError = token && !token.ok ? token.reason : left === "expired" ? "That token has expired. Tokens last about an hour; get a fresh one." : "";
  const canSubmit = teamId !== null && (!live || (token?.ok === true && left !== "expired"));

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit || teamId === null) return;
    setIdentity({ teamId, token: live && token?.ok ? token.token : "" });
    closeModal();
  }

  async function copySnippet() {
    try {
      await navigator.clipboard.writeText(captureSnippet);
      setCopied(true);
      setTimeout(() => setCopied(false), 2500);
    } catch {
      // the script is also shown on the page to select by hand
    }
  }

  return (
    <dialog ref={dialog} className="modal" aria-labelledby="team-title" onCancel={closeModal} onClose={closeModal}>
      <form onSubmit={submit}>
        <header>
          <h2 id="team-title">Load your team</h2>
          <button type="button" className="x" aria-label="Close" onClick={closeModal}>
            ×
          </button>
        </header>

        <label className="field">
          Your FPL team id
          <input
            inputMode="numeric"
            autoComplete="off"
            value={teamText}
            placeholder="e.g. 1234567"
            onChange={(e) => setTeamText(e.target.value)}
            aria-invalid={teamError !== ""}
            autoFocus
          />
        </label>
        {teamError ? <p className="fielderror">{teamError}</p> : null}
        <details className="howto">
          <summary>Where do I find my team id?</summary>
          <ol>
            <li>
              Open <a href="https://fantasy.premierleague.com/" target="_blank" rel="noopener noreferrer">fantasy.premierleague.com</a> and log in.
            </li>
            <li>Click <strong>Points</strong> (or <strong>Pick Team</strong>).</li>
            <li>
              Look at the address bar: <code>fantasy.premierleague.com/entry/<mark>1234567</mark>/event/8</code>. The number after <code>entry/</code> is your team id. You can paste the whole address above.
            </li>
          </ol>
        </details>

        <label className="switch">
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
          <span>
            <strong>Live squad</strong>
            <small>Include transfers you made since the last deadline. Without it you see your squad as of the deadline.</small>
          </span>
        </label>

        {live ? (
          <section className="livebox" aria-label="Live squad token">
            <p className="privacy">
              <strong>About your token.</strong> It is a login for your FPL account, so treat it like a password. Here it stays in this browser tab (it is forgotten when you close the tab), goes only to this app running on your own computer, and is passed on to FPL and nowhere else. It is never saved or logged, and it stops working after about an hour.
            </p>

            <h3>Easiest way to get it</h3>
            <ol className="steps">
              <li>
                Log in at <a href="https://fantasy.premierleague.com/my-team" target="_blank" rel="noopener noreferrer">fantasy.premierleague.com</a> and open <strong>Pick Team</strong>.
              </li>
              <li>
                Open the browser console: <kbd>F12</kbd> then <strong>Console</strong> (Mac: <kbd>⌥</kbd><kbd>⌘</kbd><kbd>J</kbd>). Chrome may ask you to type <code>allow pasting</code> first.
              </li>
              <li>
                Paste this script and press Enter.{" "}
                <button type="button" className="chip button" onClick={copySnippet}>
                  {copied ? "Copied ✓" : "Copy script"}
                </button>
                <details className="snippet">
                  <summary>Read it first</summary>
                  <p>It reads the login token from values your FPL tab has stored and from requests the page makes itself, and prints it in the console. It draws nothing on the page (a box there could be read by browser extensions) and sends nothing anywhere.</p>
                  <pre>{captureSnippet}</pre>
                </details>
              </li>
              <li>
                The console prints your token on a line of its own (and copies it for you if it can): select that line and copy it. If the app then says FPL rejected it, <strong>do not reload the page</strong>, which removes the script: click <strong>Transfers</strong> or <strong>Pick Team</strong> in FPL&apos;s menu instead, and the token FPL really sends is printed too, last.
              </li>
              <li>Paste it below.</li>
            </ol>

            <details className="howto">
              <summary>Or find it by hand</summary>
              <ol>
                <li>On the Pick Team page open developer tools (<kbd>F12</kbd>) and the <strong>Network</strong> tab, then refresh.</li>
                <li>Click the request named <code>my-team/…</code> (filter by <code>my-team</code>).</li>
                <li>Under <strong>Request Headers</strong> copy the value of <code>x-api-authorization</code>.</li>
              </ol>
            </details>

            <label className="field">
              Your token
              <div className="inline">
                <input
                  type={showToken ? "text" : "password"}
                  autoComplete="off"
                  spellCheck={false}
                  value={tokenText}
                  placeholder="Bearer eyJ… (or just eyJ…)"
                  onChange={(e) => setTokenText(e.target.value)}
                  aria-invalid={tokenError !== ""}
                />
                <label className="inlinecheck">
                  <input type="checkbox" checked={showToken} onChange={(e) => setShowToken(e.target.checked)} /> Show
                </label>
              </div>
            </label>
            {tokenError ? <p className="fielderror">{tokenError}</p> : null}
            {token?.ok && left && left !== "expired" ? <p className="fieldok">Looks right. Valid for another {left}.</p> : null}
          </section>
        ) : null}

        <footer>
          {identity.teamId !== null ? (
            <button
              type="button"
              className="link"
              onClick={() => {
                clear();
                closeModal();
              }}
            >
              Forget this team
            </button>
          ) : (
            <span />
          )}
          <span className="actions">
            <button type="button" onClick={closeModal}>
              Cancel
            </button>
            <button type="submit" className="primary" disabled={!canSubmit}>
              Load team
            </button>
          </span>
        </footer>
      </form>
    </dialog>
  );
}
