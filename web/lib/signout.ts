/**
 * Where "Sign out" goes. The login cookie belongs to production's address, which a preview
 * (pr-N.<production host>) shares, so signing out always happens there. It then lands on
 * production's "signed out" page, which is outside the login: landing on any other page would
 * send a user still logged in to GitHub straight back in.
 */
export function signOutURL(host: string): string {
  const prod = host.replace(/^pr-\d+\./, "");
  return `https://${prod}/oauth2/sign_out?rd=${encodeURIComponent(`https://${prod}/signed-out`)}`;
}

/** Signing out only means something behind the login; on your own computer there is none. */
export function canSignOut(user: string | undefined): boolean {
  return user !== undefined && user !== "" && user !== "local";
}
