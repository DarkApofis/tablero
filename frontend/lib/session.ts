// Shared by proxy.ts and the browser, so it must not import anything.

/**
 * Non-HttpOnly flag set while the browser holds a session. The API's own cookies
 * are scoped to its /auth and /boards paths (and, in production, its domain), so
 * the proxy cannot see them; this flag only steers routing. The API decides
 * whether the session is valid.
 */
export const SESSION_COOKIE = "tablero_session";

const MAX_AGE = 30 * 24 * 60 * 60; // the API's default refresh-token lifetime

export function markSignedIn() {
  const secure = location.protocol === "https:" ? "; Secure" : "";
  document.cookie = `${SESSION_COOKIE}=1; Path=/; Max-Age=${MAX_AGE}; SameSite=Lax${secure}`;
}

export function clearSignedIn() {
  document.cookie = `${SESSION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
}

/** `next` if it is a path on this site, else "/". Prevents open redirects. */
export function safeNextPath(next: string | null | undefined): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  return next;
}
