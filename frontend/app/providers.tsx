"use client";

import { useEffect, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { QueryClientProvider } from "@tanstack/react-query";
import { onSessionExpired } from "@/lib/api";
import { getQueryClient } from "@/lib/query-client";

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
