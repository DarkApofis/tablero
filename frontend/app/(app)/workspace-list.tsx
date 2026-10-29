"use client";

import { useState } from "react";
import Link from "next/link";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { roleLabel } from "@/lib/roles";
import { useBoards, useMembers, useWorkspaceQuota, useWorkspaces, type Workspace } from "@/lib/workspaces";
import { CreateWorkspaceDialog } from "./create-workspace-dialog";

/**
 * The home page: the user's workspaces and, while they have not created the
 * most they may, two ways to create one (the header button and the dashed
 * tile after the cards), both opening the same dialog.
 */
export function WorkspacesHome() {
  const [creating, setCreating] = useState(false);
  const open = () => setCreating(true);
  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-[28px] font-semibold tracking-[-.02em]">Tus workspaces</h1>
          <p className="mt-1.5 text-sm text-muted">Elige un equipo para ver sus tableros.</p>
        </div>
        <NewWorkspace onOpen={open} />
      </div>
      <WorkspaceList onCreate={open} />
      <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
    </>
  );
}

function NewWorkspace({ onOpen }: { onOpen: () => void }) {
  const quota = useWorkspaceQuota();

  if (!quota.data) return null;
  const { created, max, canCreate } = quota.data;
  if (!canCreate) {
    return (
      <p className="text-[13px] text-muted">
        Has creado {created} de {max} workspaces, el máximo por persona.
      </p>
    );
  }
  return (
    <div className="flex flex-col items-end gap-1">
      <Button variant="primary" icon="+" onClick={onOpen}>
        Crear workspace
      </Button>
      <span className="text-[12px] text-faint">
        {max - created === 1 ? "Puedes crear 1 más" : `Puedes crear ${max - created} más`}
      </span>
    </div>
  );
}

const grid = "grid gap-4 sm:grid-cols-2 lg:grid-cols-3";

/** `onCreate`, when given, adds a "Crear workspace nuevo" tile after the cards. */
export function WorkspaceList({ onCreate }: { onCreate?: () => void }) {
  const { data: workspaces, error, isPending, refetch, isRefetching } = useWorkspaces();
  const quota = useWorkspaceQuota();

  if (isPending) {
    return (
      <div className={grid} role="status" aria-busy="true">
        <span className="sr-only">Cargando workspaces…</span>
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
    const canCreate = onCreate && quota.data?.canCreate;
    return (
      <div className="flex min-h-[180px] flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-8 text-center">
        <p className="font-medium">Todavía no perteneces a ningún workspace</p>
        <p className="text-[13.5px] text-muted">
          {canCreate
            ? "Crea el tuyo, o espera a que un admin te invite a su equipo."
            : "Espera a que un admin te invite a su equipo."}
        </p>
        {canCreate && (
          <Button variant="primary" icon="+" className="mt-3" onClick={onCreate}>
            Crear workspace nuevo
          </Button>
        )}
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
      {onCreate && quota.data?.canCreate && (
        <li>
          <button
            type="button"
            onClick={onCreate}
            className="flex h-full min-h-[140px] w-full cursor-pointer flex-col items-center justify-center gap-1 rounded-2xl border-[1.5px] border-dashed border-border-strong p-5 text-sm text-muted transition-colors hover:border-accent-text hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
          >
            <span aria-hidden className="text-[22px] leading-none text-accent-text">
              +
            </span>
            Crear workspace nuevo
          </button>
        </li>
      )}
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
      href={`/workspaces/${workspace.id}`}
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
          <>
            <span aria-hidden className="inline-block h-3 w-24 animate-pulse rounded bg-border align-middle" />
            <span className="sr-only">cargando tableros</span>
          </>
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
