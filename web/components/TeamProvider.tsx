"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { headersFor, loadIdentity, noIdentity, saveIdentity, type Identity } from "../lib/identity.ts";

interface TeamState {
  identity: Identity;
  /** False until the saved team has been read from the browser; requests wait for it. */
  ready: boolean;
  /** Headers that say whose squad a request is about. */
  headers: Record<string, string>;
  setIdentity: (id: Identity) => void;
  clear: () => void;
  modalOpen: boolean;
  openModal: () => void;
  closeModal: () => void;
}

const Ctx = createContext<TeamState | null>(null);

export function TeamProvider({ children }: { children: ReactNode }) {
  const [identity, setId] = useState<Identity>(noIdentity);
  const [ready, setReady] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);

  // Read the saved team after mounting, so the server-rendered page and the first
  // client render agree.
  useEffect(() => {
    setId(loadIdentity());
    setReady(true);
  }, []);

  const setIdentity = useCallback((id: Identity) => {
    setId(id);
    saveIdentity(id);
  }, []);
  const clear = useCallback(() => setIdentity(noIdentity), [setIdentity]);
  const openModal = useCallback(() => setModalOpen(true), []);
  const closeModal = useCallback(() => setModalOpen(false), []);

  const value = useMemo<TeamState>(
    () => ({ identity, ready, headers: headersFor(identity), setIdentity, clear, modalOpen, openModal, closeModal }),
    [identity, ready, setIdentity, clear, modalOpen, openModal, closeModal],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTeam(): TeamState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useTeam must be used inside TeamProvider");
  return v;
}
