"use client";

import { Async } from "../../components/Async.tsx";
import { Notes } from "../../components/Notes.tsx";
import { three, two } from "../../lib/format.ts";
import type { Backtest, Health } from "../../lib/types.gen.ts";
import { useApi } from "../../lib/useApi.ts";

export default function Model() {
  const bt = useApi<Backtest>("/api/backtest");
  const health = useApi<Health>("/api/health/runs");
  return (
    <>
      <h1>Model</h1>
      <p className="muted">
        How well each way of scoring players ranked gameweeks that had already been played, using only the gameweeks before each.{" "}
        <strong>Spearman</strong> is rank correlation with actual points (1 is perfect, 0 is noise); <strong>Top-20</strong> is the average actual points of the 20 players ranked highest.
      </p>
      {health.data ? <ModelHealth h={health.data} /> : null}
      <Async state={bt}>
        {(b) => (
          <>
            <Notes notes={b.notes} />
            <h2>
              Models, GW{b.first_gameweek}–{b.last_gameweek}
            </h2>
            <div className="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th>Model</th>
                    <th className="num">Spearman</th>
                    <th className="num">Error (pts)</th>
                    <th className="num">Top-20</th>
                  </tr>
                </thead>
                <tbody>
                  {b.models.map((m) => (
                    <tr key={m.name}>
                      <td>{m.name}</td>
                      <td className="num">{three(m.spearman)}</td>
                      <td className="num">{two(m.mae)}</td>
                      <td className="num">{two(m.top20)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <h2>Each gameweek</h2>
            <p className="muted">
              The model beats the old form-based scorer in {b.model_wins} of {b.per_gameweek.length} gameweeks.
            </p>
            <div className="bars">
              {b.per_gameweek.map((g) => (
                <div className="barrow" key={g.gameweek}>
                  <span className="barlabel">GW{g.gameweek}</span>
                  <div className="bartrack">
                    <div className="bar old" style={{ width: `${Math.max(0, g.old_spearman) * 100}%` }} />
                    <div className="bar model" style={{ width: `${Math.max(0, g.model_spearman) * 100}%` }} />
                  </div>
                  <span className="barvals">
                    old {three(g.old_spearman)} · model {three(g.model_spearman)}
                  </span>
                </div>
              ))}
            </div>
            <p className="legend">
              <span className="swatch old" /> old scorer <span className="swatch model" /> model
            </p>

            <h2>Out-of-sample check</h2>
            <p className="muted">Settings tuned on earlier gameweeks, then scored on one they never saw. This is the honest test.</p>
            <div className="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th>Gameweek</th>
                    <th>Tuned on</th>
                    <th className="num">Tuned</th>
                    <th className="num">Default</th>
                    <th className="num">Old scorer</th>
                  </tr>
                </thead>
                <tbody>
                  {b.walk_forward.map((w) => (
                    <tr key={w.gameweek}>
                      <td>GW{w.gameweek}</td>
                      <td>{w.tuned_on}</td>
                      <td className="num">{three(w.tuned_spearman)}</td>
                      <td className="num">{three(w.default_spearman)}</td>
                      <td className="num">{three(w.old_scorer_spearman)}</td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>Average</td>
                    <td className="num">{three(b.walk_forward_avg_tuned)}</td>
                    <td className="num">{three(b.walk_forward_avg_default)}</td>
                    <td className="num">{three(b.walk_forward_avg_old_scorer)}</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <h2>Best settings found</h2>
            <p className="muted">Treat the top few as a region, not a point. Default: rate {b.default_settings.rate_decay}, minutes {b.default_settings.minutes_decay}, prior {b.default_settings.prior_games}, slope {b.default_settings.fixture_slope}.</p>
            <div className="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th className="num">Rate decay</th>
                    <th className="num">Minutes decay</th>
                    <th className="num">Prior games</th>
                    <th className="num">Fixture slope</th>
                    <th className="num">Spearman</th>
                  </tr>
                </thead>
                <tbody>
                  {b.best_settings.map((s, i) => (
                    <tr key={i}>
                      <td className="num">{s.rate_decay}</td>
                      <td className="num">{s.minutes_decay}</td>
                      <td className="num">{s.prior_games}</td>
                      <td className="num">{s.fixture_slope}</td>
                      <td className="num">{three(s.spearman)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </Async>
    </>
  );
}

/** The nightly eval and the after-sync backtests, newest first, as the background workflows recorded them. */
function ModelHealth({ h }: { h: Health }) {
  if (h.evals.length === 0 && h.backtests.length === 0) {
    return (
      <p className="panel small muted">
        Model health appears here once the background worker has run a nightly eval or a backtest after a sync.
        {h.live ? null : " Start it with `gaffer worker`."}
      </p>
    );
  }
  return (
    <section className="panel">
      <h2>Model health</h2>
      <div className="health">
        <div>
          <h3>Assistant eval (nightly)</h3>
          {h.evals.length === 0 ? (
            <p className="muted small">No run yet.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>When</th>
                  <th className="num">Passed</th>
                  <th>Failing</th>
                </tr>
              </thead>
              <tbody>
                {h.evals.map((e, i) => (
                  <tr key={i} className={e.pass_rate < 0.9 ? "status-out" : undefined}>
                    <td>{e.age} ago</td>
                    <td className="num">{Math.round(e.pass_rate * 100)}%</td>
                    <td className="small">{e.failing.length > 0 ? e.failing.join(", ") : "none"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        <div>
          <h3>Default model backtest (after each sync)</h3>
          {h.backtests.length === 0 ? (
            <p className="muted small">No run yet.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Through</th>
                  <th className="num">Spearman</th>
                  <th className="num">Error</th>
                  <th>When</th>
                </tr>
              </thead>
              <tbody>
                {h.backtests.map((b, i) => (
                  <tr key={i}>
                    <td>GW{b.through_gameweek}</td>
                    <td className="num">{three(b.spearman)}</td>
                    <td className="num">{two(b.mae)}</td>
                    <td>{b.age} ago</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
      {h.temporal_ui ? (
        <p className="small">
          <a href={h.temporal_ui} target="_blank" rel="noopener noreferrer">
            Workflows in Temporal ↗
          </a>
        </p>
      ) : null}
    </section>
  );
}
