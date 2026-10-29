"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ActionMenu, type MenuItem } from "@/components/ui/action-menu";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { cx } from "@/components/ui/cx";
import { Modal } from "@/components/ui/modal";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { useDeleteBoard, useDuplicateBoard, useMoveBoard, useUpdateBoard } from "@/lib/board-admin";
import { relative, useNow } from "@/lib/time";
import { useBoardPresence, useWorkspaces, type BoardSummary } from "@/lib/workspaces";
import { useWorkspaceShell } from "./workspace-shell";

/**
 * Tells the boards tab what an action did. With `restore`, it offers to undo
 * archiving that board (the tab does it: the card leaves the list).
 */
export type Announce = (message: { text: string; tone?: "success" | "error"; restore?: { id: string; name: string } }) => void;

const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";

function failure(err: unknown) {
  if (err instanceof ApiError && (err.status < 500 || err.code === "network_error")) return err.message;
  return "Algo salió mal. Inténtalo de nuevo en un momento.";
}

/**
 * A board in its workspace (design 2b): name, task count, when it last
 * changed, who changed it and who is on it now. Admins get its menu: open,
 * rename (in place), duplicate, move, archive and delete.
 */
export function BoardCard({ board, announce }: { board: BoardSummary; announce: Announce }) {
  const router = useRouter();
  const { workspace, isAdmin } = useWorkspaceShell();
  const now = useNow(60_000);
  const presence = useBoardPresence(board.id);
  const update = useUpdateBoard(board.id);
  const duplicate = useDuplicateBoard(board.id);
  const [renaming, setRenaming] = useState(false);
  const [dialog, setDialog] = useState<"move" | "delete" | null>(null);
  const live = presence.data?.length ?? 0;
  const tasks = board.task_count === 1 ? "1 tarea" : `${board.task_count} tareas`;

  const archive = () =>
    update.mutate(
      { archived: true },
      {
        onSuccess: () =>
          announce({
            text: `«${board.name}» se archivó. Lo encontrarás en Archivados.`,
            restore: { id: board.id, name: board.name },
          }),
        onError: (err) => announce({ tone: "error", text: `No se pudo archivar: ${failure(err)}` }),
      },
    );

  const items: MenuItem[] = [
    { label: "Abrir", onSelect: () => router.push(`/boards/${board.id}`) },
    { label: "Renombrar", onSelect: () => setRenaming(true) },
    {
      label: "Duplicar",
      onSelect: () =>
        duplicate.mutate(undefined, {
          onSuccess: (copy) => announce({ text: `Se creó «${copy.name}».` }),
          onError: (err) => announce({ tone: "error", text: `No se pudo duplicar: ${failure(err)}` }),
        }),
    },
    { label: "Mover a otro workspace", onSelect: () => setDialog("move") },
    { label: "Archivar", onSelect: archive },
    { label: "Eliminar tablero", danger: true, onSelect: () => setDialog("delete") },
  ];

  return (
    <article
      aria-busy={update.isPending || duplicate.isPending || undefined}
      className="relative flex h-full flex-col gap-3.5 rounded-2xl border border-border bg-surface p-[18px] transition-colors hover:border-border-strong"
    >
      <div className="flex min-h-[34px] items-center justify-between gap-2">
        {renaming ? (
          <RenameForm board={board} onDone={() => setRenaming(false)} />
        ) : (
          <>
            <h2 className="min-w-0 flex-1 break-words text-base font-semibold">
              {/* The link covers the card; the menu sits above it. */}
              <Link href={`/boards/${board.id}`} className={cx("rounded after:absolute after:inset-0 after:rounded-2xl", focusRing)}>
                {board.name}
              </Link>
            </h2>
            {board.visibility === "private" && (
              <span className="shrink-0 rounded-md border border-border-strong px-[7px] py-0.5 text-[11.5px] font-semibold text-muted">
                Privado
              </span>
            )}
            {isAdmin && <ActionMenu label={`Acciones del tablero ${board.name}`} items={items} className="z-10" />}
          </>
        )}
      </div>
      <p className="text-[13px] text-muted">
        {tasks} · Actualizado {relative(board.updated_at, now)}
      </p>
      <div className="mt-auto flex min-h-[26px] items-center justify-between">
        <div className="flex pl-2" aria-label={board.actors.length ? `Últimos cambios de ${board.actors.map((a) => a.name).join(", ")}` : undefined}>
          {board.actors.map((a) => (
            <Avatar key={a.id} name={a.name} size={26} className="-ml-2 shadow-[0_0_0_2px_var(--surface)]" />
          ))}
        </div>
        {live > 0 && <span className="text-[12.5px] text-accent-text">● {live} en vivo</span>}
      </div>

      {isAdmin && (
        <>
          <MoveDialog
            board={board}
            open={dialog === "move"}
            onClose={() => setDialog(null)}
            onMoved={(to) => announce({ text: `«${board.name}» se movió a ${to}.` })}
          />
          <DeleteBoardDialog
            board={board}
            workspaceId={workspace.id}
            open={dialog === "delete"}
            onClose={() => setDialog(null)}
            onDeleted={() => announce({ text: `«${board.name}» se eliminó.` })}
          />
        </>
      )}
    </article>
  );
}

