import Link from "next/link";

/** On every page: the policies (public, so they can be read before signing in) and the disclaimer. */
export function Footer() {
  return (
    <footer className="sitefooter">
      <Link href="/privacy">Privacy</Link>
      <span aria-hidden="true">·</span>
      <Link href="/terms">Terms</Link>
      <span aria-hidden="true">·</span>
      <span className="muted">Not affiliated with the Premier League or Fantasy Premier League.</span>
    </footer>
  );
}
