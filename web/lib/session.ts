/**
 * Whether the browser is signed in. Production answers at /oauth2/auth (oauth2-proxy, served
 * without a login): 202 signed in, 401 not. Anything else means there is no login to pass, as on
 * your own computer, where the dev server answers 404 and everyone is the local user.
 */
export type Session = "unknown" | "in" | "out";

export function sessionFrom(status: number): Session {
  return status === 401 || status === 403 ? "out" : "in";
}

/** Where "Sign in" goes: oauth2-proxy starts the login, then returns to rd (here, the home page). */
export const signInURL = `/oauth2/start?rd=${encodeURIComponent("/")}`;

/** Pages served without a login (see the gaffer-public ingress); everything else needs one. */
export const publicPaths = ["/", "/signed-out", "/privacy", "/terms"];

export function isPublicPath(path: string): boolean {
  return publicPaths.includes(path);
}
