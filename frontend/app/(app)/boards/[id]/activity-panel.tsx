"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { useActivity, type ActivityItem } from "@/lib/activity";
import { ApiError, type Schemas } from "@/lib/api";
import { useMe } from "@/lib/auth";
import type { BoardDetail } from "@/lib/boards";
import { priorities, type Priority } from "@/lib/tasks";
import { relative, useNow } from "@/lib/time";
import { useMembers } from "@/lib/workspaces";
import { PanelFrame } from "./panel-frame";

type Payload<K extends keyof Schemas> = Schemas[K];

export function ActivityPanel({ board, onClose }: { board: BoardDetail; onClose: () => void }) {
  const activity = useActivity(board.id);
  // Loaded so live entries can name who made them.
  const members = useMembers(board.workspace_id);
  const { data: me } = useMe();
  const now = useNow(30_000);
  const sentinel = useRef<HTMLDivElement>(null);

  const { hasNextPage, isFetchingNextPage, fetchNextPage, isFetchNextPageError } = activity;
  // Reaching the end of the list loads the next page; the button does the same by hand.
  useEffect(() => {
    const node = sentinel.current;
    if (!node || !hasNextPage || isFetchNextPageError) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting) && !isFetchingNextPage) void fetchNextPage();
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, [hasNextPage, isFetchingNextPage, isFetchNextPageError, fetchNextPage]);

  const items = activity.data?.pages.flatMap((p) => p.items) ?? [];
  const ctx: Ctx = {
    columnName: (id) => board.columns.find((c) => c.id === id)?.name,
    labelName: (id) => board.labels.find((l) => l.id === id)?.name,
    personName: (id) => members.data?.find((m) => m.user_id === id)?.name,
  };

  return (
    <PanelFrame label="Actividad del tablero" onClose={onClose} status={<h2 className="text-sm font-semibold">Actividad</h2>}>
      {activity.isPending ? (
        <ol aria-busy="true" aria-label="Cargando actividad" className="flex flex-col gap-3.5">
          {[0, 1, 2, 3, 4].map((i) => (
            <li key={i} className="flex gap-2.5">
              <div className="size-6 shrink-0 animate-pulse rounded-full bg-border" />
              <div className="flex flex-1 flex-col gap-1.5">
                <div className="h-3.5 w-4/5 animate-pulse rounded bg-border" />
                <div className="h-2.5 w-1/4 animate-pulse rounded bg-border" />
              </div>
            </li>
          ))}
        </ol>
      ) : activity.isError && items.length === 0 ? (
        <Notice
          tone="error"
          title="No pudimos cargar la actividad"
          actions={
            <Button size="sm" variant="primary" loading={activity.isRefetching} onClick={() => activity.refetch()}>
              Reintentar
            </Button>
          }
        >
          {activity.error instanceof ApiError && activity.error.code === "network_error"
            ? activity.error.message
            : "Algo salió mal. Inténtalo de nuevo en un momento."}
        </Notice>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center gap-1 rounded-xl border-[1.5px] border-dashed border-border-strong px-4 py-8 text-center">
          <p className="text-sm font-semibold">Todavía no hay actividad</p>
          <p className="text-[12.5px] text-muted">Los cambios en el tablero aparecerán aquí, también los de otras personas.</p>
        </div>
      ) : (
        <>
          <ol className="flex flex-col gap-3.5">
            {items.map((item) => (
              <li key={item.id} className="flex items-start gap-2.5">
                <span aria-hidden>
                  <Avatar name={item.actor?.name ?? "?"} size={24} />
                </span>
                <div className="min-w-0 flex-1 text-[13px] leading-[1.45] text-muted">
                  <span className="font-semibold text-text">
                    {item.actor ? item.actor.name : "Un usuario eliminado"}
                    {item.actor && item.actor.id === me?.id && <span className="font-normal text-muted"> (tú)</span>}
                  </span>{" "}
                  <span className="break-words">{describe(item, ctx)}</span>
                  <time dateTime={item.created_at} title={fullDate.format(Date.parse(item.created_at))} className="mt-0.5 block text-[11.5px] text-faint">
                    {relative(item.created_at, now)}
                  </time>
                </div>
              </li>
            ))}
          </ol>
          <div ref={sentinel}>
            {isFetchNextPageError ? (
              <Notice
                tone="error"
                compact
                title="No pudimos cargar más actividad"
                actions={
                  <Button size="sm" variant="secondary" onClick={() => fetchNextPage()}>
                    Reintentar
                  </Button>
                }
              />
            ) : hasNextPage ? (
              <Button size="sm" variant="ghost" full loading={isFetchingNextPage} onClick={() => fetchNextPage()}>
                {isFetchingNextPage ? "Cargando…" : "Cargar más"}
              </Button>
            ) : (
              items.length > 10 && <p className="text-center text-xs text-faint">No hay actividad anterior.</p>
            )}
          </div>
        </>
      )}
    </PanelFrame>
  );
}

