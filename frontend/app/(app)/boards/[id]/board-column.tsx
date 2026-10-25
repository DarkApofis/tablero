import type { Task } from "@/lib/api";
import type { BoardColumn as Column } from "@/lib/boards";
import { TaskCard } from "./task-card";

// Shared by the columns and their skeletons.
export const columnBox =
  "relative flex max-h-full w-[292px] shrink-0 snap-start flex-col gap-2.5 rounded-[14px] border border-border bg-surface p-2.5 max-sm:w-[calc(100vw-52px)]";

type Props = {
  column: Column;
  /** The column's tasks that pass the filters. */
  tasks: Task[];
  filtered: boolean;
  today: Date;
};

export function BoardColumn({ column, tasks, filtered, today }: Props) {
  const headingId = `column-${column.id}`;
  const total = column.tasks.length;
  const count = filtered ? `${tasks.length} de ${total} tareas` : total === 1 ? "1 tarea" : `${total} tareas`;

  return (
    <section aria-labelledby={headingId} className={columnBox}>
      <header className="flex shrink-0 items-center gap-2 py-0.5 pl-1.5 pr-1">
        <h2 id={headingId} className="min-w-0 flex-1 truncate text-[13.5px] font-semibold tracking-[.01em]">
          {column.name}
        </h2>
        <span className="rounded-full border border-border bg-surface2 px-2 py-px text-xs font-semibold tabular-nums text-muted">
          <span aria-hidden>{filtered ? `${tasks.length}/${total}` : total}</span>
          <span className="sr-only">{count}</span>
        </span>
      </header>
      {tasks.length === 0 ? (
        <div className="flex flex-col items-center gap-1.5 rounded-xl border-[1.5px] border-dashed border-border-strong px-4 py-[22px] text-center">
          <p className="text-sm font-semibold">{total === 0 ? "Sin tareas" : "Sin coincidencias"}</p>
          <p className="max-w-[210px] text-[12.5px] leading-normal text-muted [text-wrap:pretty]">
            {total === 0
              ? "Todavía no hay tareas en esta columna."
              : "Ninguna tarea de esta columna coincide con los filtros."}
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
            <li key={t.id}>
              <TaskCard task={t} today={today} />
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
