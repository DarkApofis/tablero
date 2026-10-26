"use client";

import { useRef } from "react";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import type { Task } from "@/lib/api";
import type { Label } from "@/lib/boards";
import { hasFilters, UNASSIGNED, type TaskFilters } from "@/lib/tasks";
import { CheckboxMenu, type CheckboxOption } from "./checkbox-menu";

type Props = {
  labels: Label[];
  tasks: Task[];
  filters: TaskFilters;
  onChange: (filters: TaskFilters) => void;
  shown: number;
};

export function FilterBar({ labels, tasks, filters, onChange, shown }: Props) {
  const barRef = useRef<HTMLDivElement>(null);
  const active = hasFilters(filters);

  const labelOptions: CheckboxOption[] = labels.map((l) => ({
    value: l.id,
    label: l.name,
    icon: <span aria-hidden className="size-2.5 shrink-0 rounded-full" style={{ background: l.color }} />,
    count: tasks.filter((t) => t.labels.some((tl) => tl.id === l.id)).length,
  }));

  return (
    <div
      ref={barRef}
      role="group"
      aria-label="Filtros"
      className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-2.5 sm:px-5"
    >
      <CheckboxMenu
        label="Etiquetas"
        legend="Filtrar por etiquetas"
        options={labelOptions}
        selected={filters.labels}
        onChange={(ids) => onChange({ ...filters, labels: ids })}
        emptyText="Este tablero no tiene etiquetas."
      />
      <CheckboxMenu
        label="Responsable"
        legend="Filtrar por responsable"
        options={assigneeOptions(tasks)}
        selected={filters.assignees}
        onChange={(ids) => onChange({ ...filters, assignees: ids })}
        emptyText="Todavía no hay tareas."
      />
      {active && (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            onChange({ labels: [], assignees: [] });
            // The button disappears with the filters; keep focus in the bar.
            barRef.current?.querySelector("button")?.focus();
          }}
        >
          Limpiar filtros
        </Button>
      )}
      <p role="status" className="ml-auto text-[12.5px] text-muted tabular-nums">
        {!active
          ? plural(tasks.length)
          : shown === 0
            ? "Ninguna tarea coincide con los filtros"
            : `${shown} de ${plural(tasks.length)}`}
      </p>
    </div>
  );
}

/** Everyone who has a task on the board, by name, then "Sin responsable". */
function assigneeOptions(tasks: Task[]): CheckboxOption[] {
  const people = new Map<string, CheckboxOption & { count: number }>();
  let unassigned = 0;
  for (const t of tasks) {
    if (!t.assignee) {
      unassigned++;
      continue;
    }
    const option = people.get(t.assignee.id);
    if (option) option.count++;
    else
      people.set(t.assignee.id, {
        value: t.assignee.id,
        label: t.assignee.name,
        icon: (
          <span aria-hidden>
            <Avatar name={t.assignee.name} size={20} />
          </span>
        ),
        count: 1,
      });
  }
  const options = [...people.values()].sort((a, b) => a.label.localeCompare(b.label, "es"));
  if (unassigned > 0) {
    options.push({
      value: UNASSIGNED,
      label: "Sin responsable",
      icon: <span aria-hidden className="size-5 shrink-0 rounded-full border-[1.5px] border-dashed border-border-strong" />,
      count: unassigned,
    });
  }
  return options;
}

function plural(n: number) {
  return n === 1 ? "1 tarea" : `${n} tareas`;
}
