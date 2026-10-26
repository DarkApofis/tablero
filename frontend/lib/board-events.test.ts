import { describe, expect, it } from "vitest";
import { board, layout, task } from "@/test/helpers";
import { applyEvent, eventTouchesTask, type StreamEvent } from "./board-events";
import type { BoardDetail } from "./boards";

function event(type: string, payload: object, version: number | null = null): StreamEvent {
  return {
    id: 1,
    type: type as StreamEvent["type"],
    board_id: "b1",
    actor_id: "u2",
    version,
    payload: payload as StreamEvent["payload"],
    created_at: "2026-10-26T10:00:00Z",
  };
}

const names = (id: string) => ({ u9: "Nuevo" })[id as "u9"];

const start = () =>
  board([
    task("a", "todo", 1024, { version: 3 }),
    task("b", "todo", 2048, { assignee: { id: "u2", name: "Marcos" } }),
    task("c", "done", 1024),
  ]);

const apply = (b: BoardDetail, e: StreamEvent) => {
  const next = applyEvent(b, e, names);
  if (next === "refetch") throw new Error("unexpected refetch");
  return next;
};

describe("applyEvent", () => {
  describe("deduplication by version", () => {
    it("ignores a task event with the version the task already has", () => {
      const b = start();
      const e = event("task.updated", { task_id: "a", title: "Otro", version: 3, changes: { title: { from: "a", to: "Otro" } } }, 3);
      expect(applyEvent(b, e, names)).toBe(b);
    });

    it("ignores an older version, e.g. one our optimistic change already went past", () => {
      const b = start();
      const e = event("task.moved", { task_id: "a", title: "a", version: 2, from: {}, to: { column_id: "done", position: 1 } }, 2);
      expect(applyEvent(b, e, names)).toBe(b);
    });

    it("applies a newer version", () => {
      const e = event("task.updated", { task_id: "a", title: "Otro", version: 4, changes: { title: { from: "a", to: "Otro" } } }, 4);
      const t = apply(start(), e).columns[0].tasks[0];
      expect([t.title, t.version]).toEqual(["Otro", 4]);
    });

    it("does not add a created task twice", () => {
      const created = event(
        "task.created",
        { task_id: "n", title: "N", description: "", column_id: "done", position: 512, version: 1, priority: "high", due_date: null, assignee_id: null, label_ids: [] },
        1,
      );
      const once = apply(start(), created);
      expect(applyEvent(once, created, names)).toBe(once);
    });
  });

  it("inserts a created task by position, with its assignee and labels", () => {
    const e = event(
      "task.created",
      { task_id: "n", title: "N", description: "", column_id: "done", position: 512, version: 1, priority: "high", due_date: null, assignee_id: "u2", label_ids: ["l1"] },
      1,
    );
    const next = apply(start(), e);
    expect(layout(next)).toBe("todo: a, b | done: n, c");
    expect(next.columns[1].tasks[0].assignee).toEqual({ id: "u2", name: "Marcos" });
    expect(next.columns[1].tasks[0].labels.map((l) => l.name)).toEqual(["Bug"]);
  });

  it("applies each changed field of an update", () => {
    const e = event(
      "task.updated",
      {
        task_id: "a",
        title: "a",
        version: 4,
        changes: {
          priority: { from: "medium", to: "urgent" },
          due_date: { from: null, to: "2026-11-01" },
          assignee_id: { from: null, to: "u9" },
          label_ids: { from: [], to: ["l1"] },
        },
      },
      4,
    );
    const t = apply(start(), e).columns[0].tasks[0];
    expect([t.priority, t.due_date, t.assignee?.name, t.labels.map((l) => l.id)]).toEqual(["urgent", "2026-11-01", "Nuevo", ["l1"]]);
  });

  it("moves a task and applies the rebalanced positions without bumping their versions", () => {
    const e = event(
      "task.moved",
      {
        task_id: "b",
        title: "b",
        version: 2,
        from: { column_id: "todo", position: 2048 },
        to: { column_id: "done", position: 1500 },
        rebalanced: [{ task_id: "c", position: 2000 }, { task_id: "b", position: 1000 }],
      },
      2,
    );
    const next = apply(start(), e);
    expect(layout(next)).toBe("todo: a | done: b, c");
    expect(next.columns[1].tasks.map((t) => t.version)).toEqual([2, 1]);
  });

  it("removes deleted and archived tasks, and a deleted column with its tasks", () => {
    const deleted = apply(start(), event("task.deleted", { task_id: "b", title: "b", column_id: "todo", version: 1 }, 1));
    expect(layout(deleted)).toBe("todo: a | done: c");
    const archived = apply(
      start(),
      event("task.archived", { task_id: "a", title: "a", version: 4, changes: { archived_at: { from: null, to: "2026-10-26T10:00:00Z" } } }, 4),
    );
    expect(layout(archived)).toBe("todo: b | done: c");
    const column = apply(start(), event("column.deleted", { column_id: "todo", name: "Por hacer", deleted_task_ids: ["a", "b"] }));
    expect(layout(column)).toBe("done: c");
  });

  it("creates, renames and reorders columns", () => {
    let b = apply(start(), event("column.created", { column_id: "qa", name: "QA", position: 512 }));
    b = apply(b, event("column.updated", { column_id: "done", name: "Terminado", changes: { name: { from: "Hecho", to: "Terminado" } } }));
    b = apply(b, event("column.reordered", { column_id: "done", name: "Terminado", from: 2048, to: 100 }));
    expect(b.columns.map((c) => c.name)).toEqual(["Terminado", "QA", "Por hacer"]);
  });

  it("renames a label everywhere and removes a deleted one from its tasks", () => {
    const labeled = apply(start(), event("task.updated", { task_id: "c", title: "c", version: 2, changes: { label_ids: { from: [], to: ["l1"] } } }, 2));
    const renamed = apply(labeled, event("label.changed", { action: "updated", label: { id: "l1", name: "Error", color: "#ff0000" } }));
    expect(renamed.columns[1].tasks[0].labels[0].name).toBe("Error");
    const removed = apply(labeled, event("label.changed", { action: "deleted", label: { id: "l1", name: "Bug", color: "#e5484d" } }));
    expect([removed.labels, removed.columns[1].tasks[0].labels, removed.columns[1].tasks[0].version]).toEqual([[], [], 2]);
  });

  it("asks for a refetch when the event refers to something the board doesn't have", () => {
    const unknownLabel = event(
      "task.created",
      { task_id: "z", title: "Z", description: "", column_id: "todo", position: 1, version: 1, priority: "low", due_date: null, assignee_id: null, label_ids: ["nope"] },
      1,
    );
    expect(applyEvent(start(), unknownLabel, names)).toBe("refetch");
    const unknownTask = event("task.moved", { task_id: "zz", title: "?", version: 2, from: {}, to: { column_id: "todo", position: 1 } }, 2);
    expect(applyEvent(start(), unknownTask, names)).toBe("refetch");
  });

  it("ignores event types it doesn't know", () => {
    const b = start();
    expect(applyEvent(b, event("task.teleported", {}), names)).toBe(b);
  });
});

describe("eventTouchesTask", () => {
  it("covers the task's own events, rebalances that move it and column deletes that take it", () => {
    expect(eventTouchesTask(event("task.updated", { task_id: "a" }), "a")).toBe(true);
    expect(eventTouchesTask(event("task.moved", { task_id: "b", rebalanced: [{ task_id: "a", position: 1 }] }), "a")).toBe(true);
    expect(eventTouchesTask(event("column.deleted", { column_id: "todo", deleted_task_ids: ["a"] }), "a")).toBe(true);
    expect(eventTouchesTask(event("task.updated", { task_id: "b" }), "a")).toBe(false);
  });
});
