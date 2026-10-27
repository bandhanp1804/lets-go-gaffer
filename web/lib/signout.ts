/**
 * Where "Sign out" goes. The login cookie belongs to production's address, which a preview
 * (pr-N.<production host>) shares, so signing out always happens there and then lands on
 * production's home page, which asks for the login again.
 */
export function signOutURL(host: string): string {
  const prod = host.replace(/^pr-\d+\./, "");
  return `https://${prod}/oauth2/sign_out?rd=${encodeURIComponent(`https://${prod}/`)}`;
}

/** Signing out only means something behind the login; on your own computer there is none. */
export function canSignOut(user: string | undefined): boolean {
  return user !== undefined && user !== "" && user !== "local";
}
