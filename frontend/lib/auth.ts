"use client";

import { useQuery } from "@tanstack/react-query";
import { api, clearSession, startSession, type User } from "./api";

export const meQueryKey = ["me"] as const;

/** The signed-in user (GET /me). A 401 renews the session once before failing. */
export function useMe() {
  return useQuery({
    queryKey: meQueryKey,
    queryFn: ({ signal }) => api.get("/me", { signal }),
    staleTime: 5 * 60_000,
  });
}

export async function login(body: { email: string; password: string }): Promise<User> {
  const session = await api.post("/auth/login", { body });
  startSession(session);
  return session.user;
}

export async function register(body: { name: string; email: string; password: string }): Promise<User> {
  const session = await api.post("/auth/register", { body });
  startSession(session);
  return session.user;
}

/** Revokes the refresh token. The local session ends even if the API is unreachable. */
export async function logout() {
  try {
    await api.post("/auth/logout");
  } finally {
    clearSession();
  }
}
