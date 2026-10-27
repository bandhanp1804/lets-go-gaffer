"use client";

import { useState } from "react";
import { cleanToken, expiresIn } from "../../lib/identity.ts";
import { captureSnippet } from "../../lib/snippet.ts";
import { useTeam } from "../../components/TeamProvider.tsx";

/** Your saved team and FPL login token: what loads by default on any device you sign in on. */
export default function Setup() {
  const { settings, identity, ready, saveSettings, openModal } = useTeam();
  const [teamText, setTeamText] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [tokenText, setTokenText] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);
  const [copied, setCopied] = useState(false);

  if (!ready) return <p className="muted">Loading…</p>;

  if (settings === null) {
    return (
      <>
        <h1>Setup</h1>
        <p className="panel attention">
          This server does not keep settings (it has no <code>GAFFER_DATA_KEY</code>), so your team is remembered in this browser instead.{" "}
          {identity.teamId !== null ? <>It is team {identity.teamId}. </> : null}
          <button type="button" className="link" onClick={openModal}>
            Choose a team
          </button>
        </p>
      </>
    );
  }

  async function save(req: Parameters<typeof saveSettings>[0], done: string) {
    setBusy(true);
    setMessage(null);
    try {
      await saveSettings(req);
      setMessage({ ok: true, text: done });
      return true;
    } catch (e) {
      setMessage({ ok: false, text: e instanceof Error ? e.message : "Could not save." });
      return false;
    } finally {
      setBusy(false);
    }
  }

  const teamId = Number.parseInt(teamText.replace(/\D+/g, " ").trim().split(" ").pop() ?? "", 10);
  const token = tokenText.trim() !== "" ? cleanToken(tokenText) : null;
  const left = token?.ok ? expiresIn(token.token, Date.now()) : null;
  const tokenProblem = token && !token.ok ? token.reason : left === "expired" ? "That token has already expired: copy a fresh one." : "";
  const t = settings.token;

  return (
    <>
      <h1>Setup</h1>
      <p className="muted">
        Signed in as <strong>{settings.user === "local" ? "you, on this computer" : settings.user}</strong>. What you save here loads by default wherever you sign in. Your team and token are encrypted on the server with a key the
        database never holds, and the token is never sent back to a browser.
      </p>

      <section className="panel">
        <h2>Default team</h2>
        <p>{settings.team_id ? <>Team <strong>{settings.team_id}</strong> loads by default.</> : "No team saved yet."}</p>
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            if (Number.isFinite(teamId) && teamId > 0) void save({ team_id: teamId }, `Team ${teamId} saved.`).then((ok) => ok && setTeamText(""));
          }}
        >
          <label className="grow">
            Team id, or your Points page address
            <input value={teamText} placeholder="e.g. 1234567" onChange={(e) => setTeamText(e.target.value)} />
          </label>
          <button type="submit" className="primary" disabled={busy || !(Number.isFinite(teamId) && teamId > 0)}>
            Save team
          </button>
          {settings.team_id ? (
            <button type="button" disabled={busy} onClick={() => void save({ clear_team: true, clear_token: true }, "Team and token removed.")}>
              Remove
            </button>
          ) : null}
        </form>
      </section>

      <section className="panel">
        <h2>Live squad token</h2>
        <p>
          {!t.saved ? (
            "No token saved: you see your squad as of the last deadline."
          ) : t.expired ? (
            <span className="status-out">Your saved token has expired. Refresh it to see transfers made since the deadline.</span>
          ) : (
            <>
              Saved, valid for another <strong>{t.expires_in}</strong>. FPL tokens last about an hour.
            </>
          )}
        </p>
        <p className="actions">
          <button type="button" className="primary" disabled={busy || !settings.team_id} onClick={() => setRefreshing(!refreshing)}>
            {t.saved ? "Refresh token" : "Add a token"}
          </button>
          {t.saved ? (
            <button type="button" disabled={busy} onClick={() => void save({ clear_token: true }, "Token removed.")}>
              Remove token
            </button>
          ) : null}
          {!settings.team_id ? <span className="muted small"> Save a team first.</span> : null}
        </p>

        {refreshing ? (
          <form
            className="livebox"
            onSubmit={(e) => {
              e.preventDefault();
              if (token?.ok && left !== "expired")
                void save({ token: token.token }, "Token saved.").then((ok) => {
                  if (ok) {
                    setTokenText("");
                    setRefreshing(false);
                  }
                });
            }}
          >
            <ol className="steps">
              <li>
                Log in at{" "}
                <a href="https://fantasy.premierleague.com/my-team" target="_blank" rel="noopener noreferrer">
                  fantasy.premierleague.com
                </a>{" "}
                and open <strong>Pick Team</strong>.
              </li>
              <li>
                Open the console (<kbd>F12</kbd>, then <strong>Console</strong>), paste this script and press Enter.{" "}
                <button
                  type="button"
                  className="chip button"
                  onClick={() =>
                    void navigator.clipboard.writeText(captureSnippet).then(() => {
                      setCopied(true);
                      setTimeout(() => setCopied(false), 2500);
                    })
                  }
                >
                  {copied ? "Copied ✓" : "Copy script"}
                </button>
              </li>
              <li>Copy the token it prints and paste it here.</li>
            </ol>
            <label>
              Token
              <input type="password" autoComplete="off" spellCheck={false} value={tokenText} placeholder="Bearer eyJ…" onChange={(e) => setTokenText(e.target.value)} />
            </label>
            {tokenProblem ? <p className="fielderror">{tokenProblem}</p> : null}
            {token?.ok && left && left !== "expired" ? <p className="fieldok">Looks right. Valid for another {left}.</p> : null}
            <button type="submit" className="primary" disabled={busy || !token?.ok || left === "expired"}>
              Save token
            </button>
          </form>
        ) : null}
      </section>

      {message ? <p className={message.ok ? "fieldok" : "fielderror"}>{message.text}</p> : null}
    </>
  );
}
