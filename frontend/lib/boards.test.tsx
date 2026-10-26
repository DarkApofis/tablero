import { act, renderHook, waitFor } from "@testing-library/react";
import type { InfiniteData } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { board, layout, newQueryClient, stubFetch, task, withClient } from "@/test/helpers";
import { activityKeys, addActivity, type ActivityItem, type ActivityPage } from "./activity";
import { isTaskConflict } from "./api";
import { boardKeys, positionFor, useMoveTask, useUpdateTask, withTask, type BoardDetail } from "./boards";

const key = boardKeys.detail("b1");

function setup(initial: BoardDetail) {
  const queryClient = newQueryClient();
  queryClient.setQueryData(key, initial);
  const cached = () => queryClient.getQueryData<BoardDetail>(key);
  return { queryClient, cached, wrapper: withClient(queryClient) };
}

const start = () => board([task("a", "todo", 1024, { version: 3 }), task("b", "todo", 2048), task("c", "done", 1024)]);

describe("useMoveTask", () => {
  it("moves the card in the cache at once, then keeps what the server returns", async () => {
    let answer!: (r: { status: number; body: unknown }) => void;
    const calls = stubFetch({ "PATCH /tasks/a/move": () => new Promise((r) => (answer = r)) });
    const { cached, wrapper } = setup(start());
    const { result } = renderHook(() => useMoveTask("b1"), { wrapper });
    const a = cached()!.columns[0].tasks[0];

    act(() => result.current.move(a, { columnId: "done", afterTaskId: "c", beforeTaskId: null }));

    // Optimistic, before the server answers: same rule as the server (after c: +1024).
    expect(layout(cached())).toBe("todo: b | done: c, a");
    expect(cached()!.columns[1].tasks[1].position).toBe(2048);
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].body).toEqual({ columnId: "done", afterTaskId: "c", beforeTaskId: null, version: 3 });
    await waitFor(() => expect(result.current.pendingIds).toEqual(["a"]));

    await act(async () => answer({ status: 200, body: { ...a, column_id: "done", position: 2048, version: 4 } }));
    await waitFor(() => expect(cached()!.columns[1].tasks[1].version).toBe(4));
    expect(layout(cached())).toBe("todo: b | done: c, a");
    expect(result.current.pendingIds).toEqual([]);
  });

  it("on a 409, reverts and puts the card where the server says it is now", async () => {
    // Someone else moved "a" to the top of "done" meanwhile.
    const current = task("a", "done", 0, { version: 4, title: "Movida por otra persona" });
    stubFetch({
      "PATCH /tasks/a/move": () => ({ status: 409, body: { error: { code: "version_conflict", message: "Cambió" }, current } }),
    });
    const { cached, wrapper } = setup(start());
    const { result } = renderHook(() => useMoveTask("b1"), { wrapper });
    const onError = vi.fn();

    act(() => result.current.move(cached()!.columns[0].tasks[0], { columnId: "todo", afterTaskId: "b", beforeTaskId: null }, { onError }));
    expect(layout(cached())).toBe("todo: b, a | done: c");

    await waitFor(() => expect(onError).toHaveBeenCalledOnce());
    expect(isTaskConflict(onError.mock.calls[0][0])).toBe(true);
    expect(layout(cached())).toBe("todo: b | done: a, c");
    expect(cached()!.columns[1].tasks[0]).toEqual(current);
  });

  it("on any other error, puts the card back where it was", async () => {
    stubFetch({ "PATCH /tasks/a/move": () => ({ status: 500, body: { error: { code: "internal_error", message: "boom" } } }) });
    const { cached, wrapper } = setup(start());
    const { result } = renderHook(() => useMoveTask("b1"), { wrapper });
    const onError = vi.fn();
    const before = cached()!.columns[0].tasks[0];

    act(() => result.current.move(before, { columnId: "done", afterTaskId: null, beforeTaskId: "c" }, { onError }));
    expect(layout(cached())).toBe("todo: b | done: a, c");

    await waitFor(() => expect(onError).toHaveBeenCalledOnce());
    expect(layout(cached())).toBe("todo: a, b | done: c");
    expect(cached()!.columns[0].tasks[0]).toEqual(before);
  });

  it("undoes only the move that failed when two are saving at once", async () => {
    stubFetch({
      "PATCH /tasks/a/move": () => ({ status: 500, body: { error: { code: "internal_error", message: "boom" } } }),
      "PATCH /tasks/b/move": async () => {
        await new Promise((r) => setTimeout(r, 30));
        return { status: 200, body: task("b", "done", 2048, { version: 2 }) };
      },
    });
    const { cached, wrapper } = setup(start());
    const { result } = renderHook(() => useMoveTask("b1"), { wrapper });
    const [a, b] = cached()!.columns[0].tasks;
    const errors = vi.fn();

    act(() => {
      result.current.move(b, { columnId: "done", afterTaskId: "c", beforeTaskId: null });
      result.current.move(a, { columnId: "done", afterTaskId: null, beforeTaskId: "c" }, { onError: errors });
    });

    await waitFor(() => expect(errors).toHaveBeenCalledOnce());
    await waitFor(() => expect(result.current.pendingIds).toEqual([]));
    expect(layout(cached())).toBe("todo: a | done: c, b");
  });
});

