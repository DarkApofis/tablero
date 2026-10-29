"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import {
  closestCenter,
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { arrayMove, SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { cx } from "@/components/ui/cx";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Notice } from "@/components/ui/notice";
import { api, ApiError } from "@/lib/api";
import { useBoardMembers, useSetBoardMembers, useUpdateBoard } from "@/lib/board-admin";
import { boardKeys, type BoardDetail } from "@/lib/boards";
import { columnMoves } from "@/lib/column-order";
import { useMembers, type Visibility } from "@/lib/workspaces";
import { DeleteBoardDialog } from "../../workspaces/[id]/board-card";

/** A column as edited here: one of the board's (id) or a new one (key only). */
type Draft = { key: string; id?: string; name: string; tasks: number; deleted: boolean };

const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text";

function failure(err: unknown) {
  if (err instanceof ApiError && (err.status < 500 || err.code === "network_error")) return err.message;
  return "Algo salió mal. Inténtalo de nuevo en un momento.";
}

/**
 * "Ajustes del tablero" (design 2f), for admins: name, columns (rename,
 * remove, add, drag to reorder), who may open it, and archiving or deleting
 * it. Nothing changes until "Guardar".
 */
export function BoardSettingsDialog({ board, open, onClose }: { board: BoardDetail; open: boolean; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal open={open} busy={busy} width={560} title="Ajustes del tablero" onClose={onClose}>
      {open && <SettingsForm board={board} onClose={onClose} onBusy={setBusy} />}
    </Modal>
  );
}

