import type { ReactNode } from "react";
import type { ApiState } from "../lib/useApi.ts";

/** Shows a loading line, the error with a retry, or the data. */
export function Async<T>({ state, children }: { state: ApiState<T>; children: (data: T) => ReactNode }) {
  if (state.error) {
    return (
      <div className="error" role="alert">
        <strong>Could not load this.</strong> {state.error.message}
        <button className="link" onClick={state.reload}>
          Try again
        </button>
      </div>
    );
  }
  if (state.data === undefined) return <p className="muted">Loading…</p>;
  return <>{children(state.data)}</>;
}
