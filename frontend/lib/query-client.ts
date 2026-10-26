import { QueryClient } from "@tanstack/react-query";
import { ApiError } from "./api";

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Board data is kept fresh by the SSE stream; refetching on every mount is noise.
        staleTime: 30_000,
        // 4xx won't fix itself on retry; network errors (status 0) and 5xx might.
        retry: (failureCount, error) =>
          failureCount < 2 &&
          !(error instanceof ApiError && error.status >= 400 && error.status < 500),
      },
    },
  });
}

let browserQueryClient: QueryClient | undefined;

/** The browser's one QueryClient; also reachable outside React, to start fetches early. */
export function getQueryClient() {
  // Keep server requests isolated and preserve the browser cache across renders.
  if (typeof window === "undefined") return makeQueryClient();
  browserQueryClient ??= makeQueryClient();
  return browserQueryClient;
}
