import { ApiError } from "@/lib/api";
import { safeNextPath } from "@/lib/session";

/** Message for a failure that is not about the form's fields. */
export function describeError(err: unknown): string {
  // Both come with a message meant for the visitor (the wait, for rate_limited).
  if (err instanceof ApiError && (err.code === "network_error" || err.code === "rate_limited")) return err.message;
  return "Algo salió mal. Inténtalo de nuevo en un momento.";
}

/** Where to go after signing in: the page the proxy sent the user away from. */
export function nextPath(): string {
  return safeNextPath(new URLSearchParams(window.location.search).get("next"));
}

/** Length in characters, as the API counts it (an emoji is one, not two). */
export const charCount = (s: string) => [...s].length;
