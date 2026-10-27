import { Hero } from "../../components/Hero.tsx";

/**
 * The title page people land on after signing out. It is outside the login (the ingress serves
 * it without one), so it stays put instead of bouncing straight back through GitHub, which would
 * sign a user who is still logged in to GitHub in again without asking. It reads no data.
 */
export default function SignedOut() {
  return (
    <div className="titlepage">
      <Hero>
        <p className="kicker">Fantasy Premier League · scouting room</p>
        <h1 className="herotitle">
          Let’s go, <span>Gaffer.</span>
        </h1>
        <p className="herosub">Your squad, your lineup, your transfers and the latest team news, picked apart by a model that shows its working.</p>
        <div className="ctas">
          <a className="button primary big" href="/">
            Sign in with GitHub
          </a>
        </div>
        <p className="titlenote">You’re signed out. You are still signed in to GitHub itself: sign out there too on a shared computer.</p>
      </Hero>
      <ul className="titlefeatures">
        <li>
          <strong>Best XI and captain</strong>
          <span>The eleven with the most expected points, and the close calls.</span>
        </li>
        <li>
          <strong>Transfers that pay</strong>
          <span>Every move weighed against its hit, and your own ideas tested.</span>
        </li>
        <li>
          <strong>Ask the Gaffer</strong>
          <span>Plain-English answers whose numbers are checked by code.</span>
        </li>
      </ul>
    </div>
  );
}
