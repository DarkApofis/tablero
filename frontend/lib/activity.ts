"use client";

import { infiniteQueryOptions, useInfiniteQuery, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { api, type Schemas } from "./api";
import type { StreamEvent } from "./board-events";

export type ActivityItem = Schemas["ActivityItem"];
export type ActivityPage = Schemas["ActivityPage"];

const PAGE_SIZE = 30;

export const activityKeys = {
  board: (boardId: string) => ["boards", boardId, "activity"] as const,
};

/** The board's activity, newest first, a page at a time (cursor pagination). */
export const activityQuery = (boardId: string) =>
  infiniteQueryOptions({
    queryKey: activityKeys.board(boardId),
    queryFn: ({ pageParam, signal }) =>
      api.get("/boards/{id}/activity", {
        path: { id: boardId },
        query: { limit: PAGE_SIZE, ...(pageParam ? { cursor: pageParam } : {}) },
        signal,
      }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });

export function useActivity(boardId: string) {
  return useInfiniteQuery(activityQuery(boardId));
}

/** A stream event as the activity entry it was logged as (`task.moved` → `task_moved`). */
export function activityFromEvent(event: StreamEvent, nameOf: (userId: string) => string | undefined): ActivityItem {
  return {
    id: event.id,
    type: event.type.replace(".", "_") as ActivityItem["type"],
    actor: event.actor_id ? { id: event.actor_id, name: nameOf(event.actor_id) ?? "Alguien" } : null,
    payload: event.payload as ActivityItem["payload"],
    created_at: event.created_at,
  };
}

/**
 * Puts a live entry at the top of the loaded activity. Nothing to do if it was
 * never loaded (it is fetched fresh when opened) or the entry is already there.
 */
export function addActivity(queryClient: QueryClient, boardId: string, item: ActivityItem) {
  queryClient.setQueryData<InfiniteData<ActivityPage, string | null>>(activityKeys.board(boardId), (data) => {
    if (!data || data.pages.length === 0) return data;
    if (data.pages.some((p) => p.items.some((i) => i.id === item.id))) return data;
    const [first, ...rest] = data.pages;
    const items = [...first.items, item].sort((a, b) => b.id - a.id);
    return { ...data, pages: [{ ...first, items }, ...rest] };
  });
}