function SettingsForm({ board, onClose, onBusy }: { board: BoardDetail; onClose: () => void; onBusy: (b: boolean) => void }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const update = useUpdateBoard(board.id);
  const members = useMembers(board.workspace_id);
  const boardMembers = useBoardMembers(board.id);
  const setMembers = useSetBoardMembers(board.id);
  const [name, setName] = useState(board.name);
  const [columns, setColumns] = useState<Draft[]>(() =>
    board.columns.map((c) => ({ key: c.id, id: c.id, name: c.name, tasks: c.tasks.length, deleted: false })),
  );
  const [visibility, setVisibility] = useState<Visibility>(board.visibility);
  const [given, setGiven] = useState<Set<string> | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const archived = board.archived_at !== null;
  const chosen = given ?? new Set((boardMembers.data ?? []).map((m) => m.user_id));
  // Admins see every board; the choice is about everyone else.
  const others = (members.data ?? []).filter((m) => m.role !== "admin");

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return;
    setColumns((cols) => {
      const from = cols.findIndex((c) => c.key === active.id);
      const to = cols.findIndex((c) => c.key === over.id);
      return arrayMove(cols, from, to);
    });
  };
  const patch = (key: string, change: Partial<Draft>) => setColumns((cols) => cols.map((c) => (c.key === key ? { ...c, ...change } : c)));

  /** Applies the changes one request at a time, stopping at the first that fails. */
  const save = async () => {
    const trimmed = name.trim();
    if (!trimmed) return setError("El tablero necesita un nombre");
    const kept = columns.filter((c) => !c.deleted);
    if (kept.some((c) => !c.name.trim())) return setError("Cada columna necesita un nombre");
    setError(null);
    setSaving(true);
    onBusy(true);
    try {
      if (trimmed !== board.name || visibility !== board.visibility) {
        await update.mutateAsync({
          ...(trimmed !== board.name && { name: trimmed }),
          ...(visibility !== board.visibility && { visibility }),
        });
      }
      if (visibility === "private" && given) await setMembers.mutateAsync([...given]);

      for (const c of columns.filter((c) => c.deleted && c.id)) {
        await api.delete("/columns/{id}", { path: { id: c.id! } });
      }
      const original = new Map(board.columns.map((c) => [c.id, c.name]));
      for (const c of kept.filter((c) => c.id && original.get(c.id) !== c.name.trim())) {
        await api.patch("/columns/{id}", { path: { id: c.id! }, body: { name: c.name.trim() } });
      }
      // New columns go at the end; then each goes where it was dragged.
      const ids = new Map<string, string>();
      for (const c of kept.filter((c) => !c.id)) {
        const created = await api.post("/boards/{id}/columns", { path: { id: board.id }, body: { name: c.name.trim() } });
        ids.set(c.key, created.id);
      }
      const desired = kept.map((c) => c.id ?? ids.get(c.key)!);
      // What the board looks like now: the columns kept, in their old order, then the new ones.
      const current = [
        ...board.columns.map((c) => c.id).filter((id) => desired.includes(id)),
        ...kept.filter((c) => !c.id).map((c) => ids.get(c.key)!),
      ];
      for (const move of columnMoves(current, desired)) {
        await api.post("/columns/{id}/move", { path: { id: move.id }, body: { after_id: move.after } });
      }
      await queryClient.invalidateQueries({ queryKey: boardKeys.detail(board.id) });
      onBusy(false);
      onClose();
    } catch (err) {
      setError(`${failure(err)} Lo anterior a este paso ya se guardó.`);
      void queryClient.invalidateQueries({ queryKey: boardKeys.detail(board.id) });
      onBusy(false);
    } finally {
      setSaving(false);
    }
  };

  const toggleArchive = () =>
    update.mutate({ archived: !archived }, { onSuccess: onClose, onError: (err) => setError(failure(err)) });

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (!saving) void save();
      }}
    >
      {error && (
        <Notice tone="error" title="No se guardó todo" compact>
          {error}
        </Notice>
      )}
      <Input label="Nombre" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} readOnly={saving} />

      <div className="flex flex-col gap-2">
        <span id="columns-label" className="text-[13px] font-medium">
          Columnas <span className="font-normal text-faint">· arrastra para reordenar</span>
        </span>
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
          <SortableContext items={columns.map((c) => c.key)} strategy={verticalListSortingStrategy}>
            <ul aria-labelledby="columns-label" className="flex flex-col gap-1.5">
              {columns.map((c) => (
                <ColumnRow key={c.key} column={c} disabled={saving} onChange={(change) => patch(c.key, change)} onDiscard={() => setColumns((cols) => cols.filter((x) => x.key !== c.key))} />
              ))}
            </ul>
          </SortableContext>
        </DndContext>
        <button
          type="button"
          disabled={saving || columns.length >= 50}
          onClick={() => setColumns((cols) => [...cols, { key: `new-${Date.now()}`, name: "", tasks: 0, deleted: false }])}
          className={cx("self-start rounded px-0 py-1 text-[13.5px] font-semibold text-accent-text", focusRing)}
        >
          + Añadir columna
        </button>
      </div>

      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-[13px] font-medium">Acceso</legend>
        {(["workspace", "private"] as const).map((v) => (
          <label key={v} className={cx("flex cursor-pointer items-start gap-2.5 rounded-[10px] border px-3 py-2.5", visibility === v ? "border-accent-text" : "border-border")}>
            <input type="radio" name="board-visibility" checked={visibility === v} onChange={() => setVisibility(v)} disabled={saving} className="mt-1 accent-[var(--accent-text)]" />
            <span>
              <span className="block text-[13.5px] font-semibold">{v === "workspace" ? "Todo el workspace" : "Solo personas invitadas"}</span>
              <span className="block text-[12.5px] text-muted">
                {v === "workspace" ? "Cada miembro lo ve según su rol." : "Los admins y las personas que elijas."}
              </span>
            </span>
          </label>
        ))}
        {visibility === "private" && (
          <div className="flex flex-col gap-1 rounded-[10px] border border-border p-2.5">
            {others.length === 0 ? (
              <p className="text-[13px] text-muted">Todos los miembros son admins: lo ven igualmente.</p>
            ) : (
              others.map((m) => (
                <label key={m.user_id} className="flex cursor-pointer items-center gap-2.5 rounded-md px-1.5 py-1 text-[13.5px] hover:bg-surface2">
                  <input
                    type="checkbox"
                    checked={chosen.has(m.user_id)}
                    disabled={saving || boardMembers.isPending}
                    onChange={(e) => {
                      const next = new Set(chosen);
                      if (e.target.checked) next.add(m.user_id);
                      else next.delete(m.user_id);
                      setGiven(next);
                    }}
                    className="accent-[var(--accent-text)]"
                  />
                  <span className="font-medium">{m.name}</span>
                  <span className="truncate text-muted">{m.email}</span>
                </label>
              ))
            )}
          </div>
        )}
      </fieldset>

      <div className="flex flex-wrap gap-2.5 border-t border-border pt-3.5">
        <Button size="sm" onClick={toggleArchive} loading={update.isPending && !saving} disabled={saving}>
          {archived ? "Restaurar tablero" : "Archivar tablero"}
        </Button>
        <Button size="sm" variant="danger" onClick={() => setDeleting(true)} disabled={saving}>
          Eliminar tablero
        </Button>
      </div>

      <div className="-mx-6 -mb-5 flex justify-end gap-2.5 border-t border-border px-6 py-3.5">
        <Button variant="ghost" onClick={onClose} disabled={saving}>
          Cancelar
        </Button>
        <Button type="submit" variant="primary" loading={saving}>
          Guardar
        </Button>
      </div>

      <DeleteBoardDialog
        board={{ id: board.id, name: board.name, task_count: board.columns.reduce((n, c) => n + c.tasks.length, 0) }}
        workspaceId={board.workspace_id}
        open={deleting}
        onClose={() => setDeleting(false)}
        onDeleted={() => router.replace(`/workspaces/${board.workspace_id}`)}
      />
    </form>
  );
}

