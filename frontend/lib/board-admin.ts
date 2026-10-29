"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Schemas } from "./api";
import { boardKeys, type BoardDetail } from "./boards";
import { workspaceKeys, type Visibility } from "./workspaces";

export type Board = Schemas["Board"];
export type BoardMember = Schemas["BoardMembers"]["members"][number];

/** A board changed: its page, and its workspace's lists (both, it may have been archived). */
function refreshBoard(queryClient: ReturnType<typeof useQueryClient>, board: Board) {
  queryClient.setQueryData<BoardDetail>(boardKeys.detail(board.id), (detail) => detail && { ...detail, ...board });
  void queryClient.invalidateQueries({ queryKey: workspaceKeys.boards(board.workspace_id) });
  void queryClient.invalidateQueries({ queryKey: workspaceKeys.detail(board.workspace_id) });
}

export function useUpdateBoard(boardId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name?: string; visibility?: Visibility; archived?: boolean }) =>
      api.patch("/boards/{id}", { path: { id: boardId }, body }),
    onSuccess: (board) => refreshBoard(queryClient, board),
  });
}

export function useDeleteBoard(boardId: string, workspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.delete("/boards/{id}", { path: { id: boardId } }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: boardKeys.detail(boardId) });
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.boards(workspaceId) });
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.detail(workspaceId) });
    },
  });
}

export function useDuplicateBoard(boardId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.post("/boards/{id}/duplicate", { path: { id: boardId }, body: {} }),
    onSuccess: (copy) => refreshBoard(queryClient, copy),
  });
}

export function useMoveBoard(boardId: string, fromWorkspaceId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (workspaceId: string) =>
      api.post("/boards/{id}/move", { path: { id: boardId }, body: { workspace_id: workspaceId } }),
    onSuccess: (board) => {
      void queryClient.invalidateQueries({ queryKey: ["workspaces", fromWorkspaceId] });
      void queryClient.invalidateQueries({ queryKey: boardKeys.detail(boardId) });
      refreshBoard(queryClient, board);
    },
  });
}

export const boardMemberKeys = (boardId: string) => ["boards", boardId, "members"] as const;

export function useBoardMembers(boardId: string, enabled = true) {
  return useQuery({
    queryKey: boardMemberKeys(boardId),
    queryFn: ({ signal }) => api.get("/boards/{id}/members", { path: { id: boardId }, signal }).then((r) => r.members),
    enabled,
  });
}

export function useSetBoardMembers(boardId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userIds: string[]) =>
      api.put("/boards/{id}/members", { path: { id: boardId }, body: { user_ids: userIds } }).then((r) => r.members),
    onSuccess: (members) => queryClient.setQueryData(boardMemberKeys(boardId), members),
  });
}
