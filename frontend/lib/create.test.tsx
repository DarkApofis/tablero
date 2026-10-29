import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CreateForm } from "@/components/ui/create-form";
import { applyEvent } from "@/lib/board-events";
import { board, layout, newQueryClient, stubFetch, task, withClient } from "@/test/helpers";
import { boardKeys, useCreateColumn, useCreateTask, type BoardDetail } from "./boards";
import { ApiError } from "./api";

describe("CreateForm", () => {
  it("asks for a name, creates, and clears for the next one", async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(<CreateForm label="Nombre del tablero" submitLabel="Crear" onCreate={onCreate} />);
    const field = screen.getByLabelText("Nombre del tablero");

    await userEvent.click(screen.getByRole("button", { name: "Crear" }));
    expect(await screen.findByText("Escribe un nombre")).toBeVisible();
    expect(onCreate).not.toHaveBeenCalled();

    await userEvent.type(field, "  Sprint 1  ");
    await userEvent.click(screen.getByRole("button", { name: "Crear" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith("Sprint 1"));
    await waitFor(() => expect(field).toHaveValue(""));
    expect(field).toHaveFocus();
  });

  it("shows why the API refused, and keeps what was typed", async () => {
    const limit = new ApiError({ status: 409, code: "workspace_limit", message: "Ya creaste 2 workspaces, el máximo por persona" });
    render(<CreateForm label="Nombre del workspace" submitLabel="Crear" onCreate={() => Promise.reject(limit)} />);

    await userEvent.type(screen.getByLabelText("Nombre del workspace"), "Tercero");
    await userEvent.click(screen.getByRole("button", { name: "Crear" }));

    expect(await screen.findByText("Ya creaste 2 workspaces, el máximo por persona")).toBeVisible();
    expect(screen.getByLabelText("Nombre del workspace")).toHaveValue("Tercero");
    expect(screen.getByLabelText("Nombre del workspace")).toHaveAttribute("aria-invalid", "true");
  });

  it("closes with Escape when it can be cancelled", async () => {
    const onCancel = vi.fn();
    render(<CreateForm label="Título" submitLabel="Añadir" onCreate={vi.fn()} onCancel={onCancel} />);
    await userEvent.type(screen.getByLabelText("Título"), "a{Escape}");
    expect(onCancel).toHaveBeenCalled();
  });
});

function setup() {
  const queryClient = newQueryClient();
  queryClient.setQueryData(boardKeys.detail("b1"), board([task("a", "todo", 1024)]));
  return { cached: () => queryClient.getQueryData<BoardDetail>(boardKeys.detail("b1")), wrapper: withClient(queryClient) };
}

describe("creating on the board", () => {
  it("puts a new task at the end of its column, once even when its event arrives", async () => {
    const created = task("n", "todo", 2048, { title: "Nueva" });
    const calls = stubFetch({ "POST /columns/todo/tasks": () => ({ status: 201, body: created }) });
    const { cached, wrapper } = setup();
    const { result } = renderHook(() => useCreateTask("b1"), { wrapper });

    await act(() => result.current.mutateAsync({ columnId: "todo", title: "Nueva" }));

    expect(calls[0].body).toEqual({ title: "Nueva" });
    expect(layout(cached())).toBe("todo: a, n | done: ");
    // The stream then delivers task.created for it: nothing changes.
    const event = {
      id: 1, type: "task.created", board_id: "b1", actor_id: "u1", version: 1, created_at: "2026-10-29T00:00:00Z",
      payload: { task_id: "n", title: "Nueva", description: "", column_id: "todo", position: 2048, version: 1,
        priority: "medium", due_date: null, assignee_id: null, label_ids: [] },
    } as const;
    const next = applyEvent(cached()!, event as never, () => undefined);
    expect(next === "refetch" ? next : layout(next)).toBe("todo: a, n | done: ");
  });

  it("adds a column at the end, once", async () => {
    const column = { id: "review", board_id: "b1", name: "Revisión", position: 3000, created_at: "2026-10-29T00:00:00Z" };
    stubFetch({ "POST /boards/b1/columns": () => ({ status: 201, body: column }) });
    const { cached, wrapper } = setup();
    const { result } = renderHook(() => useCreateColumn("b1"), { wrapper });

    await act(() => result.current.mutateAsync("Revisión"));
    await act(() => result.current.mutateAsync("Revisión")); // the same column again (as its event would)

    expect(cached()!.columns.map((c) => c.name)).toEqual(["Por hacer", "Hecho", "Revisión"]);
    expect(cached()!.columns[2].tasks).toEqual([]);
  });
});
