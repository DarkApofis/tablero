"use client";

import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { cx } from "@/components/ui/cx";
import type { Task } from "@/lib/api";
import { TaskCard } from "./task-card";

type Props = {
  task: Task;
  today: Date;
  /** False for viewers: the card is just a card. */
  draggable: boolean;
  /** Its last move is still saving. */
  saving: boolean;
};

export function SortableTask({ task, today, draggable, saving }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: task.id,
    data: { type: "task" },
    disabled: !draggable || saving,
  });

  if (!draggable) {
    return (
      <li>
        <TaskCard task={task} today={today} />
      </li>
    );
  }

  return (
    <li>
      <div
        ref={setNodeRef}
        style={{ transform: CSS.Translate.toString(transform), transition }}
        {...attributes}
        {...listeners}
        aria-roledescription="tarea arrastrable"
        data-task-id={task.id}
        aria-busy={saving || undefined}
        className={cx(
          "rounded-xl focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-accent-text",
          saving ? "cursor-progress opacity-80" : "cursor-grab touch-manipulation",
          // The spot it will land on, the size of the card it stands for.
          isDragging && "bg-accent-soft outline-[1.5px] outline-offset-[-1.5px] outline-dashed outline-accent-text",
        )}
      >
        <div className={cx(isDragging && "opacity-0")}>
          <TaskCard task={task} today={today} />
        </div>
      </div>
    </li>
  );
}
