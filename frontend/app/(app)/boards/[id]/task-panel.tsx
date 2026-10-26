"use client";

import { useId } from "react";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { cx } from "@/components/ui/cx";
import { Notice } from "@/components/ui/notice";
import { Tag } from "@/components/ui/tag";
import { ApiError, type Task } from "@/lib/api";
import type { BoardDetail, Label, TaskFields } from "@/lib/boards";
import { describeDue, priorities, type Priority } from "@/lib/tasks";
import { useMembers, type Member } from "@/lib/workspaces";
import type { Toast } from "./board-toast";
import { CheckboxMenu } from "./checkbox-menu";
import { PanelFrame } from "./panel-frame";
import { PriorityBadge } from "./task-card";
import { changesBetween, fieldsOf, useTaskEditor, type Conflict, type FieldKey } from "./use-task-editor";

type Props = {
  board: BoardDetail;
  taskId: string;
  onClose: () => void;
  notify: (toast: Toast) => void;
};

const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";
const fieldNames: Record<FieldKey, string> = {
  title: "Título",
  description: "Descripción",
  priority: "Prioridad",
  due_date: "Fecha límite",
  assignee_id: "Responsable",
  label_ids: "Etiquetas",
};
const longDate = new Intl.DateTimeFormat("es", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });

export function TaskPanel({ board, taskId, onClose, notify }: Props) {
  const task = board.columns.flatMap((c) => c.tasks).find((t) => t.id === taskId);
  const headingId = useId();

  if (!task) {
    return (
      <PanelFrame label="Tarea no encontrada" onClose={onClose} status={null}>
        <h2 id={headingId} className="text-lg font-semibold">
          Esta tarea ya no existe
        </h2>
        <p className="text-sm text-muted">Puede que la hayan borrado o archivado.</p>
      </PanelFrame>
    );
  }
  // A new task starts a new draft; the old one saves what it had on the way out.
  return <TaskEditor key={task.id} board={board} task={task} onClose={onClose} notify={notify} />;
}

