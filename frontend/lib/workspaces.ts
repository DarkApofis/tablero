"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Schemas } from "./api";
import type { Role } from "./roles";

export type Workspace = Schemas["Workspace"];
export type Member = Schemas["Member"];

export const workspaceKeys = {
  all: ["workspaces"] as const,
  members: (id: string) => ["workspaces", id, "members"] as const,
  boards: (id: string) => ["workspaces", id, "boards"] as const,
};

// One request feeds both the list and the quota (each hook selects its part).
const workspacesQuery = {
  queryKey: workspaceKeys.all,
  queryFn: ({ signal }: { signal: AbortSignal }) => api.get("/workspaces", { signal }),
};

export function useWorkspaces() {
  return useQuery({ ...workspacesQuery, select: (r) => r.workspaces });
}

/** How many workspaces the user created, out of the most they may. */
export function useWorkspaceQuota() {
  return useQuery({
    ...workspacesQuery,
    // null from an API older than the quota (briefly, while a deploy rolls out).
    select: (r) =>
      typeof r.max_created === "number"
        ? { created: r.created, max: r.max_created, canCreate: r.created < r.max_created }
        : null,
  });
}

export function useCreateWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.post("/workspaces", { body: { name } }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true }),
  });
}

/** The columns every board made here starts with; admins can add more. */
export const DEFAULT_COLUMNS = ["Pendiente", "En progreso", "Hecho"];

export function useCreateBoard(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      api.post("/workspaces/{id}/boards", { path: { id: workspaceId }, body: { name, columns: DEFAULT_COLUMNS } }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceKeys.boards(workspaceId) }),
  });
}

export function useMembers(workspaceId: string) {
  return useQuery({
    queryKey: workspaceKeys.members(workspaceId),
    queryFn: ({ signal }) =>
      api.get("/workspaces/{id}/members", { path: { id: workspaceId }, signal }).then((r) => r.members),
  });
}

export function useBoards(workspaceId: string) {
  return useQuery({
    queryKey: workspaceKeys.boards(workspaceId),
    queryFn: ({ signal }) =>
      api.get("/workspaces/{id}/boards", { path: { id: workspaceId }, signal }).then((r) => r.boards),
  });
}

export function useChangeRole(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: Role }) =>
      api.patch("/workspaces/{id}/members/{userId}", { path: { id: workspaceId, userId }, body: { role } }),
    onSuccess: (updated) => {
      queryClient.setQueryData<Member[]>(workspaceKeys.members(workspaceId), (members) =>
        members?.map((m) => (m.user_id === updated.user_id ? updated : m)),
      );
      // The caller's own role in the list may have changed.
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true });
    },
  });
}

export function useRemoveMember(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      api.delete("/workspaces/{id}/members/{userId}", { path: { id: workspaceId, userId } }),
    onSuccess: (_, userId) => {
      queryClient.setQueryData<Member[]>(workspaceKeys.members(workspaceId), (members) =>
        members?.filter((m) => m.user_id !== userId),
      );
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true }); // member_count
    },
  });
}
