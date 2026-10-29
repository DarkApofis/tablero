"use client";

import { createContext, useContext, useState, type ReactNode } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Button } from "@/components/ui/button";
import { cx } from "@/components/ui/cx";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { canManageBoards, canManageMembers, roleLabel } from "@/lib/roles";
import { useWorkspace, type WorkspaceDetail } from "@/lib/workspaces";
import { InviteDialog } from "./invite-dialog";
import { NewBoardDialog } from "./new-board-dialog";

type Shell = {
  workspace: WorkspaceDetail;
  isAdmin: boolean;
  openNewBoard: () => void;
};

const ShellContext = createContext<Shell | null>(null);

/** The workspace around a tab: its data, and the "Nuevo tablero" dialog. */
export function useWorkspaceShell() {
  const shell = useContext(ShellContext);
  if (!shell) throw new Error("useWorkspaceShell outside WorkspaceShell");
  return shell;
}

const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";

/**
 * A workspace's page (design 2b): its name and numbers, the admin's actions
 * (edit, invite, new board) and the tabs: boards, members, settings and
 * archived boards.
 */
export function WorkspaceShell({ workspaceId, children }: { workspaceId: string; children: ReactNode }) {
  const workspace = useWorkspace(workspaceId);
  const pathname = usePathname();
  const [dialog, setDialog] = useState<"invite" | "board" | null>(null);

  if (workspace.isPending) return <ShellSkeleton />;
  if (workspace.error) {
    const forbidden = workspace.error instanceof ApiError && workspace.error.status === 403;
    return (
      <Notice
        tone="error"
        title={forbidden ? "No tienes acceso a este workspace" : "No pudimos cargar el workspace"}
        actions={
          forbidden ? (
            <Link href="/" className={`rounded text-[13px] font-semibold text-text underline underline-offset-2 ${focusRing}`}>
              Volver a tus workspaces
            </Link>
          ) : (
            <Button size="sm" variant="primary" loading={workspace.isRefetching} onClick={() => workspace.refetch()}>
              Reintentar
            </Button>
          )
        }
      >
        {forbidden
          ? "Puede que te hayan quitado de él o que ya no exista."
          : workspace.error instanceof ApiError && workspace.error.code === "network_error"
            ? workspace.error.message
            : "Algo salió mal. Inténtalo de nuevo en un momento."}
      </Notice>
    );
  }

  const ws = workspace.data;
  const isAdmin = canManageBoards(ws.role);
  const base = `/workspaces/${ws.id}`;
  const tabs = [
    { href: base, label: "Tableros", count: ws.board_count },
    { href: `${base}/members`, label: "Miembros", count: ws.member_count },
    ...(isAdmin ? [{ href: `${base}/settings`, label: "Ajustes" }] : []),
    { href: `${base}/archived`, label: "Archivados" },
  ];

  return (
    <ShellContext.Provider value={{ workspace: ws, isAdmin, openNewBoard: () => setDialog("board") }}>
      <nav aria-label="Ruta" className="text-[13.5px] text-muted">
        <Link href="/" className={`rounded hover:text-text ${focusRing}`}>
          Tus workspaces
        </Link>
        <span aria-hidden> / </span>
        <span className="font-semibold text-text">{ws.name}</span>
      </nav>

      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="break-words text-[28px] font-semibold tracking-[-.02em]">{ws.name}</h1>
            {isAdmin && (
              <Link
                href={`${base}/settings`}
                aria-label="Editar workspace"
                className={`inline-flex h-7 items-center rounded-lg border border-border-strong px-2.5 text-[12.5px] text-muted hover:text-text ${focusRing}`}
              >
                <span aria-hidden>✎&nbsp;</span>Editar
              </Link>
            )}
          </div>
          <p className="mt-1.5 text-sm text-muted">
            {roleLabel(ws.role)} · {plural(ws.member_count, "miembro", "miembros")} ·{" "}
            {plural(ws.board_count, "tablero activo", "tableros activos")}
          </p>
          {ws.description && <p className="mt-1 max-w-[70ch] text-[13.5px] text-faint">{ws.description}</p>}
        </div>
        {isAdmin && (
          <div className="flex gap-2.5">
            {canManageMembers(ws.role) && <Button onClick={() => setDialog("invite")}>Invitar</Button>}
            <Button variant="primary" icon="+" onClick={() => setDialog("board")}>
              Nuevo tablero
            </Button>
          </div>
        )}
      </div>

      <nav aria-label="Secciones del workspace" className="-mt-1 flex gap-1 overflow-x-auto border-b border-border">
        {tabs.map((tab) => {
          const current = pathname === tab.href;
          return (
            <Link
              key={tab.href}
              href={tab.href}
              aria-current={current ? "page" : undefined}
              className={cx(
                "-mb-px whitespace-nowrap rounded-t px-3 pb-2.5 pt-2 text-[13.5px]",
                focusRing,
                current ? "border-b-2 border-accent font-semibold text-text" : "text-muted hover:text-text",
              )}
            >
              {tab.label}
              {tab.count !== undefined && <span className="text-faint"> {tab.count}</span>}
            </Link>
          );
        })}
      </nav>

      {children}

      {isAdmin && (
        <>
          <InviteDialog workspace={ws} open={dialog === "invite"} onClose={() => setDialog(null)} />
          <NewBoardDialog workspace={ws} open={dialog === "board"} onClose={() => setDialog(null)} />
        </>
      )}
    </ShellContext.Provider>
  );
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}

export function ShellSkeleton() {
  return (
    <div className="flex flex-col gap-6" role="status" aria-busy="true">
      <span className="sr-only">Cargando workspace…</span>
      <div className="h-4 w-48 animate-pulse rounded bg-border" />
      <div className="flex flex-col gap-2">
        <div className="h-8 w-56 animate-pulse rounded bg-border" />
        <div className="h-4 w-64 animate-pulse rounded bg-border" />
      </div>
      <div className="h-9 w-full animate-pulse rounded bg-border/60" />
    </div>
  );
}
