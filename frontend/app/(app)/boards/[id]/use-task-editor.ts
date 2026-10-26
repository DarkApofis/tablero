"use client";

import { useEffect, useEffectEvent, useRef, useState } from "react";
import { ApiError, isTaskConflict, type Task } from "@/lib/api";
import { useUpdateTask, type TaskFields } from "@/lib/boards";

const KEYS = ["title", "description", "priority", "due_date", "assignee_id", "label_ids"] as const;
export type FieldKey = (typeof KEYS)[number];

/** Typing waits this long for a pause; picking a value saves right away. */
const TYPING_DELAY_MS = 800;

export function fieldsOf(task: Task): TaskFields {
  return {
    title: task.title,
    description: task.description,
    priority: task.priority,
    due_date: task.due_date,
    assignee_id: task.assignee?.id ?? null,
    label_ids: task.labels.map((l) => l.id).sort(),
  };
}

function equal<K extends FieldKey>(key: K, a: TaskFields[K], b: TaskFields[K]) {
  if (key === "label_ids") {
    const x = a as string[];
    const y = b as string[];
    return x.length === y.length && x.every((v, i) => v === y[i]);
  }
  // The server trims the title, so spaces at the ends aren't a change.
  if (key === "title") return (a as string).trim() === (b as string).trim();
  return a === b;
}

/** The fields of `to` that differ from `from`. */
export function changesBetween(from: TaskFields, to: TaskFields): Partial<TaskFields> {
  const out: Partial<TaskFields> = {};
  for (const key of KEYS) {
    if (!equal(key, from[key], to[key])) Object.assign(out, { [key]: to[key] });
  }
  return out;
}

const isEmpty = (changes: Partial<TaskFields>) => Object.keys(changes).length === 0;

export type Conflict = {
  /** The version the unsaved edits were made on. */
  base: Task;
  /** The task as the server has it now. */
  server: Task;
};

export type SaveError = { error: Error; changesKey: string };

/**
 * The task panel's editing state. Edits go to a local draft; after a pause they
 * are saved with the version the draft is based on. Only one save is in flight at
 * a time: edits made meanwhile go in the next one, on the version it returns.
 *
 * A 409 whose server task differs only where the panel doesn't edit (a move, say)
 * is retried on the new version. One that changed edited fields stops saving until
 * the user picks the server's version or reapplies their own changes on top of it.
 */
export function useTaskEditor(
  task: Task,
  boardId: string,
  { editable, onLostChanges }: { editable: boolean; onLostChanges: (title: string) => void },
) {
  const update = useUpdateTask(boardId);
  const [base, setBase] = useState(task);
  const [draft, setDraft] = useState(() => fieldsOf(task));
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [failure, setFailure] = useState<SaveError | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const delay = useRef(0);

  // Someone else's change reached the board (a refetch, or a move from this tab).
  const [seen, setSeen] = useState(task);
  if (task !== seen) {
    setSeen(task);
    if (!saving && !conflict && task.version > base.version) {
      const mine = changesBetween(fieldsOf(base), draft);
      if (isEmpty(mine)) {
        setBase(task);
        setDraft(fieldsOf(task));
      } else if (isEmpty(changesBetween(fieldsOf(base), fieldsOf(task)))) {
        setBase(task); // nothing the panel edits changed: keep editing on the new version
      }
      // Otherwise the next save gets the 409 and the user decides.
    }
  }

  const changes = changesBetween(fieldsOf(base), draft);
  const changesKey = JSON.stringify(changes);
  const titleError = draft.title.trim() === "" ? "Escribe un título." : null;
  const dirty = !isEmpty(changes);
  const canSave =
    editable && dirty && !conflict && !saving && !titleError && failure?.changesKey !== changesKey;

  const save = useEffectEvent(() => {
    const sentFrom = base;
    const key = changesKey;
    setSaving(true);
    setFailure(null);
    update.mutate(
      { id: task.id, version: sentFrom.version, changes: toBody(changes) },
      {
        onSuccess: (saved) => {
          setBase(saved);
          setSavedOnce(true);
        },
        onError: (error) => {
          if (isTaskConflict(error)) {
            if (isEmpty(changesBetween(fieldsOf(sentFrom), fieldsOf(error.current)))) {
              delay.current = 0;
              setBase(error.current); // retried by the effect below
            } else {
              setConflict({ base: sentFrom, server: error.current });
            }
          } else {
            setFailure({ error, changesKey: key });
          }
        },
        onSettled: () => setSaving(false),
      },
    );
  });

  useEffect(() => {
    if (!canSave) return;
    const timer = setTimeout(save, delay.current);
    return () => clearTimeout(timer);
  }, [canSave, changesKey]);

  // Closing the panel (or switching task) saves what is still waiting for a pause.
  const latest = useRef({ canSave, base, changes, title: draft.title });
  useEffect(() => {
    latest.current = { canSave, base, changes, title: draft.title };
  });
  useEffect(
    () => () => {
      const { canSave, base, changes, title } = latest.current;
      if (!canSave) return;
      update
        .mutateAsync({ id: base.id, version: base.version, changes: toBody(changes) })
        .catch((err: unknown) => {
          if (isTaskConflict(err)) onLostChanges(title.trim() || base.title);
        });
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on unmount
    [],
  );

  const set = <K extends FieldKey>(key: K, value: TaskFields[K]) => {
    delay.current = key === "title" || key === "description" ? TYPING_DELAY_MS : 0;
    setDraft((d) => ({ ...d, [key]: value }));
  };

  // A conflict is settled on the newest server task, even if one arrived since.
  const newest = (c: Conflict) => (task.version > c.server.version ? task : c.server);

  const takeServerVersion = () => {
    if (!conflict) return;
    const server = newest(conflict);
    setBase(server);
    setDraft(fieldsOf(server));
    setConflict(null);
  };

  const reapplyMine = () => {
    if (!conflict) return;
    const server = newest(conflict);
    const mine = changesBetween(fieldsOf(conflict.base), draft);
    delay.current = 0;
    setBase(server);
    setDraft({ ...fieldsOf(server), ...mine });
    setConflict(null);
  };

  const retry = () => {
    delay.current = 0;
    setFailure(null);
  };

  const status: "conflict" | "error" | "saving" | "pending" | "saved" | "idle" = conflict
    ? "conflict"
    : failure?.changesKey === changesKey
      ? "error"
      : saving
        ? "saving"
        : dirty
          ? "pending"
          : savedOnce
            ? "saved"
            : "idle";

  const currentFailure = failure?.changesKey === changesKey ? failure.error : null;

  return {
    draft,
    set,
    status,
    titleError,
    conflict,
    failure: currentFailure,
    fieldErrors: currentFailure instanceof ApiError ? (currentFailure.fields ?? {}) : {},
    takeServerVersion,
    reapplyMine,
    retry,
  };
}

function toBody(changes: Partial<TaskFields>): Partial<TaskFields> {
  return changes.title === undefined ? changes : { ...changes, title: changes.title.trim() };
}
