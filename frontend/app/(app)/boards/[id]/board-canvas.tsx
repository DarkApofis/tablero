"use client";

import { useEffect, useRef, useState } from "react";
import {
  closestCenter,
  DndContext,
  DragOverlay,
  getFirstCollision,
  KeyboardSensor,
  MouseSensor,
  pointerWithin,
  rectIntersection,
  TouchSensor,
  useSensor,
  useSensors,
  type Announcements,
  type CollisionDetection,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
  type KeyboardCoordinateGetter,
  type UniqueIdentifier,
} from "@dnd-kit/core";
import { arrayMove } from "@dnd-kit/sortable";
import { ApiError, isTaskConflict, type Task } from "@/lib/api";
import { useMoveTask, type BoardDetail } from "@/lib/boards";
import { BoardColumn } from "./board-column";
import type { Toast } from "./board-toast";
import { TaskCard } from "./task-card";

/** Task ids per column id, in the order shown. */
type Items = Record<string, string[]>;

const instructions = {
  draggable:
    "Pulsa Enter para abrir la tarea. Para moverla, pulsa Espacio: con las flechas arriba y abajo cambia de " +
    "posición, con izquierda y derecha cambia de columna. Pulsa Espacio o Enter para soltarla o Escape para cancelar.",
};

type Props = {
  board: BoardDetail;
  /** Per column, the tasks that pass the filters. */
  visible: Task[][];
  filtered: boolean;
  today: Date;
  /** The task open in the side panel. */
  openId: string | null;
  onOpen: (taskId: string) => void;
  notify: (toast: Toast | null) => void;
};