function TaskEditor({ board, task, onClose, notify }: { board: BoardDetail; task: Task; onClose: () => void; notify: (toast: Toast) => void }) {
  const editable = board.role !== "viewer";
  const members = useMembers(board.workspace_id);
  const editor = useTaskEditor(task, board.id, {
    editable,
    onLostChanges: (title) =>
      notify({
        tone: "error",
        title: `No se guardaron tus últimos cambios en «${title}»`,
        text: "Alguien la cambió a la vez. Ábrela para ver cómo quedó.",
      }),
  });
  const { draft, set, fieldErrors } = editor;
  const ids = { title: useId(), description: useId(), priority: useId(), assignee: useId(), due: useId() };
  const column = board.columns.find((c) => c.id === task.column_id);

  const status = !editable
    ? "Solo lectura"
    : {
        conflict: "Sin guardar",
        error: "No se guardó",
        saving: "Guardando…",
        pending: "Sin guardar",
        saved: "✓ Guardado",
        idle: "",
      }[editor.status];

  const names = (labelIds: string[]) => board.labels.filter((l) => labelIds.includes(l.id));
  const due = draft.due_date ? describeDue(draft.due_date) : null;

  return (
    <PanelFrame
      label={`Tarea: ${draft.title.trim() || task.title}`}
      onClose={onClose}
      chip={column?.name}
      status={
        <span
          className={cx(
            "text-xs",
            !editable ? "text-muted" : editor.status === "error" || editor.status === "conflict" ? "text-warn" : "text-accent-text",
          )}
        >
          {status}
        </span>
      }
      // Announced only when it settles, not on every keystroke.
      live={editor.status === "saved" ? "Cambios guardados" : editor.status === "error" ? "No se guardaron los cambios" : ""}
    >
      {editor.conflict && (
        <ConflictNotice
          conflict={editor.conflict}
          draft={draft}
          board={board}
          members={members.data ?? []}
          onTakeServer={editor.takeServerVersion}
          onReapply={editor.reapplyMine}
        />
      )}
      {editor.failure && (
        <Notice
          tone="error"
          compact
          title="No pudimos guardar los cambios"
          actions={
            canRetry(editor.failure) && (
              <Button size="sm" variant="primary" onClick={editor.retry}>
                Reintentar
              </Button>
            )
          }
        >
          {describeSaveError(editor.failure)}
        </Notice>
      )}

      {editable ? (
        <div className="flex flex-col gap-1">
          <input
            id={ids.title}
            aria-label="Título de la tarea"
            value={draft.title}
            maxLength={200}
            onChange={(e) => set("title", e.target.value)}
            aria-invalid={editor.titleError || fieldErrors.title ? true : undefined}
            aria-describedby={editor.titleError || fieldErrors.title ? `${ids.title}-error` : undefined}
            className="-mx-2.5 w-[calc(100%+20px)] rounded-[10px] border border-transparent bg-transparent px-2.5 py-1.5 text-[21px] font-semibold tracking-[-.015em] hover:border-border focus:border-accent-text focus:bg-surface2 focus:outline-none aria-invalid:border-danger"
          />
          <FieldError id={`${ids.title}-error`} message={editor.titleError ?? fieldErrors.title} />
        </div>
      ) : (
        <h2 className="break-words text-[21px] font-semibold tracking-[-.015em]">{task.title}</h2>
      )}

      <div className="grid grid-cols-[100px_minmax(0,1fr)] items-center gap-y-3.5 text-[13.5px]">
        {/* Prioridad */}
        {editable ? (
          <>
            <span id={ids.priority} className="text-muted">
              Prioridad
            </span>
            <div role="radiogroup" aria-labelledby={ids.priority} className="flex flex-wrap gap-1">
              {(Object.keys(priorities) as Priority[]).map((p) => (
                <label
                  key={p}
                  className="flex h-7 cursor-pointer items-center rounded-lg border border-border px-1.5 text-xs has-[:checked]:border-accent-text/60 has-[:checked]:bg-accent-soft has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-accent-text"
                >
                  <input
                    type="radio"
                    name={`priority-${task.id}`}
                    value={p}
                    checked={draft.priority === p}
                    onChange={() => set("priority", p)}
                    className="sr-only"
                  />
                  <PriorityBadge priority={p} />
                </label>
              ))}
            </div>
          </>
        ) : (
          <>
            <span className="text-muted">Prioridad</span>
            <span className="text-xs">
              <PriorityBadge priority={task.priority} />
            </span>
          </>
        )}

        {/* Responsable */}
        {editable ? (
          <label htmlFor={ids.assignee} className="text-muted">
            Responsable
          </label>
        ) : (
          <span className="text-muted">Responsable</span>
        )}
        <div className="flex min-w-0 items-center gap-2">
          {draft.assignee_id ? (
            <Avatar name={assigneeName(draft.assignee_id, task, members.data) ?? "?"} size={24} />
          ) : (
            <span aria-hidden className="size-6 shrink-0 rounded-full border-[1.5px] border-dashed border-border-strong" />
          )}
          {editable ? (
            <select
              id={ids.assignee}
              value={draft.assignee_id ?? ""}
              onChange={(e) => set("assignee_id", e.target.value || null)}
              aria-invalid={fieldErrors.assignee_id ? true : undefined}
              aria-describedby={fieldErrors.assignee_id ? `${ids.assignee}-error` : undefined}
              className={cx(
                "h-8 min-w-0 flex-1 rounded-lg border border-control-border bg-surface2 px-2 text-[13.5px] aria-invalid:border-danger",
                focusRing,
              )}
            >
              <option value="">Sin responsable</option>
              {assigneeOptions(draft.assignee_id, task, members.data).map((o) => (
                <option key={o.id} value={o.id} disabled={o.gone}>
                  {o.gone ? `${o.name} (ya no es miembro)` : o.name}
                </option>
              ))}
            </select>
          ) : (
            <span className="truncate">{task.assignee?.name ?? "Sin responsable"}</span>
          )}
        </div>
        {editable && fieldErrors.assignee_id && (
          <FieldError id={`${ids.assignee}-error`} message={fieldErrors.assignee_id} className="col-start-2" />
        )}
        {editable && members.isError && (
          <p className="col-start-2 text-xs text-muted">No pudimos cargar los miembros del workspace.</p>
        )}

        {/* Fecha límite */}
        {editable ? (
          <label htmlFor={ids.due} className="text-muted">
            Fecha límite
          </label>
        ) : (
          <span className="text-muted">Fecha límite</span>
        )}
        <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
          {editable && (
            <input
              id={ids.due}
              type="date"
              value={draft.due_date ?? ""}
              onChange={(e) => set("due_date", e.target.value || null)}
              aria-describedby={due ? `${ids.due}-state` : undefined}
              className={cx("h-8 rounded-lg border border-control-border bg-surface2 px-2 text-[13.5px]", focusRing)}
            />
          )}
          {due ? (
            <span
              id={`${ids.due}-state`}
              className={cx(
                due.tone === "late" ? "font-semibold text-danger" : due.tone === "soon" ? "font-semibold text-warn" : "",
                editable && due.tone === "normal" && "sr-only",
              )}
            >
              {due.text}
              {!editable && due.tone !== "normal" && ` · ${longDate.format(Date.parse(draft.due_date!))}`}
            </span>
          ) : (
            !editable && <span className="text-muted">Sin fecha</span>
          )}
          {editable && draft.due_date && (
            <button
              type="button"
              onClick={() => set("due_date", null)}
              className={cx("rounded text-xs text-muted underline-offset-2 hover:text-text hover:underline", focusRing)}
            >
              Quitar fecha
            </button>
          )}
        </div>

        {/* Etiquetas */}
        <span className="text-muted">Etiquetas</span>
        <div className="flex flex-wrap items-center gap-1.5">
          {names(draft.label_ids).map((l) => (
            <Tag key={l.id} label={l.name} color={l.color} />
          ))}
          {!editable && draft.label_ids.length === 0 && <span className="text-muted">Sin etiquetas</span>}
          {editable && (
            <CheckboxMenu
              variant="add"
              label={draft.label_ids.length === 0 ? "+ Etiqueta" : "Editar"}
              legend="Etiquetas de la tarea"
              options={board.labels.map((l) => ({
                value: l.id,
                label: l.name,
                icon: <span aria-hidden className="size-2.5 shrink-0 rounded-full" style={{ background: l.color }} />,
              }))}
              selected={draft.label_ids}
              onChange={(ids) => set("label_ids", [...ids].sort())}
              emptyText="Este tablero no tiene etiquetas."
            />
          )}
        </div>
        {editable && fieldErrors.label_ids && <FieldError message={fieldErrors.label_ids} className="col-start-2" />}
      </div>

      <div className="flex flex-col gap-2">
        {editable ? (
          <>
            <label htmlFor={ids.description} className="text-[12.5px] font-semibold text-muted">
              Descripción
            </label>
            <textarea
              id={ids.description}
              rows={6}
              maxLength={10000}
              value={draft.description}
              onChange={(e) => set("description", e.target.value)}
              placeholder="Añade detalles, criterios de aceptación o enlaces."
              aria-invalid={fieldErrors.description ? true : undefined}
              className={cx(
                "w-full resize-y rounded-[10px] border border-control-border bg-surface2 px-3 py-2.5 text-sm leading-[1.55] placeholder:text-faint aria-invalid:border-danger",
                "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text",
              )}
            />
            <FieldError message={fieldErrors.description} />
          </>
        ) : (
          <>
            <h3 className="text-[12.5px] font-semibold text-muted">Descripción</h3>
            <p className="whitespace-pre-wrap break-words text-sm leading-[1.55]">
              {task.description || <span className="text-muted">Sin descripción.</span>}
            </p>
          </>
        )}
      </div>
    </PanelFrame>
  );
}