function RenameForm({ board, onDone }: { board: BoardSummary; onDone: () => void }) {
  const update = useUpdateBoard(board.id);
  const [name, setName] = useState(board.name);
  const [error, setError] = useState<string>();

  const save = () => {
    const trimmed = name.trim();
    if (!trimmed) return setError("Escribe un nombre");
    if (trimmed === board.name) return onDone();
    update.mutate({ name: trimmed }, { onSuccess: onDone, onError: (err) => setError(failure(err)) });
  };

  return (
    <form
      noValidate
      className="relative z-10 flex flex-1 flex-col gap-1"
      onSubmit={(e) => {
        e.preventDefault();
        save();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onDone();
        }
      }}
    >
      <div className="flex gap-1.5">
        <input
          aria-label="Nuevo nombre del tablero"
          value={name}
          autoFocus
          maxLength={100}
          readOnly={update.isPending}
          aria-invalid={error ? true : undefined}
          onChange={(e) => {
            setName(e.target.value);
            setError(undefined);
          }}
          onFocus={(e) => e.target.select()}
          className={cx(
            "h-[34px] min-w-0 flex-1 rounded-lg border bg-surface2 px-2.5 text-[15px] font-semibold text-text",
            "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text",
            error ? "border-danger" : "border-accent-text",
          )}
        />
        <button
          type="submit"
          aria-label="Guardar nombre"
          className={cx("size-[34px] shrink-0 cursor-pointer rounded-lg bg-accent font-bold text-accent-ink", focusRing)}
        >
          ✓
        </button>
      </div>
      {error && <span className="text-[12.5px] text-danger">! {error}</span>}
    </form>
  );
}

/** "Mover a otro workspace": one where the user is an admin too. */
function MoveDialog({ board, open, onClose, onMoved }: { board: BoardSummary; open: boolean; onClose: () => void; onMoved: (to: string) => void }) {
  const workspaces = useWorkspaces();
  const move = useMoveBoard(board.id, board.workspace_id);
  const [target, setTarget] = useState<string | null>(null);
  const options = (workspaces.data ?? []).filter((w) => w.role === "admin" && w.id !== board.workspace_id);
  const chosen = options.find((w) => w.id === target) ?? options[0];

  return (
    <Modal
      open={open}
      busy={move.isPending}
      title="Mover tablero"
      description={`«${board.name}» pasará a otro workspace donde seas admin.`}
      onClose={onClose}
      onSubmit={() =>
        chosen &&
        move.mutate(chosen.id, {
          onSuccess: () => {
            onClose();
            onMoved(chosen.name);
          },
        })
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={move.isPending}>
            Cancelar
          </Button>
          <Button type="submit" variant="primary" loading={move.isPending} disabled={!chosen}>
            Mover
          </Button>
        </>
      }
    >
      {move.error && (
        <Notice tone="error" title="No se pudo mover" compact>
          {failure(move.error)}
        </Notice>
      )}
      {options.length === 0 ? (
        <p className="text-sm text-muted">No eres admin de ningún otro workspace.</p>
      ) : (
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-[13px] font-medium">Workspace de destino</legend>
          {options.map((w) => (
            <label
              key={w.id}
              className={cx(
                "flex cursor-pointer items-center gap-2.5 rounded-[10px] border px-3 py-2.5 text-sm",
                chosen?.id === w.id ? "border-accent-text bg-accent-soft" : "border-border",
              )}
            >
              <input
                type="radio"
                name={`move-${board.id}`}
                checked={chosen?.id === w.id}
                onChange={() => setTarget(w.id)}
                className="accent-[var(--accent-text)]"
              />
              <span className="font-medium">{w.name}</span>
            </label>
          ))}
        </fieldset>
      )}
      <p className="text-[12.5px] text-faint">
        Quien no sea miembro del destino deja de verlo, y sus tareas en él quedan sin responsable.
      </p>
    </Modal>
  );
}

export function DeleteBoardDialog({
  board,
  workspaceId,
  open,
  onClose,
  onDeleted,
}: {
  board: { id: string; name: string; task_count?: number };
  workspaceId: string;
  open: boolean;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const remove = useDeleteBoard(board.id, workspaceId);
  const tasks = board.task_count === undefined ? "sus tareas" : board.task_count === 1 ? "su tarea" : `sus ${board.task_count} tareas`;
  return (
    <ConfirmDialog
      open={open}
      tone="danger"
      title={`¿Eliminar «${board.name}»?`}
      confirmLabel="Eliminar tablero"
      pending={remove.isPending}
      onCancel={onClose}
      onConfirm={() =>
        remove.mutate(undefined, {
          onSuccess: () => {
            onClose();
            onDeleted();
          },
        })
      }
    >
      Se borran sus columnas, {tasks} y su actividad. No se puede deshacer.
      {remove.error && <span className="mt-2 block text-danger">! {failure(remove.error)}</span>}
    </ConfirmDialog>
  );
}
