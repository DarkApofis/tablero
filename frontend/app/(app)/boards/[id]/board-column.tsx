"use client";

import { useDroppable } from "@dnd-kit/core";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { cx } from "@/components/ui/cx";
import type { Task } from "@/lib/api";
import type { BoardColumn as Column } from "@/lib/boards";
import { SortableTask } from "./sortable-task";
import { columnBox } from "./styles";

type Props = {
  column: Column;
  /** The column's tasks that pass the filters, in the order shown. */
  tasks: Task[];
  /** All of the column's tasks, ignoring the filters and any drag in progress. */
  total: number;
  filtered: boolean;
  today: Date;
  /** Whether the user may move tasks at all. */
  canDrag: boolean;
  /** Tasks whose move is still saving; they can't be dragged again until it lands. */
  pendingIds: string[];
  openId: string | null;
  onOpen: (taskId: string) => void;
  /** Whether a dragged task is over this column. */
  isTarget: boolean;
};

export function BoardColumn(props: Props) {
  const { column, tasks, total, filtered, today, canDrag, pendingIds, openId, onOpen, isTarget } = props;
  const headingId = `column-${column.id}`;
  // The whole column takes drops, so an empty one (or its empty end) can too.
  const { setNodeRef } = useDroppable({ id: column.id, data: { type: "column" }, disabled: !canDrag });
  const shown = tasks.length;
  const noMatches = filtered && total > 0;
  const count = filtered ? `${shown} de ${total} tareas` : shown === 1 ? "1 tarea" : `${shown} tareas`;

  return (
    <section
      ref={setNodeRef}
      aria-labelledby={headingId}
      className={cx(columnBox, isTarget && "border-dashed border-accent-text")}
    >
      <header className="flex shrink-0 items-center gap-2 py-0.5 pl-1.5 pr-1">
        <h2 id={headingId} className="min-w-0 flex-1 truncate text-[13.5px] font-semibold tracking-[.01em]">
          {column.name}
        </h2>
        <span className="rounded-full border border-border bg-surface2 px-2 py-px text-xs font-semibold tabular-nums text-muted">
          <span aria-hidden>{filtered ? `${shown}/${total}` : shown}</span>
          <span className="sr-only">{count}</span>
        </span>
      </header>
      <SortableContext id={column.id} items={tasks.map((t) => t.id)} strategy={verticalListSortingStrategy}>
        {tasks.length === 0 ? (
          <div
            className={cx(
              "flex flex-col items-center gap-1.5 rounded-xl border-[1.5px] border-dashed px-4 py-[22px] text-center",
              isTarget ? "border-accent-text bg-accent-soft" : "border-border-strong",
            )}
          >
            <p className="text-sm font-semibold">{noMatches ? "Sin coincidencias" : "Sin tareas"}</p>
            <p className="max-w-[210px] text-[12.5px] leading-normal text-muted [text-wrap:pretty]">
              {noMatches
                ? "Ninguna tarea de esta columna coincide con los filtros."
                : canDrag
                  ? "Arrastra una tarea hasta aquí."
                  : "Todavía no hay tareas en esta columna."}
            </p>
          </div>
        ) : (
          // Focusable so a keyboard can scroll a long column.
          <ol
            tabIndex={0}
            aria-labelledby={headingId}
            className="flex min-h-0 flex-col gap-2.5 overflow-y-auto rounded-xl p-0.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
          >
            {tasks.map((t) => (
              <SortableTask
                key={t.id}
                task={t}
                today={today}
                draggable={canDrag}
                saving={pendingIds.includes(t.id)}
                open={openId === t.id}
                onOpen={() => onOpen(t.id)}
              />
            ))}
          </ol>
        )}
      </SortableContext>
    </section>
  );
}