function ConflictNotice({
  conflict,
  draft,
  board,
  members,
  onTakeServer,
  onReapply,
}: {
  conflict: Conflict;
  draft: TaskFields;
  board: BoardDetail;
  members: Member[];
  onTakeServer: () => void;
  onReapply: () => void;
}) {
  const before = fieldsOf(conflict.base);
  const server = fieldsOf(conflict.server);
  const theirs = Object.keys(changesBetween(before, server)) as FieldKey[];
  const mine = changesBetween(before, draft);
  const mineKeys = Object.keys(mine) as FieldKey[];
  const show = (key: FieldKey, fields: TaskFields) => describeValue(key, fields, board.labels, conflict.server, members);

  return (
    <>
      <Notice
        tone="conflict"
        title="La tarea cambió mientras editabas"
        actions={
          <>
            <Button size="sm" variant="secondary" onClick={onTakeServer}>
              Usar la versión del servidor
            </Button>
            <Button size="sm" variant="primary" onClick={onReapply}>
              Reaplicar mis cambios
            </Button>
          </>
        }
      >
        Alguien guardó cambios en {list(theirs.map((k) => fieldNames[k].toLowerCase()))}.
        {mineKeys.length > 0 && <> Tus cambios en {list(mineKeys.map((k) => fieldNames[k].toLowerCase()))} siguen aquí, sin guardar.</>}{" "}
        «Reaplicar» los guarda sobre la versión nueva.
      </Notice>
      <div className="flex flex-col gap-2.5">
        {theirs.map((key) => (
          <div
            key={key}
            className="overflow-hidden rounded-xl border border-[color-mix(in_oklab,var(--warn)_45%,transparent)] text-[13px] leading-[1.55]"
          >
            <div className="bg-warn-soft px-3 py-2 text-xs font-semibold text-warn">{fieldNames[key]} en el servidor</div>
            <div className="bg-danger-soft px-3 py-2 text-muted line-through">
              <span className="sr-only">Antes: </span>
              <span className="whitespace-pre-wrap break-words text-text">− {show(key, before)}</span>
            </div>
            <div className="bg-accent-soft px-3 py-2">
              <span className="sr-only">Ahora: </span>
              <span className="whitespace-pre-wrap break-words">+ {show(key, server)}</span>
            </div>
            {key in mine && (
              <div className="border-t border-border px-3 py-2 text-muted">
                Tu cambio: <span className="whitespace-pre-wrap break-words text-text">{show(key, draft)}</span>
              </div>
            )}
          </div>
        ))}
      </div>
    </>
  );
}