type Ctx = {
  columnName: (id: string) => string | undefined;
  labelName: (id: string) => string | undefined;
  personName: (id: string) => string | undefined;
};

const shortDate = new Intl.DateTimeFormat("es", { day: "numeric", month: "short", timeZone: "UTC" });
const fullDate = new Intl.DateTimeFormat("es", { dateStyle: "long", timeStyle: "short" });

/** "creó la tarea «X» en Por hacer", from the entry's payload. */
function describe(item: ActivityItem, ctx: Ctx): ReactNode {
  const q = (s: string) => `«${s}»`;
  const column = (id: string) => ctx.columnName(id) ?? "otra columna";
  switch (item.type) {
    case "task_created": {
      const p = item.payload as Payload<"TaskCreatedPayload">;
      return `creó la tarea ${q(p.title)} en ${column(p.column_id)}`;
    }
    case "task_archived": {
      const p = item.payload as Payload<"TaskUpdatedPayload">;
      return `archivó ${q(p.title)}`;
    }
    case "task_updated": {
      const p = item.payload as Payload<"TaskUpdatedPayload">;
      return describeUpdate(p, ctx);
    }
    case "task_moved": {
      const p = item.payload as Payload<"TaskMovedPayload">;
      return p.from.column_id === p.to.column_id
        ? `reordenó ${q(p.title)} en ${column(p.to.column_id)}`
        : `movió ${q(p.title)} a ${column(p.to.column_id)}`;
    }
    case "task_deleted": {
      const p = item.payload as Payload<"TaskDeletedPayload">;
      return `borró la tarea ${q(p.title)}`;
    }
    case "column_created": {
      const p = item.payload as Payload<"ColumnCreatedPayload">;
      return `creó la columna ${q(p.name)}`;
    }
    case "column_updated": {
      const p = item.payload as Payload<"ColumnUpdatedPayload">;
      return p.changes.name ? `renombró la columna ${q(p.changes.name.from)} a ${q(p.changes.name.to)}` : `editó la columna ${q(p.name)}`;
    }
    case "column_reordered": {
      const p = item.payload as Payload<"ColumnReorderedPayload">;
      return `cambió de lugar la columna ${q(p.name)}`;
    }
    case "column_deleted": {
      const p = item.payload as Payload<"ColumnDeletedPayload">;
      const n = p.deleted_task_ids.length;
      return `borró la columna ${q(p.name)}${n > 0 ? ` y ${n === 1 ? "su tarea" : `sus ${n} tareas`}` : ""}`;
    }
    case "label_changed": {
      const p = item.payload as Payload<"LabelChangedPayload">;
      const verb = { created: "creó", updated: "editó", deleted: "borró" }[p.action];
      return `${verb} la etiqueta ${q(p.label.name)}`;
    }
    default:
      return "hizo un cambio";
  }
}

function describeUpdate(p: Payload<"TaskUpdatedPayload">, ctx: Ctx) {
  const title = `«${p.title}»`;
  const c = p.changes;
  const keys = Object.keys(c);
  if (keys.length === 1) {
    if (c.title) return `renombró «${c.title.from}» a «${c.title.to}»`;
    if (c.description) return `editó la descripción de ${title}`;
    if (c.priority) return `cambió la prioridad de ${title} a ${priorities[c.priority.to as Priority]?.label ?? c.priority.to}`;
    if (c.due_date) {
      return c.due_date.to ? `cambió la fecha de ${title} a ${shortDate.format(Date.parse(c.due_date.to))}` : `quitó la fecha de ${title}`;
    }
    if (c.assignee_id) {
      const to = c.assignee_id.to;
      return to ? `asignó ${title} a ${ctx.personName(to) ?? "otra persona"}` : `dejó ${title} sin responsable`;
    }
    if (c.label_ids) {
      const added = c.label_ids.to.filter((id) => !c.label_ids!.from.includes(id));
      const removed = c.label_ids.from.filter((id) => !c.label_ids!.to.includes(id));
      const name = (id: string) => `«${ctx.labelName(id) ?? "una etiqueta"}»`;
      if (added.length === 1 && removed.length === 0) return `añadió la etiqueta ${name(added[0])} a ${title}`;
      if (removed.length === 1 && added.length === 0) return `quitó la etiqueta ${name(removed[0])} de ${title}`;
      return `cambió las etiquetas de ${title}`;
    }
    if (c.archived_at && c.archived_at.to === null) return `desarchivó ${title}`;
  }
  const names: Record<string, string> = {
    title: "título",
    description: "descripción",
    priority: "prioridad",
    due_date: "fecha",
    assignee_id: "responsable",
    label_ids: "etiquetas",
    archived_at: "archivo",
  };
  const fields = keys.map((k) => names[k] ?? k);
  const list = fields.length > 1 ? `${fields.slice(0, -1).join(", ")} y ${fields.at(-1)}` : fields[0];
  return `editó ${title}${list ? ` (${list})` : ""}`;
}
