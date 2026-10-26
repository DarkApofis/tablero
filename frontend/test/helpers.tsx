import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { vi } from "vitest";
import type { Task } from "@/lib/api";
import type { BoardDetail } from "@/lib/boards";

/** A recorded call to the stubbed fetch. */
export type Call = { method: string; path: string; body: unknown };

type Reply = { status: number; body?: unknown } | Promise<{ status: number; body?: unknown }>;

/**
 * Replaces fetch with routes keyed "METHOD /path" (e.g. "PATCH /tasks/t1/move").
 * Unknown routes answer 404, so a test fails loudly on a request it didn't expect.
 */
export function stubFetch(routes: Record<string, (call: Call) => Reply>) {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const url = new URL(input.toString());
      const method = (init.method ?? "GET").toUpperCase();
      const call = { method, path: url.pathname, body: init.body ? JSON.parse(String(init.body)) : undefined };
      calls.push(call);
      const route = routes[`${method} ${url.pathname}`];
      const reply = route ? await route(call) : { status: 404, body: { error: { code: "not_found", message: "No" } } };
      return new Response(reply.body === undefined ? null : JSON.stringify(reply.body), {
        status: reply.status,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  return calls;
}

export function newQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

export function withClient(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

export function task(id: string, columnId: string, position: number, extra: Partial<Task> = {}): Task {
  return {
    id,
    column_id: columnId,
    title: `Tarea ${id}`,
    description: "",
    position,
    version: 1,
    priority: "medium",
    due_date: null,
    assignee: null,
    labels: [],
    archived_at: null,
    created_by: "u1",
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...extra,
  };
}

/** Columns "todo" and "done" with these tasks, each placed by its column_id. */
export function board(tasks: Task[]): BoardDetail {
  const column = (id: string, name: string, position: number) => ({
    id,
    board_id: "b1",
    name,
    position,
    created_at: "2026-10-01T00:00:00Z",
    tasks: tasks.filter((t) => t.column_id === id).sort((a, b) => a.position - b.position),
  });
  return {
    id: "b1",
    workspace_id: "w1",
    name: "Tablero",
    created_at: "2026-10-01T00:00:00Z",
    role: "member",
    labels: [{ id: "l1", board_id: "b1", name: "Bug", color: "#e5484d", created_at: "2026-10-01T00:00:00Z" }],
    columns: [column("todo", "Por hacer", 1024), column("done", "Hecho", 2048)],
  };
}

/** "todo: a, b | done: c": where each task is, in order. */
export function layout(b: BoardDetail | undefined) {
  return b?.columns.map((c) => `${c.id}: ${c.tasks.map((t) => t.id).join(", ")}`).join(" | ");
}