export function BoardCanvas({ board, visible, filtered, today, openId, onOpen, notify }: Props) {
  const canDrag = board.role !== "viewer";
  const { move, pendingIds } = useMoveTask(board.id);
  // Only set during a drag: where each card is shown while it moves.
  const [dragItems, setDragItems] = useState<Items | null>(null);
  const [activeId, setActiveId] = useState<string | null>(null);
  // After a drop, the board the order above was dragged over. The moved card keeps
  // its new spot until the cache, which notifies a tick later, has it too.
  const [heldFor, setHeldFor] = useState<BoardDetail | null>(null);
  // The announcements read it inside dnd-kit's handlers, before a re-render.
  const itemsRef = useRef<Items | null>(null);
  // The first "over" after picking a card up is the card itself; announcing it
  // would replace the pick-up message.
  const justPickedUp = useRef(false);
  const lastOverId = useRef<UniqueIdentifier | null>(null);
  // Set right after a card changes column, while the layout settles.
  const justMoved = useRef(false);

  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 6 } }),
    // A short press, so a swipe still scrolls the board.
    useSensor(TouchSensor, { activationConstraint: { delay: 220, tolerance: 6 } }),
    useSensor(KeyboardSensor, {
      // Enter opens the task, so only Space picks a card up.
      keyboardCodes: { start: ["Space"], cancel: ["Escape"], end: ["Space", "Enter"] },
      coordinateGetter: (event, args) => keyboardCoordinates.current(event, args),
    }),
  );
  // The sensor keeps the getter it was created with; this one reads fresh state.
  const keyboardCoordinates = useRef<KeyboardCoordinateGetter>(() => undefined);

  // Dragging across the page would otherwise select the text it passes over.
  const dragging = activeId !== null;
  useEffect(() => {
    if (!dragging) return;
    document.body.style.userSelect = "none";
    return () => {
      document.body.style.userSelect = "";
    };
  }, [dragging]);

  const tasks = new Map(board.columns.flatMap((c) => c.tasks).map((t) => [t.id, t]));
  const columnName = new Map(board.columns.map((c) => [c.id, c.name]));
  const baseItems: Items = Object.fromEntries(board.columns.map((c, i) => [c.id, visible[i].map((t) => t.id)]));
  const items = dragItems && (activeId !== null || heldFor === board) ? dragItems : baseItems;

  const setItems = (next: Items | null) => {
    itemsRef.current = next;
    setDragItems(next);
  };
  useEffect(() => {
    justMoved.current = false;
  }, [dragItems]);
  const columnOf = (id: UniqueIdentifier, its: Items) =>
    String(id) in its ? String(id) : Object.keys(its).find((c) => its[c].includes(String(id)));

  // Up and down walk the column; left and right jump to the next column at the
  // same height. dnd-kit's sortableKeyboardCoordinates treats the card's own column
  // as "to the left" of it (the column starts a padding further left) and stays put.
  useEffect(() => {
    keyboardCoordinates.current = (event, { context: { active, over, droppableRects, collisionRect } }) => {
      const its = itemsRef.current;
      if (!its || !active || !collisionRect) return undefined;
      const id = String(active.id);
      const column = columnOf(id, its);
      if (!column) return undefined;
      const index = visibleIndex(its, column, id, over?.id);
      if (event.code === "ArrowUp" || event.code === "ArrowDown") {
        event.preventDefault();
        const down = event.code === "ArrowDown";
        // Over the card at index t (its own placeholder included), it shows at t.
        // Rects are measured without the sorting transforms, so they follow the list.
        const rect = droppableRects.get(its[column][down ? index + 1 : index - 1] ?? "");
        if (!rect) return undefined;
        return { x: rect.left, y: down ? rect.bottom - collisionRect.height : rect.top };
      }
      if (event.code === "ArrowLeft" || event.code === "ArrowRight") {
        event.preventDefault();
        const order = Object.keys(its);
        const target = order[order.indexOf(column) + (event.code === "ArrowLeft" ? -1 : 1)];
        if (!target) return undefined;
        const cards = its[target];
        const card = cards.length > 0 ? droppableRects.get(cards[Math.min(index, cards.length - 1)]) : undefined;
        // Land where the card's placeholder will be: on the card it takes the place
        // of, or past the last one (the gap between cards is 10px). Once the
        // placeholder is there, it is what the card is over.
        if (card) return { x: card.left, y: index < cards.length ? card.top : card.bottom + 10 };
        const box = droppableRects.get(target);
        // An empty column: its drop area sits under the header.
        return box ? { x: box.left + 10, y: box.top + 44 } : undefined;
      }
      return undefined;
    };
  });

  // dnd-kit's recipe for several lists: what is under the pointer wins, so a short
  // column next to a tall one still gets the drop. Over a column with cards, the
  // nearest card decides the spot.
  const collisionDetection: CollisionDetection = (args) => {
    const its = itemsRef.current ?? baseItems;
    const underPointer = pointerWithin(args);
    let overId = getFirstCollision(underPointer.length > 0 ? underPointer : rectIntersection(args), "id");
    if (overId != null) {
      const cards = String(overId) in its ? its[String(overId)] : null;
      if (cards && cards.length > 0) {
        overId =
          closestCenter({
            ...args,
            droppableContainers: args.droppableContainers.filter(
              (c) => c.id !== overId && cards.includes(String(c.id)),
            ),
          })[0]?.id ?? overId;
      }
      lastOverId.current = overId;
      return [{ id: overId }];
    }
    // Just moved to another column: stay on the card until the layout catches up.
    if (justMoved.current && activeId) lastOverId.current = activeId;
    return lastOverId.current != null ? [{ id: lastOverId.current }] : [];
  };

  const onDragStart = ({ active }: DragStartEvent) => {
    notify(null);
    setActiveId(String(active.id));
    justPickedUp.current = true;
    setItems(baseItems);
  };

  // Between columns, the card moves as soon as it crosses into another one.
  const onDragOver = ({ active, over }: DragOverEvent) => {
    const its = itemsRef.current;
    if (!its || !over) return;
    const from = columnOf(active.id, its);
    const to = columnOf(over.id, its);
    if (!from || !to || from === to) return;
    const target = its[to];
    const overIndex = target.indexOf(String(over.id));
    const translated = active.rect.current.translated;
    const below = translated !== null && translated.top > over.rect.top + over.rect.height / 2;
    const index = overIndex >= 0 ? overIndex + (below ? 1 : 0) : target.length;
    justMoved.current = true;
    setItems({
      ...its,
      [from]: its[from].filter((id) => id !== active.id),
      [to]: [...target.slice(0, index), String(active.id), ...target.slice(index)],
    });
  };

  const onDragEnd = ({ active, over }: DragEndEvent) => {
    const its = itemsRef.current;
    const id = String(active.id);
    lastOverId.current = null;
    setActiveId(null);
    const task = tasks.get(id);
    const column = its && over ? columnOf(id, its) : undefined;
    if (!its || !task || !column || !over) return setItems(null);

    let list = its[column];
    const overIndex = list.indexOf(String(over.id));
    if (overIndex >= 0) list = arrayMove(list, list.indexOf(id), overIndex);
    const index = list.indexOf(id);
    if (column === task.column_id && baseItems[column].indexOf(id) === index) return setItems(null); // where it was

    setItems({ ...its, [column]: list });
    setHeldFor(board);

    // The neighbors the user saw; with filters on, hidden tasks may sit between them.
    move(
      task,
      { columnId: column, afterTaskId: list[index - 1] ?? null, beforeTaskId: list[index + 1] ?? null },
      {
        onError: (err) => {
          // The card is put back, maybe in another column, which remounts it.
          if ((document.activeElement as HTMLElement | null)?.dataset.taskId === task.id) {
            setTimeout(() => document.querySelector<HTMLElement>(`[data-task-id="${task.id}"]`)?.focus());
          }
          if (isTaskConflict(err)) {
            const where = columnName.get(err.current.column_id);
            notify({
              tone: "conflict",
              title: "Alguien movió esta tarjeta",
              text: where ? `«${task.title}» está ahora en ${where}.` : `«${task.title}» volvió a su sitio.`,
            });
          } else {
            notify({ tone: "error", title: `No pudimos mover «${task.title}»`, text: describeMoveError(err) });
          }
        },
      },
    );
  };

  const onDragCancel = () => {
    lastOverId.current = null;
    setActiveId(null);
    setItems(null);
  };

  // dnd-kit announces right after running the handler above it, so itemsRef already
  // holds the result: a card that changed column is in its new list, and after a
  // drop the list is the final order.
  const titleOf = (id: UniqueIdentifier) => `«${tasks.get(String(id))?.title ?? "la tarea"}»`;
  const placeOf = (activeId: UniqueIdentifier, overId?: UniqueIdentifier) => {
    const its = itemsRef.current ?? baseItems;
    const column = columnOf(activeId, its);
    if (!column) return "fuera de las columnas";
    const list = its[column];
    const at = overId === undefined ? list.indexOf(String(activeId)) : visibleIndex(its, column, String(activeId), overId);
    return `la posición ${at + 1} de ${list.length} de ${columnName.get(column)}`;
  };
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Levantaste ${titleOf(active.id)}, en ${placeOf(active.id)}.`,
    onDragOver: ({ active, over }) => {
      const first = justPickedUp.current;
      justPickedUp.current = false;
      if (first && over?.id === active.id) return undefined;
      return over
        ? `${titleOf(active.id)} sobre ${placeOf(active.id, over.id)}.`
        : `${titleOf(active.id)} no está sobre ninguna columna.`;
    },
    onDragEnd: ({ active, over }) =>
      over ? `Soltaste ${titleOf(active.id)} en ${placeOf(active.id)}.` : `${titleOf(active.id)} no se movió.`,
    onDragCancel: ({ active }) => `Movimiento cancelado. ${titleOf(active.id)} vuelve a su sitio.`,
  };

  const activeTask = activeId ? tasks.get(activeId) : undefined;
  const overColumn = activeId ? columnOf(activeId, items) : undefined;

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={collisionDetection}
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDragEnd={onDragEnd}
      onDragCancel={onDragCancel}
      accessibility={{ announcements, screenReaderInstructions: instructions }}
    >
      {/* Focusable so the arrow keys scroll it sideways. */}
      <div
        role="region"
        aria-label="Columnas del tablero"
        tabIndex={0}
        className="flex min-h-0 min-w-0 flex-1 items-start gap-4 overflow-x-auto scroll-px-5 p-5 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent-text max-sm:snap-x max-sm:snap-mandatory max-sm:scroll-px-3 max-sm:gap-2.5 max-sm:p-3"
      >
        {board.columns.map((c) => (
          <BoardColumn
            key={c.id}
            column={c}
            tasks={items[c.id].map((id) => tasks.get(id)!).filter(Boolean)}
            total={c.tasks.length}
            filtered={filtered}
            today={today}
            canDrag={canDrag}
            pendingIds={pendingIds}
            openId={openId}
            onOpen={onOpen}
            isTarget={overColumn === c.id && overColumn !== activeTask?.column_id}
          />
        ))}
      </div>

      <DragOverlay>
        {activeTask && (
          <div
            aria-hidden
            className="cursor-grabbing rounded-xl shadow-[0_24px_48px_rgba(0,0,0,.45),0_0_0_1px_var(--accent)] motion-safe:rotate-[2.5deg] motion-safe:scale-[1.02]"
          >
            <TaskCard task={activeTask} today={today} overlay />
          </div>
        )}
      </DragOverlay>

    </DndContext>
  );
}

/**
 * Where the dragged card shows in its column. Within a column the list only
 * reorders on drop; until then the card takes the place of the one it is over.
 */
function visibleIndex(its: Items, column: string, activeId: string, overId?: UniqueIdentifier) {
  const list = its[column];
  const over = overId === undefined ? -1 : list.indexOf(String(overId));
  return over >= 0 && String(overId) !== activeId ? over : list.indexOf(activeId);
}

function describeMoveError(err: Error) {
  if (!(err instanceof ApiError)) return "Algo salió mal. Inténtalo de nuevo en un momento.";
  if (err.code === "network_error") return err.message;
  if (err.status === 403) return "Tu rol ya no permite mover tareas. Actualizamos el tablero.";
  if (err.status === 404) return "La tarea ya no existe. Actualizamos el tablero.";
  if (err.status === 422) return `${err.message}. Actualizamos el tablero.`;
  return "Algo salió mal. La tarea volvió a su sitio.";
}
