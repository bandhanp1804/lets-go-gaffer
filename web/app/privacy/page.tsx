import type { Metadata } from "next";

export const metadata: Metadata = { title: "Privacy policy · Let’s Go Gaffer" };

const contact = "risingspy007@gmail.com";
const updated = "28 October 2026";

/**
 * The privacy policy. Public (served without sign-in, see the ingress), and written to match
 * what the code actually does: change it when the data handling changes.
 */
export default function Privacy() {
  return (
    <article className="legal">
      <h1>Privacy policy</h1>
      <p className="muted">Last updated {updated}.</p>

      <p>
        Let’s Go Gaffer is a free, non-commercial Fantasy Premier League assistant run by Bandhan Patel as a personal project. This page says what it collects when you use it, why, who
        else sees it, and how to have it removed. Questions or requests: <a href={`mailto:${contact}`}>{contact}</a>.
      </p>

      <h2>What we collect</h2>
      <ul>
        <li>
          <strong>Your sign-in.</strong> When you sign in with GitHub, Google or Microsoft, that service tells us your email address and your name or username. We use the email to
          recognise you. We never see your password.
        </li>
        <li>
          <strong>Settings you choose to save.</strong> Your FPL team id and, if you add one, your FPL login token. Both are encrypted (AES-256-GCM) with a key that is kept apart from
          the database, and the token is never shown back to any browser. An FPL token stops working after about an hour.
        </li>
        <li>
          <strong>How much you use the costly features.</strong> A daily count of your Ask questions, news searches and news refreshes, used only to apply the daily limits.
        </li>
        <li>
          <strong>Your questions to Ask.</strong> They are sent to Anthropic to be answered. A conversation is held in the server’s memory while you use it and forgotten after 30
          idle minutes; it is not saved to the database.
        </li>
        <li>
          <strong>Technical logs.</strong> The sign-in service records your email, the page or address requested and the time; the app records the path, the result and how long it
          took, without your search text or any token. These logs stay in the servers’ own log files, which are regularly overwritten.
        </li>
        <li>
          <strong>Cookies and browser storage.</strong> Sign-in cookies (one for your session, and a short-lived one that protects the sign-in step itself), and a cookie that keeps your Ask conversation on the same server. If saved settings are turned off, your
          browser may remember your team id itself. There are no advertising or analytics cookies.
        </li>
      </ul>

      <h2>What we do not do</h2>
      <p>We do not sell your data, show ads, or use analytics or tracking services. We never change anything in your FPL account: your token is used only to read your squad.</p>

      <h2>Who else handles it</h2>
      <ul>
        <li>
          <strong>Microsoft Azure</strong> hosts the app and its database, in the United States.
        </li>
        <li>
          <strong>Fantasy Premier League</strong> (fantasy.premierleague.com) receives your team id, and your token if you saved one, to fetch your squad.
        </li>
        <li>
          <strong>Anthropic</strong> receives your Ask questions and the squad and player details needed to answer them.
        </li>
        <li>
          <strong>Voyage AI</strong> receives the text of news searches and Ask questions about news, to match them with stories.
        </li>
        <li>
          <strong>GitHub, Google or Microsoft</strong>, whichever you sign in with, under their own privacy policies.
        </li>
      </ul>

      <h2>How long we keep it</h2>
      <ul>
        <li>Saved settings: until you remove them on the Setup page or ask us to.</li>
        <li>Daily usage counts: 30 days, then deleted automatically.</li>
        <li>Ask conversations: not stored.</li>
        <li>Logs: until the log files are overwritten.</li>
      </ul>

      <h2>Your choices</h2>
      <p>
        You can remove your saved team and token at any time on the <strong>Setup</strong> page, and sign out from the menu. To have everything about you deleted, or to get a
        copy of it, email <a href={`mailto:${contact}`}>{contact}</a> from the address you sign in with; we reply within 30 days.
      </p>

      <h2>Security</h2>
      <p>The site is served only over HTTPS, the database is on a private network, and saved settings are encrypted as described above.</p>

      <h2>Children</h2>
      <p>The app is not meant for children under 13, or under 16 in the European Economic Area and the UK.</p>

      <h2>Changes</h2>
      <p>If this policy changes, the date at the top changes too.</p>
    </article>
  );
}
