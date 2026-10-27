import { NextResponse, type NextRequest } from "next/server";
import { SESSION_COOKIE, safeNextPath } from "@/lib/session";

const PUBLIC_PATHS = ["/login", "/register"];

// Optimistic check on the session flag only: it keeps signed-out visitors off the
// app and signed-in ones off the login screens. The API is what enforces access; a
// stale flag ends at /login once the session fails to renew (see Providers).
export function proxy(request: NextRequest) {
  const { pathname, search, searchParams } = request.nextUrl;
  const signedIn = request.cookies.has(SESSION_COOKIE);
  const isPublic = PUBLIC_PATHS.includes(pathname);

  if (!signedIn && !isPublic) {
    const url = new URL("/login", request.url);
    if (pathname !== "/") url.searchParams.set("next", pathname + search);
    return NextResponse.redirect(url);
  }
  if (signedIn && isPublic) {
    return NextResponse.redirect(new URL(safeNextPath(searchParams.get("next")), request.url));
  }
  return NextResponse.next();
}

export const config = {
  // Everything but Next's assets, static files and /api (the API itself, forwarded
  // by next.config.ts: its calls must not be sent to the login page).
  matcher: ["/((?!api/|_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico)$).*)"],
};
