import type { ReactNode } from "react";
import { signInURL } from "../lib/session.ts";
import { Hero } from "./Hero.tsx";

const features = [
  { icon: "①", title: "Best XI and captain", text: "The eleven with the most expected points, the armband, and the close calls the numbers cannot settle." },
  { icon: "⇄", title: "Transfers that pay", text: "Every move weighed against its 4-point hit, and your own ideas tested before you commit." },
  { icon: "💬", title: "Ask the Gaffer", text: "Plain-English answers that quote the tools and cite the news, with every number checked by code." },
  { icon: "📰", title: "Team news, gathered", text: "BBC, Sky, the Guardian and more, pulled every half hour and searchable by meaning, not keywords." },
  { icon: "⭐", title: "Alerts on your players", text: "Star a player and his injury news and status changes land in your own inbox." },
  { icon: "▦", title: "Wildcard builder", text: "The best legal fifteen from scratch under £100m, within FPL's rules." },
];

const steps = [
  { title: "Sign in", text: "With GitHub, Google or Microsoft. No new password, and it is free." },
  { title: "Load your team", text: "Enter your FPL team id: the number in the address of your Points page." },
  { title: "Make the call", text: "Your best XI, captain, transfers and the news behind them, for the coming deadline." },
];

const stack = ["Go", "Postgres + pgvector", "Temporal", "Claude", "Voyage embeddings", "Next.js", "Kubernetes on Azure", "Datadog"];

/**
 * The page people see before signing in: what the app does and how to start. It is served
 * without a login and reads no data. note adds a line under the buttons (the signed-out page
 * uses it to say what signing out did and did not do).
 */
export function Landing({ note }: { note?: ReactNode }) {
  return (
    <div className="landing">
      <Hero>
        <p className="kicker">Fantasy Premier League · scouting room</p>
        <h1 className="herotitle">
          Let’s go, <span>Gaffer.</span>
        </h1>
        <p className="herosub">Your squad, your lineup, your transfers and the latest team news, picked apart by a model that shows its working.</p>
        <div className="ctas">
          <a className="button primary big" href={signInURL}>
            Sign in to start
          </a>
          <a className="button ghost big" href="#how">
            How it works
          </a>
        </div>
        <p className="titlenote">{note ?? "Free to use. Sign in with GitHub, Google or Microsoft."}</p>
      </Hero>
      <p className="small muted">
        By signing in you agree to the <a href="/terms">terms of use</a>; see the <a href="/privacy">privacy policy</a> for how your data is handled.
      </p>

      <h2>What it does</h2>
      <ul className="features landingfeatures">
        {features.map((f) => (
          <li key={f.title} className="feature">
            <span className="icon" aria-hidden="true">
              {f.icon}
            </span>
            <strong>{f.title}</strong>
            <span className="muted">{f.text}</span>
          </li>
        ))}
      </ul>

      <h2 id="how">How it works</h2>
      <ol className="steps">
        {steps.map((s, i) => (
          <li key={s.title}>
            <span className="stepnum" aria-hidden="true">
              {i + 1}
            </span>
            <strong>{s.title}</strong>
            <span className="muted">{s.text}</span>
          </li>
        ))}
      </ol>

      <section className="panel trust">
        <h2>Numbers you can check</h2>
        <p>
          The AI explains; it never does the maths. Expected points, lineups, transfers and FPL’s rules are all worked out in code, and the model is scored on gameweeks it never
          saw before it is trusted with yours. When the numbers cannot separate two choices, it says so.
        </p>
      </section>

      <section className="stack" aria-label="How it is built">
        <span className="muted">Built with</span>
        <ul>
          {stack.map((t) => (
            <li key={t}>{t}</li>
          ))}
        </ul>
      </section>

      <div className="landingcta">
        <strong>The deadline will not wait.</strong>
        <a className="button primary big" href={signInURL}>
          Sign in to start
        </a>
      </div>
    </div>
  );
}
