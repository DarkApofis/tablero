"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Schemas } from "./api";
import type { Role } from "./roles";

export type Workspace = Schemas["Workspace"];
export type WorkspaceDetail = Schemas["WorkspaceDetail"];
export type Member = Schemas["Member"];
export type Invitation = Schemas["Invitation"];
export type InviteLink = Schemas["InviteLink"];
export type BoardSummary = Schemas["BoardSummary"];
export type Visibility = Schemas["Visibility"];

export const workspaceKeys = {
  all: ["workspaces"] as const,
  detail: (id: string) => ["workspaces", id, "detail"] as const,
  members: (id: string) => ["workspaces", id, "members"] as const,
  boards: (id: string) => ["workspaces", id, "boards"] as const,
  archived: (id: string) => ["workspaces", id, "boards", "archived"] as const,
  invitations: (id: string) => ["workspaces", id, "invitations"] as const,
  inviteLink: (id: string) => ["workspaces", id, "invite-link"] as const,
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

/** One workspace with its settings, its owner and what it holds. */
export function useWorkspace(workspaceId: string) {
  return useQuery({
    queryKey: workspaceKeys.detail(workspaceId),
    queryFn: ({ signal }) => api.get("/workspaces/{id}", { path: { id: workspaceId }, signal }),
  });
}

/** Refreshes everything that shows the workspace: the list, its page, its boards. */
function invalidateWorkspace(queryClient: ReturnType<typeof useQueryClient>, workspaceId: string) {
  void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true });
  void queryClient.invalidateQueries({ queryKey: ["workspaces", workspaceId] });
}

export function useCreateWorkspace() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; slug?: string }) => api.post("/workspaces", { body }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true }),
  });
}

/**
 * Whether an address is free (`slug`), or the free one a name would get
 * (`name`). Not a hook: the create dialog calls it as the user types.
 */
export function checkSlug(query: { slug: string } | { name: string }, signal?: AbortSignal) {
  return api.get("/workspace-slugs", { query, signal });
}

export function useUpdateWorkspace(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name?: string; slug?: string; description?: string; default_role?: Role }) =>
      api.patch("/workspaces/{id}", { path: { id: workspaceId }, body }),
    onSuccess: (detail) => {
      queryClient.setQueryData(workspaceKeys.detail(workspaceId), detail);
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true });
    },
  });
}

export function useTransferWorkspace(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      api.post("/workspaces/{id}/transfer", { path: { id: workspaceId }, body: { user_id: userId } }),
    onSuccess: (detail) => queryClient.setQueryData(workspaceKeys.detail(workspaceId), detail),
  });
}

export function useDeleteWorkspace(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.delete("/workspaces/{id}", { path: { id: workspaceId } }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: ["workspaces", workspaceId] });
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true });
    },
  });
}

/** A board template: the columns a new board starts with. */
export type BoardTemplate = { id: string; name: string; description: string; columns: string[] };

export const BOARD_TEMPLATES: BoardTemplate[] = [
  { id: "blank", name: "En blanco", description: "Empieza sin columnas.", columns: [] },
  { id: "kanban", name: "Kanban básico", description: "Tres columnas listas para usar.", columns: ["Por hacer", "En curso", "Hecho"] },
  { id: "sprint", name: "Sprint", description: "Backlog, sprint actual y revisión.", columns: ["Backlog", "Sprint", "Revisión", "Hecho"] },
];

export const DEFAULT_TEMPLATE = BOARD_TEMPLATES[1];

export function useCreateBoard(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; columns: string[]; visibility: Visibility }) =>
      api.post("/workspaces/{id}/boards", { path: { id: workspaceId }, body }),
    onSuccess: () => invalidateWorkspace(queryClient, workspaceId),
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

export function useArchivedBoards(workspaceId: string) {
  return useQuery({
    queryKey: workspaceKeys.archived(workspaceId),
    queryFn: ({ signal }) =>
      api
        .get("/workspaces/{id}/boards", { path: { id: workspaceId }, query: { archived: true }, signal })
        .then((r) => r.boards),
  });
}

/** Who is watching a board right now (the "● 3 en vivo" of its card). */
export function useBoardPresence(boardId: string) {
  return useQuery({
    queryKey: ["boards", boardId, "presence"],
    queryFn: ({ signal }) => api.get("/boards/{id}/presence", { path: { id: boardId }, signal }).then((r) => r.users),
    refetchInterval: 30_000,
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
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.detail(workspaceId) });
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
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.detail(workspaceId) });
    },
  });
}

// --- invitations ---

export function useInvitations(workspaceId: string, enabled = true) {
  return useQuery({
    queryKey: workspaceKeys.invitations(workspaceId),
    queryFn: ({ signal }) =>
      api.get("/workspaces/{id}/invitations", { path: { id: workspaceId }, signal }).then((r) => r.invitations),
    enabled,
  });
}

/** Invites to the workspace in the variables (it may have just been created). */
export function useInvite() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, ...body }: { workspaceId: string; emails: string[]; role: Role }) =>
      api.post("/workspaces/{id}/invitations", { path: { id: workspaceId }, body }),
    onSuccess: (res, { workspaceId }) => {
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.invitations(workspaceId) });
      if (res.added.length > 0) {
        void queryClient.invalidateQueries({ queryKey: workspaceKeys.members(workspaceId) });
        invalidateWorkspace(queryClient, workspaceId);
      }
    },
  });
}

export function useRenewInvitation(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (invitationId: string) =>
      api.post("/workspaces/{id}/invitations/{invitationId}/renew", { path: { id: workspaceId, invitationId } }),
    onSuccess: (renewed) =>
      queryClient.setQueryData<Invitation[]>(workspaceKeys.invitations(workspaceId), (list) =>
        list?.map((i) => (i.id === renewed.id ? renewed : i)),
      ),
  });
}

export function useCancelInvitation(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (invitationId: string) =>
      api.delete("/workspaces/{id}/invitations/{invitationId}", { path: { id: workspaceId, invitationId } }),
    onSuccess: (_, invitationId) =>
      queryClient.setQueryData<Invitation[]>(workspaceKeys.invitations(workspaceId), (list) =>
        list?.filter((i) => i.id !== invitationId),
      ),
  });
}

export function useInviteLink(workspaceId: string, enabled = true) {
  return useQuery({
    queryKey: workspaceKeys.inviteLink(workspaceId),
    queryFn: ({ signal }) =>
      api.get("/workspaces/{id}/invite-link", { path: { id: workspaceId }, signal }).then((r) => r.link),
    enabled,
  });
}

export function useCreateInviteLink(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (role: Role) =>
      api.post("/workspaces/{id}/invite-link", { path: { id: workspaceId }, body: { role } }).then((r) => r.link),
    onSuccess: (link) => queryClient.setQueryData(workspaceKeys.inviteLink(workspaceId), link),
  });
}

export function useDeleteInviteLink(workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.delete("/workspaces/{id}/invite-link", { path: { id: workspaceId } }),
    onSuccess: () => queryClient.setQueryData(workspaceKeys.inviteLink(workspaceId), null),
  });
}

/** The page an invitation link opens: /join/<token>, on this site. */
export function inviteUrl(token: string) {
  return `${window.location.origin}/join/${token}`;
}

/** Where a workspace's address takes you: /w/<slug>, on this site. */
export function addressPrefix() {
  return typeof window === "undefined" ? "/w/" : `${window.location.host}/w/`;
}
