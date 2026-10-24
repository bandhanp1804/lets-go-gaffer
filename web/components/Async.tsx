import type { ReactNode } from "react";
import type { ApiState } from "../lib/useApi.ts";
import { useTeam } from "./TeamProvider.tsx";

/** Shows a loading line, the error with a retry, or the data. */
export function Async<T>({ state, children }: { state: ApiState<T>; children: (data: T) => ReactNode }) {
  const { openModal } = useTeam();
  if (state.error?.code === "team_required") {
    return (
      <div className="panel callout">
        <strong>Which team?</strong> Enter your FPL team id and this page will show your squad.
        <div>
          <button type="button" className="primary" onClick={openModal}>
            Enter my team id
          </button>
        </div>
      </div>
    );
  }
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
