import type { NextConfig } from "next";

const isDev = process.env.NODE_ENV !== "production";
// The API as the browser reaches it (lib/api.ts): another origin, or a path on
// this one ("/api") that the rewrite below forwards to API_PROXY_TARGET.
const apiUrl = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
// The only other origin the pages talk to (fetch and EventSource), if any.
const apiOrigin = /^https?:\/\//.test(apiUrl) ? new URL(apiUrl).origin : "";
// Server-side only: where /api/* goes. Set it to serve the API from this origin,
// so its cookies are first-party (a frontend on *.vercel.app and an API on
// *.onrender.com are different sites; browsers that block third-party cookies
// would drop the session). docs/DEPLOY.md.
const apiProxyTarget = process.env.API_PROXY_TARGET?.replace(/\/$/, "");

// Content-Security-Policy (docs/SECURITY.md). Scripts and styles come from this
// origin only; 'unsafe-inline' stays for the inline scripts Next.js emits to
// hydrate statically rendered pages, since nonces would force every page to
// render per request. No third-party code is loaded, React escapes everything
// it renders and connect-src keeps an injected script from sending data anywhere
// but the API. Development adds what the dev server needs (eval, HMR socket).
const csp = [
  "default-src 'self'",
  `script-src 'self' 'unsafe-inline'${isDev ? " 'unsafe-eval'" : ""}`,
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "font-src 'self'",
  `connect-src 'self'${apiOrigin ? ` ${apiOrigin}` : ""}${isDev ? " ws:" : ""}`,
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join("; ");

const securityHeaders = [
  { key: "Content-Security-Policy", value: csp },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=(), payment=(), usb=()" },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  // Only honored over HTTPS; browsers ignore it on http://localhost.
  ...(isDev ? [] : [{ key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" }]),
];

const nextConfig: NextConfig = {
  // A self-contained server (.next/standalone) with only the node_modules it
  // uses, for the container image (Dockerfile).
  output: "standalone",
  poweredByHeader: false,
  cacheComponents: true,
  partialPrefetching: true,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }];
  },
  // /api/auth/login → API_PROXY_TARGET/auth/login, streams included (the API's
  // 25 s heartbeat keeps a proxied stream open). The API's own security headers
  // come back with its responses.
  async rewrites() {
    return apiProxyTarget ? [{ source: "/api/:path*", destination: `${apiProxyTarget}/:path*` }] : [];
  },
};

export default nextConfig;
