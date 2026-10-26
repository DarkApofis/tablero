import type { Schemas, Task } from "./api";
import { withTask, type BoardDetail, type BoardColumn } from "./boards";
import type { Priority } from "./tasks";

// Applies the board stream's domain events (docs/EVENTS.md) to a loaded board.

export type StreamEvent = Schemas["StreamEvent"];
export type PresenceUser = Schemas["PresenceUser"];

type TaskCreated = Schemas["TaskCreatedPayload"];
type TaskUpdated = Schemas["TaskUpdatedPayload"];
type TaskMoved = Schemas["TaskMovedPayload"];
type TaskDeleted = Schemas["TaskDeletedPayload"];
type ColumnCreated = Schemas["ColumnCreatedPayload"];
type ColumnUpdated = Schemas["ColumnUpdatedPayload"];
type ColumnReordered = Schemas["ColumnReorderedPayload"];
type ColumnDeleted = Schemas["ColumnDeletedPayload"];
type LabelChanged = Schemas["LabelChangedPayload"];

export const DOMAIN_EVENTS = [
  "task.created",
  "task.updated",
  "task.archived",
  "task.moved",
  "task.deleted",
  "column.created",
  "column.updated",
  "column.reordered",
  "column.deleted",
  "label.changed",
] as const;

/** Names the stream's events don't carry, e.g. a new assignee's. */
export type NameLookup = (userId: string) => string | undefined;

/**
 * The board after `event`: the same object when the event changes nothing (already
 * reflected, or unknown), or "refetch" when it refers to something this board doesn't
 * have and only a fresh GET can fill in (a task we never saw, an unknown label).
 *
 * Task events carry the task's version after the change. A task that already has that
 * version or a newer one is left alone: the event is already reflected, maybe by an
 * optimistic change of our own.
 */
export function applyEvent(board: BoardDetail, event: StreamEvent, nameOf: NameLookup): BoardDetail | "refetch" {
  switch (event.type) {
    case "task.created": {
      const p = event.payload as TaskCreated;
      if (findTask(board, p.task_id)) return board;
      if (!board.columns.some((c) => c.id === p.column_id)) return "refetch";
      const labels = labelsOf(board, p.label_ids);
      const assignee = p.assignee_id ? person(p.assignee_id, board, nameOf) : null;
      if (!labels || assignee === undefined) return "refetch";
      return withTask(board, {
        id: p.task_id,
        column_id: p.column_id,
        title: p.title,
        description: p.description,
        position: p.position,
        version: p.version,
        priority: p.priority,
        due_date: p.due_date,
        assignee,
        labels,
        archived_at: null,
        created_by: event.actor_id ?? "",
        created_at: event.created_at,
        updated_at: event.created_at,
      } as Task);
    }

    case "task.updated":
    case "task.archived": {
      const p = event.payload as TaskUpdated;
      const changes = p.changes;
      const task = findTask(board, p.task_id);
      // Archived tasks aren't loaded: editing one changes nothing here, but
      // unarchiving one brings back a task only a GET can fill in.
      if (!task) return changes.archived_at?.to ? board : "refetch";
      if (isReflected(task, event)) return board;
      if (changes.archived_at?.to) return withoutTasks(board, [task.id]);

      const next: Task = { ...task, title: p.title, version: event.version ?? task.version, updated_at: event.created_at };
      if (changes.description) next.description = changes.description.to;
      if (changes.priority) next.priority = changes.priority.to as Priority;
      if (changes.due_date) next.due_date = changes.due_date.to;
      if (changes.archived_at) next.archived_at = changes.archived_at.to;
      if (changes.assignee_id) {
        const id = changes.assignee_id.to;
        const assignee = id ? person(id, board, nameOf) : null;
        if (assignee === undefined) return "refetch";
        next.assignee = assignee;
      }
      if (changes.label_ids) {
        const labels = labelsOf(board, changes.label_ids.to);
        if (!labels) return "refetch";
        next.labels = labels;
      }
      return withTask(board, next);
    }

    case "task.moved": {
      const p = event.payload as TaskMoved;
      const task = findTask(board, p.task_id);
      if (!task) return "refetch";
      if (isReflected(task, event)) return board;
      if (!board.columns.some((c) => c.id === p.to.column_id)) return "refetch";
      const moved = withTask(board, {
        ...task,
        column_id: p.to.column_id,
        position: p.to.position,
        version: event.version ?? task.version,
      });
      // Rebalancing moves the other tasks of the column without a version change.
      return p.rebalanced ? withTaskPositions(moved, p.rebalanced) : moved;
    }

    case "task.deleted": {
      const p = event.payload as TaskDeleted;
      return findTask(board, p.task_id) ? withoutTasks(board, [p.task_id]) : board;
    }

    case "column.created": {
      const p = event.payload as ColumnCreated;
      if (board.columns.some((c) => c.id === p.column_id)) return board;
      const column: BoardColumn = {
        id: p.column_id,
        board_id: board.id,
        name: p.name,
        position: p.position,
        created_at: event.created_at,
        tasks: [],
      };
      return { ...board, columns: [...board.columns, column].sort(byPosition) };
    }

    case "column.updated": {
      const p = event.payload as ColumnUpdated;
      return mapColumns(board, (c) => (c.id === p.column_id && c.name !== p.name ? { ...c, name: p.name } : c));
    }

    case "column.reordered": {
      const p = event.payload as ColumnReordered;
      const positions = new Map((p.rebalanced ?? []).map((r) => [r.column_id, r.position]));
      positions.set(p.column_id, p.to);
      const columns = board.columns.map((c) =>
        positions.has(c.id) && positions.get(c.id) !== c.position ? { ...c, position: positions.get(c.id)! } : c,
      );
      return { ...board, columns: columns.sort(byPosition) };
    }

    case "column.deleted": {
      const p = event.payload as ColumnDeleted;
      if (!board.columns.some((c) => c.id === p.column_id)) return board;
      // Its tasks go with it; they get no task.deleted of their own.
      return { ...board, columns: board.columns.filter((c) => c.id !== p.column_id) };
    }

    case "label.changed": {
      const p = event.payload as LabelChanged;
      const label = { ...p.label, board_id: board.id, created_at: event.created_at };
      const existing = board.labels.find((l) => l.id === label.id);
      if (p.action === "deleted") {
        if (!existing) return board;
        // Removing a label from its tasks doesn't change their versions.
        return mapTasks(
          { ...board, labels: board.labels.filter((l) => l.id !== label.id) },
          (t) => (t.labels.some((l) => l.id === label.id) ? { ...t, labels: t.labels.filter((l) => l.id !== label.id) } : t),
        );
      }
      const merged = existing ? { ...existing, name: label.name, color: label.color } : label;
      const labels = [...board.labels.filter((l) => l.id !== label.id), merged].sort(byName);
      return mapTasks({ ...board, labels }, (t) =>
        t.labels.some((l) => l.id === label.id)
          ? { ...t, labels: t.labels.map((l) => (l.id === label.id ? merged : l)).sort(byName) }
          : t,
      );
    }

    default:
      // A type from a newer API: ignore it (docs/EVENTS.md).
      return board;
  }
}

