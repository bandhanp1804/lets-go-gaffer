"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getJSON, postJSON } from "../lib/api.ts";
import { headersFor, loadIdentity, noIdentity, saveIdentity, type Identity } from "../lib/identity.ts";
import { addMade, currentMade, loadMade, madeHeader, saveMade, type MadeRecord, type MadeTransfer } from "../lib/made.ts";
import type { Status, UserSettings, UserSettingsRequest } from "../lib/types.gen.ts";

interface TeamState {
  identity: Identity;
  /** False until the saved team has been read; requests wait for it. */
  ready: boolean;
  /** Headers that say whose squad a request is about (none when the server keeps it). */
  headers: Record<string, string>;
  /** A live squad is in use: a token in this browser, or a saved, unexpired one on the server. */
  live: boolean;
  /**
   * The signed-in user's settings, kept (encrypted) on the server. Null when the server keeps
   * none (no key, or an older API): the team is then remembered in this browser, as before.
   */
  settings: UserSettings | null;
  /** Saves a team; with keepToken a saved token stays, without it a token-less save clears it. */
  setIdentity: (id: Identity, keepToken?: boolean) => Promise<void>;
  saveSettings: (req: UserSettingsRequest) => Promise<void>;
  clear: () => Promise<void>;
  modalOpen: boolean;
  openModal: () => void;
  closeModal: () => void;
  /**
   * Transfers the manager says they made since the last deadline (FPL keeps them private until
   * the next one). Sent with every request while the squad is not live, so every page uses them.
   */
  made: MadeTransfer[];
  recordMade: (gameweek: number, moves: MadeTransfer[]) => void;
  removeMade: (out: number) => void;
  clearMade: () => void;
}

const Ctx = createContext<TeamState | null>(null);

export function TeamProvider({ children }: { children: ReactNode }) {
  const [identity, setId] = useState<Identity>(noIdentity);
  const [settings, setSettings] = useState<UserSettings | null>(null);
  const [ready, setReady] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [madeRec, setMadeRec] = useState<MadeRecord | null>(null);
  const [gameweek, setGameweek] = useState<number | null>(null);

  // The entered transfers, and the gameweek being picked for: once its deadline passes, FPL's
  // public squad shows them itself, so the ones entered for it are dropped.
  useEffect(() => {
    setMadeRec(loadMade());
    const ctl = new AbortController();
    getJSON<Status>("/api/status", ctl.signal)
      .then((s) => setGameweek(s.gameweek))
      .catch(() => {});
    return () => ctl.abort();
  }, []);
  const updateMade = useCallback((rec: MadeRecord | null) => {
    setMadeRec(rec);
    saveMade(rec);
  }, []);

  const apply = useCallback((s: UserSettings) => {
    setSettings(s);
    setId({ teamId: s.team_id || null, token: "" });
  }, []);

  // After mounting (so the server-rendered page and the first client render agree): use the
  // server's settings when it keeps them, moving a team and token remembered only in this
  // browser across once; otherwise use the browser's.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const local = loadIdentity();
      try {
        let s = await getJSON<UserSettings>("/api/settings");
        if (s.team_id === 0 && local.teamId !== null) {
          const req: UserSettingsRequest = { team_id: local.teamId };
          if (local.token !== "") req.token = local.token;
          s = await postJSON<UserSettings>("/api/settings", req);
          saveIdentity(noIdentity); // the server has them now
        }
        if (!cancelled) apply(s);
      } catch {
        if (!cancelled) setId(local);
      } finally {
        if (!cancelled) setReady(true);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [apply]);

  const saveSettings = useCallback(
    async (req: UserSettingsRequest) => {
      apply(await postJSON<UserSettings>("/api/settings", req));
    },
    [apply],
  );

  const setIdentity = useCallback(
    async (id: Identity, keepToken = false) => {
      if (settings === null) {
        setId(id);
        saveIdentity(id);
        return;
      }
      const req: UserSettingsRequest = id.teamId === null ? { clear_team: true } : { team_id: id.teamId };
      if (id.token !== "") req.token = id.token;
      else if (!keepToken) req.clear_token = true;
      await saveSettings(req);
    },
    [settings, saveSettings],
  );
  const clear = useCallback(() => setIdentity(noIdentity), [setIdentity]);
  const openModal = useCallback(() => setModalOpen(true), []);
  const closeModal = useCallback(() => setModalOpen(false), []);

  const live = settings !== null ? settings.token.saved && !settings.token.expired : identity.token !== "";
  const made = useMemo(() => currentMade(madeRec, identity.teamId, gameweek), [madeRec, identity.teamId, gameweek]);
  const recordMade = useCallback(
    (gw: number, moves: MadeTransfer[]) => {
      if (identity.teamId === null) return;
      updateMade({ teamId: identity.teamId, gameweek: gw, moves: addMade(currentMade(madeRec, identity.teamId, gw), moves) });
    },
    [identity.teamId, madeRec, updateMade],
  );
  const removeMade = useCallback(
    (out: number) => {
      if (madeRec !== null) updateMade({ ...madeRec, moves: madeRec.moves.filter((m) => m.out !== out) });
    },
    [madeRec, updateMade],
  );
  const clearMade = useCallback(() => updateMade(null), [updateMade]);
  const headers = useMemo(() => {
    // With server settings the API looks the team and token up itself; only the entered
    // transfers are sent. A live squad already has them, so they are not sent with one.
    const h: Record<string, string> = settings !== null ? {} : headersFor(identity);
    if (made.length > 0 && !live) h["X-Gaffer-Made"] = madeHeader(made);
    return h;
  }, [settings, identity, made, live]);
  const value = useMemo<TeamState>(
    () => ({
      identity,
      ready,
      headers,
      live,
      settings,
      setIdentity,
      saveSettings,
      clear,
      modalOpen,
      openModal,
      closeModal,
      made,
      recordMade,
      removeMade,
      clearMade,
    }),
    [identity, ready, headers, settings, live, setIdentity, saveSettings, clear, modalOpen, openModal, closeModal, made, recordMade, removeMade, clearMade],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTeam(): TeamState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useTeam must be used inside TeamProvider");
  return v;
}
