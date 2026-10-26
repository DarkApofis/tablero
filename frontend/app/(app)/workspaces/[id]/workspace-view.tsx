"use client";

import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { roleLabel } from "@/lib/roles";
import { useBoards, useWorkspaces } from "@/lib/workspaces";

const grid = "grid gap-4 sm:grid-cols-2 lg:grid-cols-3";
const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";
const created = new Intl.DateTimeFormat("es", { day: "numeric", month: "short", year: "numeric" });

export function WorkspaceView({ workspaceId }: { workspaceId: string }) {
  const boards = useBoards(workspaceId);
  const workspace = useWorkspaces().data?.find((w) => w.id === workspaceId);

  if (boards.isPending) return <WorkspaceSkeleton />;

  if (boards.error) {
    const forbidden = boards.error instanceof ApiError && boards.error.status === 403;
    return (
      <Notice
        tone="error"
        title={forbidden ? "No tienes acceso a este workspace" : "No pudimos cargar los tableros"}
        actions={
          forbidden ? undefined : (
            <Button size="sm" variant="primary" loading={boards.isRefetching} onClick={() => boards.refetch()}>
              Reintentar
            </Button>
          )
        }
      >
        {forbidden
          ? "Puede que te hayan quitado de él o que ya no exista."
          : boards.error instanceof ApiError && boards.error.code === "network_error"
            ? boards.error.message
            : "Algo salió mal. Inténtalo de nuevo en un momento."}
      </Notice>
    );
  }

  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h1 className="break-words text-[28px] font-semibold tracking-[-.02em]">{workspace?.name ?? "Tableros"}</h1>
          <p className="mt-1.5 text-sm text-muted">
            {workspace && `${roleLabel(workspace.role)} · `}
            {boards.data.length === 1 ? "1 tablero" : `${boards.data.length} tableros`}
          </p>
        </div>
        <Link
          href={`/workspaces/${workspaceId}/members`}
          className={`inline-flex h-[30px] items-center rounded-[10px] border border-border-strong bg-raised px-3 text-[12.5px] font-semibold ${focusRing}`}
        >
          Miembros{workspace && ` · ${workspace.member_count}`}
        </Link>
      </div>

      {boards.data.length === 0 ? (
        <div className="flex min-h-[180px] flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-8 text-center">
          <p className="font-medium">Este workspace todavía no tiene tableros</p>
          <p className="text-[13.5px] text-muted">Cuando un admin cree uno, aparecerá aquí.</p>
        </div>
      ) : (
        <ul className={grid}>
          {boards.data.map((b) => (
            <li key={b.id}>
              <Link
                href={`/boards/${b.id}`}
                className={`flex h-full flex-col gap-2 rounded-2xl border border-border bg-surface p-5 transition-colors hover:border-border-strong ${focusRing}`}
              >
                <h2 className="break-words text-[17px] font-semibold tracking-[-.01em]">{b.name}</h2>
                <p className="text-[13px] text-faint">Creado el {created.format(new Date(b.created_at))}</p>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

export function WorkspaceSkeleton() {
  return (
    <div className="flex flex-col gap-6" role="status" aria-busy="true">
      <span className="sr-only">Cargando tableros…</span>
      <div className="flex flex-col gap-2">
        <div className="h-8 w-56 animate-pulse rounded bg-border" />
        <div className="h-4 w-32 animate-pulse rounded bg-border" />
      </div>
      <div className={grid}>
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex h-[92px] flex-col gap-3 rounded-2xl border border-border bg-surface p-5">
            <div className="h-5 w-1/2 animate-pulse rounded bg-border" />
            <div className="h-3.5 w-1/3 animate-pulse rounded bg-border" />
          </div>
        ))}
      </div>
    </div>
  );
}
