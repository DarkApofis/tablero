"use client";

import { useQuery } from "@tanstack/react-query";
import { api, type Schemas } from "./api";

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