describe("useUpdateTask", () => {
  it("keeps the saved task, or the server's current one after a 409", async () => {
    const theirs = task("a", "todo", 1024, { version: 5, title: "De otra persona" });
    stubFetch({
      "PATCH /tasks/a": ({ body }) =>
        (body as { title?: string }).title === "Mía v4"
          ? { status: 200, body: task("a", "todo", 1024, { version: 4, title: "Mía v4" }) }
          : { status: 409, body: { error: { code: "version_conflict", message: "Cambió" }, current: theirs } },
    });
    const { cached, wrapper } = setup(start());
    const { result } = renderHook(() => useUpdateTask("b1"), { wrapper });

    await act(() => result.current.mutateAsync({ id: "a", version: 3, changes: { title: "Mía v4" } }));
    expect(cached()!.columns[0].tasks[0].title).toBe("Mía v4");

    await act(() => result.current.mutateAsync({ id: "a", version: 3, changes: { title: "Otra" } }).catch(() => {}));
    expect(cached()!.columns[0].tasks[0]).toEqual(theirs);
  });
});

describe("positionFor and withTask", () => {
  it("place a task with the server's rule: midpoint, or a step past the end", () => {
    const b = start();
    expect(positionFor(b, "c", { columnId: "todo", afterTaskId: "a", beforeTaskId: "b" })).toBe(1536);
    expect(positionFor(b, "c", { columnId: "todo", afterTaskId: "a", beforeTaskId: null })).toBe(1536);
    expect(positionFor(b, "c", { columnId: "todo", afterTaskId: null, beforeTaskId: null })).toBe(3072);
    expect(positionFor(b, "a", { columnId: "todo", afterTaskId: null, beforeTaskId: "b" })).toBe(1024);
    expect(layout(withTask(b, { ...b.columns[0].tasks[0], column_id: "done", position: 9999 }))).toBe("todo: b | done: c, a");
  });
});

describe("addActivity", () => {
  const item = (id: number): ActivityItem => ({
    id,
    type: "task_moved",
    actor: { id: "u2", name: "Marcos" },
    payload: { task_id: "a", title: "a", version: 2, from: { column_id: "todo", position: 1 }, to: { column_id: "done", position: 1 } },
    created_at: "2026-10-26T10:00:00Z",
  });

  it("puts a live entry on top once, and only if the feed was loaded", () => {
    const queryClient = newQueryClient();
    const feed = () => queryClient.getQueryData<InfiniteData<ActivityPage, string | null>>(activityKeys.board("b1"));

    addActivity(queryClient, "b1", item(9));
    expect(feed()).toBeUndefined();

    queryClient.setQueryData(activityKeys.board("b1"), {
      pages: [{ items: [item(8), item(7)], next_cursor: "7" }, { items: [item(6)], next_cursor: null }],
      pageParams: [null, "7"],
    });
    addActivity(queryClient, "b1", item(9));
    addActivity(queryClient, "b1", item(9));
    addActivity(queryClient, "b1", item(6));
    expect(feed()!.pages.map((p) => p.items.map((i) => i.id))).toEqual([[9, 8, 7], [6]]);
  });
});