/** Whether `event` concerns `taskId`: its own task events, or a column delete that takes it. */
export function eventTouchesTask(event: StreamEvent, taskId: string) {
  const p = event.payload as Partial<TaskMoved & ColumnDeleted>;
  return (
    p.task_id === taskId ||
    (p.rebalanced as { task_id?: string }[] | undefined)?.some((r) => r.task_id === taskId) === true ||
    p.deleted_task_ids?.includes(taskId) === true
  );
}

function isReflected(task: Task, event: StreamEvent) {
  return event.version != null && event.version <= task.version;
}

function findTask(board: BoardDetail, id: string) {
  for (const c of board.columns) {
    const t = c.tasks.find((t) => t.id === id);
    if (t) return t;
  }
  return undefined;
}

/** The labels for these ids, by name; undefined if one isn't on the board. */
function labelsOf(board: BoardDetail, ids: string[]) {
  const labels = ids.map((id) => board.labels.find((l) => l.id === id));
  return labels.every((l) => l !== undefined) ? labels.sort(byName) : undefined;
}

/** An assignee for the user id; undefined if no name is known for it. */
function person(id: string, board: BoardDetail, nameOf: NameLookup) {
  const known = board.columns.flatMap((c) => c.tasks).find((t) => t.assignee?.id === id)?.assignee;
  const name = known?.name ?? nameOf(id);
  return name === undefined ? undefined : { id, name };
}

function withoutTasks(board: BoardDetail, ids: string[]): BoardDetail {
  return mapColumns(board, (c) =>
    c.tasks.some((t) => ids.includes(t.id)) ? { ...c, tasks: c.tasks.filter((t) => !ids.includes(t.id)) } : c,
  );
}

function withTaskPositions(board: BoardDetail, positions: { task_id: string; position: number }[]): BoardDetail {
  const byId = new Map(positions.map((p) => [p.task_id, p.position]));
  return mapColumns(board, (c) =>
    c.tasks.some((t) => byId.has(t.id) && byId.get(t.id) !== t.position)
      ? {
          ...c,
          tasks: c.tasks
            .map((t) => (byId.has(t.id) ? { ...t, position: byId.get(t.id)! } : t))
            .sort((a, b) => a.position - b.position || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)),
        }
      : c,
  );
}

function mapColumns(board: BoardDetail, fn: (c: BoardColumn) => BoardColumn): BoardDetail {
  const columns = board.columns.map(fn);
  return columns.every((c, i) => c === board.columns[i]) ? board : { ...board, columns };
}

function mapTasks(board: BoardDetail, fn: (t: Task) => Task): BoardDetail {
  return mapColumns(board, (c) => {
    const tasks = c.tasks.map(fn);
    return tasks.every((t, i) => t === c.tasks[i]) ? c : { ...c, tasks };
  });
}

function byPosition(a: { position: number; id: string }, b: { position: number; id: string }) {
  return a.position - b.position || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

function byName(a: { name: string }, b: { name: string }) {
  return a.name.localeCompare(b.name, "es");
}