function ColumnRow({
  column,
  disabled,
  onChange,
  onDiscard,
}: {
  column: Draft;
  disabled: boolean;
  onChange: (change: Partial<Draft>) => void;
  onDiscard: () => void;
}) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id: column.key,
    disabled: disabled || column.deleted,
  });
  const [editing, setEditing] = useState(!column.id);
  const label = column.name || "columna nueva";

  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={cx(
        "flex min-h-10 items-center gap-2.5 rounded-[10px] border border-border bg-surface2 pl-2 pr-1.5",
        isDragging && "relative z-10 shadow-pop",
      )}
    >
      <button
        ref={setActivatorNodeRef}
        type="button"
        aria-label={`Reordenar ${label}`}
        className={cx("cursor-grab rounded px-1 tracking-[-2px] text-faint disabled:cursor-not-allowed", focusRing)}
        disabled={disabled || column.deleted}
        {...attributes}
        {...listeners}
      >
        ⋮⋮
      </button>
      {column.deleted ? (
        <>
          <span className="flex-1 text-[13.5px] text-muted line-through">{column.name}</span>
          <span className="text-xs text-danger">
            {column.tasks > 0 ? `Se borrará con ${column.tasks === 1 ? "su tarea" : `sus ${column.tasks} tareas`}` : "Se borrará"}
          </span>
          <Button size="sm" variant="ghost" onClick={() => onChange({ deleted: false })} disabled={disabled}>
            Deshacer
          </Button>
        </>
      ) : editing ? (
        <input
          aria-label={column.id ? `Nombre de la columna ${column.name}` : "Nombre de la columna nueva"}
          value={column.name}
          autoFocus
          maxLength={100}
          readOnly={disabled}
          onChange={(e) => onChange({ name: e.target.value })}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              if (column.name.trim()) setEditing(false);
            }
          }}
          onBlur={() => column.name.trim() && setEditing(false)}
          className={cx("h-8 min-w-0 flex-1 rounded-md border border-accent-text bg-surface px-2 text-[13.5px] text-text", focusRing)}
        />
      ) : (
        <span className="flex-1 truncate text-[13.5px]">{column.name}</span>
      )}
      {!column.deleted && (
        <>
          {!editing && (
            <button
              type="button"
              aria-label={`Renombrar columna ${column.name}`}
              disabled={disabled}
              onClick={() => setEditing(true)}
              className={cx("flex size-[30px] cursor-pointer items-center justify-center rounded-[7px] text-muted hover:bg-surface hover:text-text", focusRing)}
            >
              ✎
            </button>
          )}
          <button
            type="button"
            aria-label={`Eliminar columna ${label}`}
            disabled={disabled}
            onClick={() => (column.id ? onChange({ deleted: true }) : onDiscard())}
            className={cx("flex size-[30px] cursor-pointer items-center justify-center rounded-[7px] text-base text-muted hover:bg-surface hover:text-danger", focusRing)}
          >
            ×
          </button>
        </>
      )}
    </li>
  );
}