function FieldError({ id, message, className }: { id?: string; message?: string | null; className?: string }) {
  if (!message) return null;
  return (
    <p id={id} className={cx("text-xs text-danger", className)}>
      {message}
    </p>
  );
}

function describeValue(key: FieldKey, fields: TaskFields, labels: Label[], server: Task, members: Member[]) {
  switch (key) {
    case "title":
      return fields.title.trim();
    case "description":
      return fields.description || "(vacía)";
    case "priority":
      return priorities[fields.priority].label;
    case "due_date":
      return fields.due_date ? longDate.format(Date.parse(fields.due_date)) : "Sin fecha";
    case "assignee_id":
      return fields.assignee_id ? (assigneeName(fields.assignee_id, server, members) ?? "Alguien") : "Sin responsable";
    case "label_ids": {
      const names = labels.filter((l) => fields.label_ids.includes(l.id)).map((l) => l.name);
      return names.length > 0 ? names.join(", ") : "Sin etiquetas";
    }
  }
}

function assigneeName(id: string, task: Task, members: Member[] | undefined) {
  return members?.find((m) => m.user_id === id)?.name ?? (task.assignee?.id === id ? task.assignee.name : undefined);
}

/** Workspace members by name; a current assignee who left is kept, but can't be picked again. */
function assigneeOptions(current: string | null, task: Task, members: Member[] | undefined) {
  const options = (members ?? [])
    .map((m) => ({ id: m.user_id, name: m.name, gone: false }))
    .sort((a, b) => a.name.localeCompare(b.name, "es"));
  if (current && !options.some((o) => o.id === current)) {
    const name = assigneeName(current, task, members);
    // While members load, the assignee is simply listed.
    options.unshift({ id: current, name: name ?? "Responsable actual", gone: members !== undefined });
  }
  return options;
}

function list(items: string[]) {
  return items.length <= 1 ? (items[0] ?? "") : `${items.slice(0, -1).join(", ")} y ${items.at(-1)}`;
}

function canRetry(err: Error) {
  return !(err instanceof ApiError) || ![403, 404, 422].includes(err.status);
}

function describeSaveError(err: Error) {
  if (!(err instanceof ApiError)) return "Algo salió mal. Inténtalo de nuevo.";
  if (err.code === "network_error") return err.message;
  if (err.status === 403) return "Tu rol ya no permite editar tareas.";
  if (err.status === 404) return "Esta tarea ya no existe.";
  if (err.status === 422) return "Revisa los campos marcados.";
  return "Algo salió mal. Inténtalo de nuevo.";
}
