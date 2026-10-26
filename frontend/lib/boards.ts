"use client";

import { useMutation, useMutationState, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, isTaskConflict, type Schemas, type Task } from "./api";
import type { Priority } from "./tasks";

export type BoardDetail = Schemas["BoardDetail"];
export type BoardColumn = BoardDetail["columns"][number];
export type Label = Schemas["Label"];

export const boardKeys = {
  detail: (id: string) => ["boards", id] as const,
};

export function useBoard(boardId: string) {
  return useQuery({
    queryKey: boardKeys.detail(boardId),
    queryFn: ({ signal }) => api.get("/boards/{id}", { path: { id: boardId }, signal }),
  });
}

/** Where a task goes: a column and, optionally, the tasks it lands between. */
export type MoveTarget = { columnId: string; afterTaskId: string | null; beforeTaskId: string | null };

type MoveVars = { task: Task; to: MoveTarget; position: number };

// Same values as the server (internal/domain/position.go).
const POSITION_STEP = 1024;

/** Columns order their tasks by position, then id, like the server does. */
function byPosition(a: Task, b: Task) {
  return a.position - b.position || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

/** Puts `task` in its column at its position, taking it out of wherever it was. */
export function withTask(board: BoardDetail, task: Task): BoardDetail {
  return {
    ...board,
    columns: board.columns.map((c) => {
      const rest = c.tasks.filter((t) => t.id !== task.id);
      if (c.id === task.column_id) return { ...c, tasks: [...rest, task].sort(byPosition) };
      return rest.length === c.tasks.length ? c : { ...c, tasks: rest };
    }),
  };
}

/**
 * The position the server will give the task, from the same neighbors: with one
 * neighbor the other is the task next to it, with none it goes at the end.
 */
export function positionFor(board: BoardDetail, taskId: string, to: MoveTarget) {
  const tasks = (board.columns.find((c) => c.id === to.columnId)?.tasks ?? []).filter((t) => t.id !== taskId);
  const at = (id: string) => tasks.findIndex((t) => t.id === id);
  let prev: Task | undefined;
  let next: Task | undefined;
  if (to.afterTaskId) {
    prev = tasks[at(to.afterTaskId)];
    next = to.beforeTaskId ? tasks[at(to.beforeTaskId)] : tasks[at(to.afterTaskId) + 1];
  } else if (to.beforeTaskId) {
    next = tasks[at(to.beforeTaskId)];
    prev = tasks[at(to.beforeTaskId) - 1];
  } else {
    prev = tasks.at(-1);
  }
  if (prev && next) return prev.position + (next.position - prev.position) / 2;
  if (prev) return prev.position + POSITION_STEP;
  if (next) return next.position - POSITION_STEP;
  return POSITION_STEP;
}

/**
 * Moves a task optimistically. The cache changes synchronously, so a drop never
 * flashes the old order. A failed move undoes only itself: other moves still in
 * flight keep theirs. On a 409 the task takes the state the server sent.
 */
export function useMoveTask(boardId: string) {
  const queryClient = useQueryClient();
  const queryKey = boardKeys.detail(boardId);
  const mutationKey = ["moveTask", boardId];

  const mutation = useMutation({
    mutationKey,
    mutationFn: ({ task, to }: MoveVars) =>
      api.patch("/tasks/{id}/move", { path: { id: task.id }, body: { ...to, version: task.version } }),
    onSuccess: (moved, { position }) => {
      queryClient.setQueryData<BoardDetail>(queryKey, (b) => b && withTask(b, moved));
      // A different position means the column changed under us or was rebalanced.
      if (moved.position !== position) void queryClient.invalidateQueries({ queryKey });
    },
    onError: (err, { task }) => {
      queryClient.setQueryData<BoardDetail>(queryKey, (b) => b && withTask(b, isTaskConflict(err) ? err.current : task));
      const staleView =
        err instanceof ApiError &&
        err.status >= 400 &&
        err.status < 500 &&
        !(isTaskConflict(err) && err.code === "version_conflict");
      if (staleView) void queryClient.invalidateQueries({ queryKey });
    },
  });

  const pendingIds = useMutationState({
    filters: { mutationKey, status: "pending" },
    select: (m) => (m.state.variables as MoveVars | undefined)?.task.id,
  }).filter((id) => id !== undefined);

  const move = (task: Task, to: MoveTarget, options?: { onError?: (err: Error) => void }) => {
    const board = queryClient.getQueryData<BoardDetail>(queryKey);
    if (!board) return;
    // A refetch already on its way would put the task back. Without `revert: false`
    // cancelling restores the data from when that fetch began, undoing this move.
    void queryClient.cancelQueries({ queryKey }, { revert: false });
    const position = positionFor(board, task.id, to);
    queryClient.setQueryData(queryKey, withTask(board, { ...task, column_id: to.columnId, position }));
    // Not mutate(): its per-call callbacks only run for the latest call, and two
    // cards can be saving at once.
    mutation.mutateAsync({ task, to, position }).catch((err: Error) => options?.onError?.(err));
  };

  return { move, pendingIds };
}

/** The fields the task panel edits, as PATCH /tasks/{id} takes them. */
export type TaskFields = {
  title: string;
  description: string;
  priority: Priority;
  due_date: string | null;
  assignee_id: string | null;
  label_ids: string[];
};

type UpdateVars = { id: string; version: number; changes: Partial<TaskFields> };

/**
 * Saves edits with the version the client read. The board cache takes the result,
 * or, on a 409, the server's current task.
 */
export function useUpdateTask(boardId: string) {
  const queryClient = useQueryClient();
  const queryKey = boardKeys.detail(boardId);
  return useMutation({
    mutationKey: ["updateTask", boardId],
    mutationFn: ({ id, version, changes }: UpdateVars) =>
      api.patch("/tasks/{id}", { path: { id }, body: { ...changes, version } }),
    onSuccess: (task) => queryClient.setQueryData<BoardDetail>(queryKey, (b) => b && withTask(b, task)),
    onError: (err) => {
      if (isTaskConflict(err)) {
        queryClient.setQueryData<BoardDetail>(queryKey, (b) => b && withTask(b, err.current));
      } else if (err instanceof ApiError && (err.status === 403 || err.status === 404)) {
        // The role or the task changed; the board says how.
        void queryClient.invalidateQueries({ queryKey });
      }
    },
  });
}
