import { Landing } from "../../components/Landing.tsx";

/**
 * Where signing out lands. It is outside the login (the ingress serves it without one), so it
 * stays put instead of bouncing straight back through a provider that would sign a user who is
 * still logged in there in again without asking. It reads no data.
 */
export default function SignedOut() {
  return (
    <Landing note="You’re signed out. You are still signed in with GitHub, Google or Microsoft, whichever you used: sign out there too on a shared computer." />
  );
}
