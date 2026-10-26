"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { useBoard, type BoardDetail } from "@/lib/boards";
import { matchesFilters, readFilters, UNASSIGNED, writeFilters, type TaskFilters } from "@/lib/tasks";
import { useBoardEvents } from "@/lib/use-board-events";
import { useWorkspaces } from "@/lib/workspaces";
import { BoardCanvas } from "./board-canvas";
import { BoardSkeleton } from "./board-skeleton";
import { ConnectionBadge, PresenceAvatars } from "./board-presence";
import { ToastRegion, type Toast } from "./board-toast";
import { FilterBar } from "./filter-bar";
import { TaskPanel } from "./task-panel";

export function BoardView({ boardId }: { boardId: string }) {
  const board = useBoard(boardId);
  const workspaces = useWorkspaces();
  const searchParams = useSearchParams();
  const [today] = useState(() => new Date());
  const [toast, setToast] = useState<Toast | null>(null);
  const dismissToast = useCallback(() => setToast(null), []);
  // Whether this page pushed the history entry of the open panel.
  const pushedPanel = useRef(false);
  const name = board.data?.name;
  // A failed refetch keeps the board on screen (the badge says it's offline), unless
  // the board is gone or no longer ours to see.
  const lost = board.error instanceof ApiError && (board.error.status === 403 || board.error.status === 404);
  const live = useBoardEvents(boardId, { enabled: board.data !== undefined && !lost });

  // The route's metadata can't know the name; it comes from the API.
  useEffect(() => {
    if (name) document.title = `${name} · Tablero`;
  }, [name]);

  if (board.isPending) return <BoardSkeleton />;
  if (board.error && (!board.data || lost)) {
    return (
      <BoardError
        error={board.error}
        retrying={board.isRefetching}
        retry={() => {
          void board.refetch();
          if (workspaces.error) void workspaces.refetch(); // for the breadcrumb
        }}
      />
    );
  }

  const b = board.data;
  const workspaceName = workspaces.data?.find((w) => w.id === b.workspace_id)?.name;
  const tasks = b.columns.flatMap((c) => c.tasks);
  const filters = knownFilters(b, readFilters(searchParams));
  const filtered = filters.labels.length > 0 || filters.assignees.length > 0;
  const visible = b.columns.map((c) => (filtered ? c.tasks.filter((t) => matchesFilters(t, filters)) : c.tasks));

  // Native history updates keep useSearchParams in sync without a server round trip.
  const setFilters = (next: TaskFilters) => {
    const query = writeFilters(new URLSearchParams(window.location.search), next).toString();
    window.history.replaceState(null, "", query ? `?${query}` : window.location.pathname);
  };

  // The open task lives in the URL (?task=), so it can be shared and Back closes it.
  const openId = searchParams.get("task");
  const openTask = (taskId: string) => {
    const params = new URLSearchParams(window.location.search);
    const switching = params.has("task");
    params.set("task", taskId);
    if (switching) {
      window.history.replaceState(null, "", `?${params}`);
    } else {
      window.history.pushState(null, "", `?${params}`);
      pushedPanel.current = true;
    }
  };
  const closeTask = () => {
    const taskId = openId;
    if (pushedPanel.current) {
      pushedPanel.current = false;
      window.history.back();
    } else {
      const params = new URLSearchParams(window.location.search);
      params.delete("task");
      const query = params.toString();
      window.history.replaceState(null, "", query ? `?${query}` : window.location.pathname);
    }
    // Back to the card that opened it, once the panel is gone.
    setTimeout(() => document.querySelector<HTMLElement>(`[data-task-id="${taskId}"]`)?.focus(), 50);
  };

  return (
    <>
      <div className="flex min-h-14 shrink-0 items-center gap-3 border-b border-border bg-surface px-3 sm:px-5">
        <Link
          href={`/workspaces/${b.workspace_id}`}
          aria-label={workspaceName ? `Volver a ${workspaceName}` : "Volver al workspace"}
          className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-border text-lg text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
        >
          ‹
        </Link>
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          {workspaceName && (
            <span className="hidden whitespace-nowrap text-[13px] text-faint sm:inline">{workspaceName} /</span>
          )}
          <h1 className="truncate text-base font-semibold tracking-[-.01em]">{b.name}</h1>
        </div>
        <ConnectionBadge status={live.status} />
        <div className="max-sm:hidden">
          <PresenceAvatars users={live.presence} stale={live.status !== "live"} />
        </div>
        {b.role === "viewer" && (
          <span className="whitespace-nowrap rounded-lg border border-border-strong px-2 py-1 text-xs font-semibold text-muted">
            Solo lectura
          </span>
        )}
      </div>

      {b.columns.length === 0 ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <div className="flex w-[min(460px,100%)] flex-col items-center gap-2.5 rounded-[18px] border-[1.5px] border-dashed border-border-strong bg-surface px-7 py-9 text-center">
            <div aria-hidden className="mb-1.5 flex items-end gap-[5px]">
              <div className="h-[30px] w-3.5 rounded border-[1.5px] border-dashed border-accent-text" />
              <div className="h-[22px] w-3.5 rounded border-[1.5px] border-dashed border-accent-text" />
              <div className="h-9 w-3.5 rounded border-[1.5px] border-dashed border-accent-text" />
            </div>
            <h2 className="text-xl font-semibold tracking-[-.01em]">Este tablero aún no tiene columnas</h2>
            <p className="text-sm leading-[1.55] text-muted [text-wrap:pretty]">
              Cuando se creen columnas, sus tareas aparecerán aquí.
            </p>
          </div>
        </div>
      ) : (
        <>
          <FilterBar
            labels={b.labels}
            tasks={tasks}
            filters={filters}
            onChange={setFilters}
            shown={visible.reduce((n, list) => n + list.length, 0)}
          />
          <div className="flex min-h-0 flex-1">
            <BoardCanvas
              board={b}
              visible={visible}
              filtered={filtered}
              today={today}
              openId={openId}
              onOpen={openTask}
              notify={setToast}
              onDragTask={live.holdTask}
            />
            {openId && <TaskPanel board={b} taskId={openId} onClose={closeTask} notify={setToast} />}
          </div>
        </>
      )}
      <ToastRegion toast={toast} onDismiss={dismissToast} />
    </>
  );
}

