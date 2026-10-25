"use client";

import { useEffect, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError, onSessionExpired } from "@/lib/api";

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

function getQueryClient() {
  // Keep server requests isolated and preserve the browser cache across renders.
  if (typeof window === "undefined") return makeQueryClient();
  browserQueryClient ??= makeQueryClient();
  return browserQueryClient;
}

export function Providers({ children }: { children: ReactNode }) {
  const queryClient = getQueryClient();
  const router = useRouter();

  // The refresh token was rejected: drop cached data and send the user to log in,
  // coming back here afterwards.
  useEffect(
    () =>
      onSessionExpired(() => {
        queryClient.clear();
        const here = location.pathname + location.search;
        if (here.startsWith("/login") || here.startsWith("/register")) return;
        router.replace(here === "/" ? "/login" : `/login?next=${encodeURIComponent(here)}`);
      }),
    [queryClient, router],
  );

  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}
