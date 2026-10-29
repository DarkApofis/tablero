"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { useUpdateBoard } from "@/lib/board-admin";
import { useBoards } from "@/lib/workspaces";
import { BoardCard, type Announce } from "./board-card";
import { useWorkspaceShell } from "./workspace-shell";

export const grid = "grid gap-4 sm:grid-cols-2 lg:grid-cols-3";

/** The boards tab: a card per board and, for admins, a tile to create one. */
export function WorkspaceView({ workspaceId }: { workspaceId: string }) {
  const boards = useBoards(workspaceId);
  const { isAdmin, openNewBoard } = useWorkspaceShell();
  const [message, setMessage] = useState<Parameters<Announce>[0] | null>(null);

  if (boards.isPending) return <BoardsSkeleton />;
  if (boards.error) {
    return (
      <Notice
        tone="error"
        title="No pudimos cargar los tableros"
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
              <>
                {message.restore && <RestoreButton board={message.restore} announce={setMessage} />}
                <Button size="sm" variant="ghost" onClick={() => setMessage(null)}>
                  Cerrar
                </Button>
              </>
            }
          />
        )}
      </div>

      {boards.data.length === 0 && !isAdmin ? (
        <div className="flex min-h-[180px] flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-8 text-center">
          <p className="font-medium">Este workspace todavía no tiene tableros</p>
          <p className="text-[13.5px] text-muted">Cuando un admin cree uno, aparecerá aquí.</p>
        </div>
      ) : (
        <ul className={grid}>
          {boards.data.map((b) => (
            <li key={b.id}>
              <BoardCard board={b} announce={setMessage} />
            </li>
          ))}
          {isAdmin && (
            <li>
              <button
                type="button"
                onClick={openNewBoard}
                className="flex h-full min-h-[130px] w-full cursor-pointer flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-[18px] text-sm text-muted transition-colors hover:border-accent-text hover:text-accent-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
              >
                <span aria-hidden className="text-[22px] leading-none text-accent-text">
                  +
                </span>
                {boards.data.length === 0 ? "Crea el primer tablero" : "Nuevo tablero"}
              </button>
            </li>
          )}
        </ul>
      )}
    </>
  );
}

/** Undoes archiving a board. */
export function RestoreButton({ board, announce, label = "Deshacer" }: { board: { id: string; name: string }; announce: Announce; label?: string }) {
  const update = useUpdateBoard(board.id);
  return (
    <Button
      size="sm"
      aria-label={`${label}: ${board.name}`}
      loading={update.isPending}
      onClick={() =>
        update.mutate(
          { archived: false },
          {
            onSuccess: () => announce({ text: `«${board.name}» se restauró.` }),
            onError: () => announce({ tone: "error", text: `No se pudo restaurar «${board.name}».` }),
          },
        )
      }
    >
      {label}
    </Button>
  );
}

export function BoardsSkeleton() {
  return (
    <div className={grid} role="status" aria-busy="true">
      <span className="sr-only">Cargando tableros…</span>
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex h-[130px] flex-col gap-3.5 rounded-2xl border border-border bg-surface p-[18px]">
          <div className="h-5 w-1/2 animate-pulse rounded bg-border" />
          <div className="h-3.5 w-2/3 animate-pulse rounded bg-border" />
          <div className="mt-auto flex gap-1">
            {[0, 1, 2].map((j) => (
              <div key={j} className="size-[26px] animate-pulse rounded-full bg-border" />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
