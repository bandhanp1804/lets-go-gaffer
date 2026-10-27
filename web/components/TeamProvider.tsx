"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getJSON, postJSON } from "../lib/api.ts";
import { headersFor, loadIdentity, noIdentity, saveIdentity, type Identity } from "../lib/identity.ts";
import type { UserSettings, UserSettingsRequest } from "../lib/types.gen.ts";

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
}

const Ctx = createContext<TeamState | null>(null);

export function TeamProvider({ children }: { children: ReactNode }) {
  const [identity, setId] = useState<Identity>(noIdentity);
  const [settings, setSettings] = useState<UserSettings | null>(null);
  const [ready, setReady] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);

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
  const value = useMemo<TeamState>(
    () => ({
      identity,
      ready,
      // With server settings the API looks the team and token up itself; nothing is sent.
      headers: settings !== null ? {} : headersFor(identity),
      live,
      settings,
      setIdentity,
      saveSettings,
      clear,
      modalOpen,
      openModal,
      closeModal,
    }),
    [identity, ready, settings, live, setIdentity, saveSettings, clear, modalOpen, openModal, closeModal],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTeam(): TeamState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useTeam must be used inside TeamProvider");
  return v;
}
