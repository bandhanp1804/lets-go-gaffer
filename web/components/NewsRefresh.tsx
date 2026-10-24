"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { getJSON, postJSON } from "../lib/api.ts";
import { ago, sourceLabel } from "../lib/format.ts";
import type { RefreshStatus } from "../lib/types.gen.ts";

const POLL_MS = 2000;

/**
 * The button that pulls the news again, and what the last pull did. A pull can take a minute on a
 * rate-limited embedding key, so the server answers at once and this polls until it is done, then
 * calls onFinished so the page can search again and show the new stories.
 */
export function NewsRefresh({ onFinished }: { onFinished: () => void }) {
  const [st, setSt] = useState<RefreshStatus>();
  const [problem, setProblem] = useState("");
  const wasRunning = useRef(false);

  const load = useCallback(async () => {
    try {
      setSt(await getJSON<RefreshStatus>("/api/news/refresh"));
    } catch {
      setSt(undefined); // an old API without the endpoint: the button simply does not show
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // While a pull runs, ask again every couple of seconds; when it stops, tell the page.
  useEffect(() => {
    if (st?.running) {
      wasRunning.current = true;
      const id = setTimeout(() => void load(), POLL_MS);
      return () => clearTimeout(id);
    }
    if (wasRunning.current) {
      wasRunning.current = false;
      onFinished();
    }
  }, [st, load, onFinished]);

  if (!st?.available) return null;

  async function start() {
    setProblem("");
    try {
      setSt(await postJSON<RefreshStatus>("/api/news/refresh"));
    } catch (e) {
      setProblem(e instanceof Error ? e.message : "Could not start a refresh.");
    }
  }

  return (
    <p className="small newsrefresh">
      <button type="button" disabled={st.running} onClick={() => void start()}>
        {st.running ? "Pulling the latest…" : "Refresh news"}
      </button>{" "}
      <span className="muted">
        {st.running
          ? "Fetching the feeds and embedding new stories. This can take a minute."
          : st.last_error
            ? `The last refresh failed: ${st.last_error}`
            : st.last_finished
              ? `Last refreshed ${ago(st.last_finished)}: ${st.last_new} new of ${st.last_fetched} fetched.`
              : "Not refreshed since the server started."}
        {st.every && !st.running ? ` Refreshes by itself every ${st.every}.` : ""}
        {/* An older API sends no last_failed_feeds. */}
        {!st.running && (st.last_failed_feeds ?? []).length > 0 ? ` Could not read: ${st.last_failed_feeds.map(sourceLabel).join(", ")}.` : ""}
      </span>
      {problem ? <span className="error"> {problem}</span> : null}
    </p>
  );
}
