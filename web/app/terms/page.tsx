import type { Metadata } from "next";

export const metadata: Metadata = { title: "Terms of use · Let’s Go Gaffer" };

const contact = "risingspy007@gmail.com";
const updated = "28 October 2026";

/** The terms of use. Public, like the privacy policy. */
export default function Terms() {
  return (
    <article className="legal">
      <h1>Terms of use</h1>
      <p className="muted">Last updated {updated}.</p>

      <p>
        Let’s Go Gaffer is a free, non-commercial Fantasy Premier League assistant run by Bandhan Patel as a personal project. By signing in you agree to these terms. Questions:{" "}
        <a href={`mailto:${contact}`}>{contact}</a>.
      </p>

      <h2>Not affiliated with the Premier League</h2>
      <p>
        This app is not made, endorsed or supported by the Premier League or Fantasy Premier League. Their names and data belong to them; the app reads public FPL data, and your
        own squad when you give it your login token.
      </p>

      <h2>Advice, not guarantees</h2>
      <p>
        Expected points, lineups, transfers, chip advice and the assistant’s answers are estimates and can be wrong. They are for fun and information only. Your FPL decisions,
        and their results, are yours.
      </p>

      <h2>Your FPL token</h2>
      <p>
        Giving the app your FPL login token is optional. It is used only to read your squad, never to change anything, and you can remove it at any time on the Setup page. Do not
        enter anyone else’s token.
      </p>

      <h2>Fair use</h2>
      <ul>
        <li>Use the app for your own, non-commercial purposes.</li>
        <li>Do not try to get around the daily limits, overload the service, scrape it automatically, or break into it.</li>
        <li>Access can be suspended for anyone who misuses it.</li>
      </ul>

      <h2>The service</h2>
      <p>
        The app is provided as it is, without any warranty, and may change, be unavailable, or stop at any time. To the extent the law allows, the person who runs it is not liable
        for any loss from using it. Signing in also means using GitHub, Google or Microsoft under their own terms.
      </p>

      <h2>Your data</h2>
      <p>
        How your data is handled is described in the <a href="/privacy">privacy policy</a>.
      </p>

      <h2>Changes</h2>
      <p>If these terms change, the date at the top changes too.</p>
    </article>
  );
}
