import { Avatar } from "@/components/ui/avatar";
import { cx } from "@/components/ui/cx";
import { Tag } from "@/components/ui/tag";
import type { Task } from "@/lib/api";
import { describeDue, priorities, type DueTone, type Priority } from "@/lib/tasks";

const dueTones: Record<DueTone, string> = {
  late: "font-semibold text-danger",
  soon: "font-semibold text-warn",
  normal: "text-faint",
};

const priorityTones: Record<Priority, string> = {
  low: "text-faint",
  medium: "text-muted",
  high: "text-warn",
  urgent: "text-danger",
};

type Props = {
  task: Task;
  today: Date;
  /** The copy that follows the pointer while dragging; it skips the ids. */
  overlay?: boolean;
};

export function TaskCard({ task, today, overlay = false }: Props) {
  const due = task.due_date ? describeDue(task.due_date, today) : null;
  const titleId = overlay ? undefined : `task-${task.id}`;

  return (
    <article
      aria-labelledby={titleId}
      className={cx(
        "relative flex flex-col gap-2.5 rounded-xl border bg-surface2 px-3 pb-2.5 pt-3",
        overlay ? "border-accent" : due?.tone === "late" ? "border-danger/40" : "border-border",
      )}
    >
      {task.labels.length > 0 && (
        <ul aria-label="Etiquetas" className="flex flex-wrap gap-1.5">
          {task.labels.map((l) => (
            <li key={l.id}>
              <Tag label={l.name} color={l.color} />
            </li>
          ))}
        </ul>
      )}
      <h3 id={titleId} className="break-words text-sm font-medium leading-[1.4] [text-wrap:pretty]">
        {task.title}
      </h3>
      <div className="flex min-h-6 items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <PriorityBadge priority={task.priority} />
          {due && (
            <time dateTime={task.due_date!} className={cx("inline-flex items-center gap-1", dueTones[due.tone])}>
              {due.tone === "late" && <span aria-hidden>!</span>}
              <span className="sr-only">Fecha límite: </span>
              {due.text}
            </time>
          )}
        </div>
        {task.assignee ? (
          <span className="flex items-center">
            <span className="sr-only">Responsable: </span>
            <Avatar name={task.assignee.name} size={24} />
          </span>
        ) : (
          <span
            role="img"
            aria-label="Sin responsable"
            title="Sin responsable"
            className="size-6 shrink-0 rounded-full border-[1.5px] border-dashed border-border-strong"
          />
        )}
      </div>
    </article>
  );
}

export function PriorityBadge({ priority }: { priority: Priority }) {
  const { label, level } = priorities[priority];
  return (
    <span className={cx("inline-flex items-center gap-1.5 font-medium", priorityTones[priority])}>
      <svg aria-hidden width="12" height="12" viewBox="0 0 12 12" className="shrink-0">
        {[0, 1, 2, 3].map((i) => (
          <rect
            key={i}
            x={i * 3}
            y={9 - i * 2.5}
            width="2"
            height={3 + i * 2.5}
            rx="0.5"
            fill="currentColor"
            opacity={i < level ? 1 : 0.25}
          />
        ))}
      </svg>
      <span className="sr-only">Prioridad </span>
      {label}
    </span>
  );
}