/** Drops ids from the URL that this board doesn't have, e.g. a deleted label. */
function knownFilters(board: BoardDetail, requested: TaskFilters): TaskFilters {
  const tasks = board.columns.flatMap((c) => c.tasks);
  return {
    labels: requested.labels.filter((id) => board.labels.some((l) => l.id === id)),
    assignees: requested.assignees.filter((id) =>
      tasks.some((t) => (t.assignee?.id ?? UNASSIGNED) === id),
    ),
  };
}

function BoardError({ error, retrying, retry }: { error: Error; retrying: boolean; retry: () => void }) {
  const status = error instanceof ApiError ? error.status : 0;
  const missing = status === 404 || status === 422; // 422: the id isn't a UUID
  const [title, text] =
    status === 403
      ? ["No tienes acceso a este tablero", "Puede que te hayan quitado del workspace."]
      : missing
        ? ["Este tablero no existe", "Puede que lo hayan borrado o que el enlace esté mal."]
        : error instanceof ApiError && error.code === "network_error"
          ? ["No pudimos cargar el tablero", "Parece que hay un problema con tu conexión. Los cambios ya guardados están a salvo."]
          : ["No pudimos cargar el tablero", "Algo salió mal. Inténtalo de nuevo en un momento."];

  return (
    <div className="flex flex-1 items-center justify-center p-6">
      <div role="alert" className="flex w-[min(440px,100%)] flex-col items-center gap-2.5 text-center">
        <div
          aria-hidden
          className="flex size-11 items-center justify-center rounded-[14px] border-[1.5px] border-danger bg-danger-soft text-xl font-bold text-danger"
        >
          !
        </div>
        <h1 className="mt-1.5 text-xl font-semibold tracking-[-.01em]">{title}</h1>
        <p className="text-sm leading-[1.55] text-muted [text-wrap:pretty]">{text}</p>
        <div className="mt-3 flex flex-wrap justify-center gap-2.5">
          {status !== 403 && !missing && (
            <Button variant="primary" loading={retrying} onClick={retry}>
              Reintentar
            </Button>
          )}
          <Link
            href="/"
            className="inline-flex h-[38px] items-center rounded-[10px] px-4 text-sm font-semibold text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
          >
            Volver a workspaces
          </Link>
        </div>
      </div>
    </div>
  );
}
