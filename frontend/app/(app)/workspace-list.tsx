"use client";

import Link from "next/link";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { roleLabel } from "@/lib/roles";
import { useBoards, useMembers, useWorkspaces, type Workspace } from "@/lib/workspaces";

const grid = "grid gap-4 sm:grid-cols-2 lg:grid-cols-3";

export function WorkspaceList() {
  const { data: workspaces, error, isPending, refetch, isRefetching } = useWorkspaces();

  if (isPending) {
    return (
      <div className={grid} aria-busy="true" aria-label="Cargando workspaces">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex h-[150px] flex-col gap-4 rounded-2xl border border-border bg-surface p-5">
            <div className="h-5 w-2/5 animate-pulse rounded bg-border" />
            <div className="h-4 w-3/5 animate-pulse rounded bg-border" />
            <div className="mt-auto flex gap-1">
              {[0, 1, 2].map((j) => (
                <div key={j} className="size-7 animate-pulse rounded-full bg-border" />
              ))}
            </div>
          </div>
        ))}
      </div>
    );
  }

  if (error) {
    return (
      <Notice
        tone="error"
        title="No pudimos cargar tus workspaces"
        actions={
          <Button size="sm" variant="primary" loading={isRefetching} onClick={() => refetch()}>
            Reintentar
          </Button>
        }
      >
        {error instanceof ApiError && error.code === "network_error"
          ? error.message
          : "Algo salió mal. Inténtalo de nuevo en un momento."}
      </Notice>
    );
  }

  if (workspaces.length === 0) {
    return (
      <div className="flex min-h-[180px] flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-8 text-center">
        <p className="font-medium">Todavía no perteneces a ningún workspace</p>
        <p className="text-[13.5px] text-muted">Cuando un admin te agregue a su equipo, aparecerá aquí.</p>
      </div>
    );
  }

  return (
    <ul className={grid}>
      {workspaces.map((ws) => (
        <li key={ws.id}>
          <WorkspaceCard workspace={ws} />
        </li>
      ))}
    </ul>
  );
}

const MAX_AVATARS = 4;

function WorkspaceCard({ workspace }: { workspace: Workspace }) {
  // The list has no board count or member preview, so each card asks for them.
  const boards = useBoards(workspace.id);
  const members = useMembers(workspace.id);
  const shown = members.data?.slice(0, MAX_AVATARS) ?? [];
  const hidden = workspace.member_count - shown.length;

  return (
    <Link
      href={`/workspaces/${workspace.id}/members`}
      className="flex h-full flex-col gap-4 rounded-2xl border border-border bg-surface p-5 transition-colors hover:border-border-strong focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
    >
      <div className="flex items-start justify-between gap-2">
        <h2 className="min-w-0 break-words text-[17px] font-semibold tracking-[-.01em]">{workspace.name}</h2>
        <span className="shrink-0 rounded-md border border-border-strong px-[7px] py-0.5 text-[11.5px] font-semibold text-muted">
          {roleLabel(workspace.role)}
        </span>
      </div>
      <p className="text-[13.5px] text-muted">
        {plural(workspace.member_count, "miembro", "miembros")}
        {" · "}
        {boards.data ? (
          plural(boards.data.length, "tablero activo", "tableros activos")
        ) : boards.isPending ? (
          <span className="inline-block h-3 w-24 animate-pulse rounded bg-border align-middle" aria-label="Cargando tableros" />
        ) : (
          "tableros no disponibles"
        )}
      </p>
      <div className="mt-auto flex min-h-7 items-center pl-2" aria-hidden>
        {shown.map((m) => (
          <Avatar key={m.user_id} name={m.name} size={28} className="-ml-2" />
        ))}
        {members.data && hidden > 0 && (
          <span className="-ml-2 flex size-7 items-center justify-center rounded-full bg-raised text-[11px] font-semibold text-muted shadow-[0_0_0_2px_var(--surface)]">
            +{hidden}
          </span>
        )}
        {members.isPending &&
          [0, 1, 2].map((i) => <span key={i} className="-ml-2 size-7 animate-pulse rounded-full bg-border" />)}
      </div>
    </Link>
  );
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}
