import { createSSEParser } from "./sse.ts";
import type { AskDone } from "./types.ts";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** Reads the {"error": "..."} body the Go server sends, falling back to the status. */
async function failure(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as { error?: string };
    if (body.error) return new ApiError(res.status, body.error);
  } catch {
    // not JSON (for example the dev server could not reach the API)
  }
  if (res.status >= 500) return new ApiError(res.status, "The API did not respond. Is `gaffer serve` running?");
  return new ApiError(res.status, `Request failed (${res.status})`);
}

export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal, cache: "no-store" });
  if (!res.ok) throw await failure(res);
  return (await res.json()) as T;
}

/** Builds "?a=1&b=x", leaving out empty values. */
export function query(params: Record<string, string | number | boolean | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "" || v === false) continue;
    q.set(k, String(v));
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

export interface AskHandlers {
  onSession(id: string): void;
  onText(delta: string): void;
  onDone(done: AskDone): void;
  onError(message: string): void;
}

/** Asks a question and streams the answer. Resolves when the stream ends. */
export async function ask(question: string, sessionId: string | undefined, h: AskHandlers, signal?: AbortSignal): Promise<void> {
  const res = await fetch("/api/ask", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ question, session_id: sessionId || undefined }),
    signal,
  });
  if (!res.ok) throw await failure(res);
  if (!res.body) throw new ApiError(500, "The browser could not read the answer stream.");

  const feed = createSSEParser((e) => {
    const data = JSON.parse(e.data) as Record<string, unknown>;
    switch (e.event) {
      case "session":
        h.onSession(String(data.session_id));
        break;
      case "text":
        h.onText(String(data.delta));
        break;
      case "done":
        h.onDone(data as unknown as AskDone);
        break;
      case "error":
        h.onError(String(data.error));
        break;
    }
  });
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    feed(decoder.decode(value, { stream: true }));
  }
}
