"use client";

import { useState } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { relative, useNow } from "@/lib/time";
import { useArchivedBoards, type BoardSummary } from "@/lib/workspaces";
import { DeleteBoardDialog, type Announce } from "../board-card";
import { BoardsSkeleton, RestoreButton } from "../workspace-view";
import { useWorkspaceShell } from "../workspace-shell";

/** Archived boards: anyone can open them (read only); admins restore or delete them. */
export function ArchivedView({ workspaceId }: { workspaceId: string }) {
  const boards = useArchivedBoards(workspaceId);
  const { isAdmin } = useWorkspaceShell();
  const now = useNow(60_000);
  const [message, setMessage] = useState<Parameters<Announce>[0] | null>(null);
  const [deleting, setDeleting] = useState<BoardSummary | null>(null);

  if (boards.isPending) return <BoardsSkeleton />;
  if (boards.error) {
    return (
      <Notice
        tone="error"
        title="No pudimos cargar los tableros archivados"
        actions={
          <Button size="sm" variant="primary" loading={boards.isRefetching} onClick={() => boards.refetch()}>
            Reintentar
          </Button>
        }
      >
        {boards.error instanceof ApiError && boards.error.code === "network_error"
          ? boards.error.message
          : "Algo salió mal. Inténtalo de nuevo en un momento."}
      </Notice>
    );
  }

  return (
    <>
      <div aria-live="polite">
        {message && (
          <Notice
            tone={message.tone ?? "success"}
            title={message.text}
            compact
            actions={
              <Button size="sm" variant="ghost" onClick={() => setMessage(null)}>
                Cerrar
              </Button>
            }
          />
        )}
      </div>
      {boards.data.length === 0 ? (
        <div className="flex min-h-[160px] flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-8 text-center">
          <p className="font-medium">No hay tableros archivados</p>
          <p className="text-[13.5px] text-muted">Un tablero archivado deja de aparecer en la lista, pero no se borra.</p>
        </div>
      ) : (
        <ul className="rounded-[14px] border border-border bg-surface">
          {boards.data.map((b) => (
            <li key={b.id} className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3 last:border-b-0">
              <div className="min-w-0 flex-1">
                <Link
                  href={`/boards/${b.id}`}
                  className="rounded text-sm font-semibold hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
                >
                  {b.name}
                </Link>
                <div className="text-[12.5px] text-muted">
                  {b.task_count === 1 ? "1 tarea" : `${b.task_count} tareas`} · Archivado{" "}
                  {b.archived_at ? relative(b.archived_at, now) : ""}
                </div>
              </div>
              {isAdmin && (
                <>
                  <RestoreButton board={b} announce={setMessage} label="Restaurar" />
                  <Button size="sm" variant="ghost" onClick={() => setDeleting(b)}>
                    Eliminar
                  </Button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      {deleting && (
        <DeleteBoardDialog
          board={deleting}
          workspaceId={workspaceId}
          open
          onClose={() => setDeleting(null)}
          onDeleted={() => setMessage({ text: `«${deleting.name}» se eliminó.` })}
        />
      )}
    </>
  );
}
