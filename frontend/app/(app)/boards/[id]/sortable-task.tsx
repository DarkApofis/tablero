"use client";

import type { KeyboardEvent } from "react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { cx } from "@/components/ui/cx";
import type { Task } from "@/lib/api";
import { TaskCard } from "./task-card";

type Props = {
  task: Task;
  today: Date;
  /** False for viewers: the card only opens. */
  draggable: boolean;
  /** Its last move is still saving. */
  saving: boolean;
  /** Its side panel is open. */
  open: boolean;
  onOpen: () => void;
};

const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-accent-text";
const openRing = "shadow-[0_0_0_2px_var(--accent)]";

export function SortableTask({ task, today, draggable, saving, open, onOpen }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging, active } = useSortable({
    id: task.id,
    data: { type: "task" },
    disabled: !draggable || saving,
  });

  if (!draggable) {
    return (
      <li>
        <div
          role="button"
          tabIndex={0}
          aria-expanded={open}
          data-task-id={task.id}
          onClick={onOpen}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              onOpen();
            }
          }}
          className={cx("cursor-pointer rounded-xl", focusRing, open && openRing)}
        >
          <TaskCard task={task} today={today} />
        </div>
      </li>
    );
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    // While a card is being dragged, Enter drops it; the sensor handles that.
    if (e.key === "Enter" && !active) {
      e.preventDefault();
      onOpen();
      return;
    }
    listeners?.onKeyDown?.(e);
  };

  return (
    <li>
      <div
        ref={setNodeRef}
        style={{ transform: CSS.Translate.toString(transform), transition }}
        {...attributes}
        {...listeners}
        onKeyDown={onKeyDown}
        // dnd-kit swallows the click that ends a drag, so this is a plain click.
        onClick={onOpen}
        aria-roledescription="tarea arrastrable"
        aria-expanded={open}
        // Saving a move only stops dragging; the card still opens.
        aria-disabled={undefined}
        data-task-id={task.id}
        aria-busy={saving || undefined}
        className={cx(
          "rounded-xl",
          focusRing,
          saving ? "cursor-progress opacity-80" : "cursor-grab touch-manipulation",
          open && !isDragging && openRing,
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
