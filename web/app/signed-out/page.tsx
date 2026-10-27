/**
 * Where "Sign out" lands. It is outside the login (the ingress serves it without one), so it
 * stays put instead of bouncing straight back through GitHub, which would sign a user who is
 * still logged in to GitHub in again without asking.
 */
export default function SignedOut() {
  return (
    <section className="panel signedout">
      <h1>You’re signed out</h1>
      <p className="muted">
        Your session here has ended. You are still signed in to GitHub itself: sign out there too if this is a shared computer.
      </p>
      <p>
        <a className="button primary" href="/">
          Sign in again
        </a>
      </p>
    </section>
  );
}
