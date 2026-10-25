import type { Schemas, Task } from "./api";

export type Priority = Schemas["Priority"];

export const priorities: Record<Priority, { label: string; level: number }> = {
  low: { label: "Baja", level: 1 },
  medium: { label: "Media", level: 2 },
  high: { label: "Alta", level: 3 },
  urgent: { label: "Urgente", level: 4 },
};

/** Days from today to a task's due date. Inside this window it is "due soon". */
export const DUE_SOON_DAYS = 2;

export type DueTone = "late" | "soon" | "normal";

const monthDay = new Intl.DateTimeFormat("es", { day: "numeric", month: "short", timeZone: "UTC" });
const monthDayYear = new Intl.DateTimeFormat("es", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
const DAY_MS = 86_400_000;

/**
 * Describes a due date ("AAAA-MM-DD", a calendar day with no time zone) relative to
 * `today` in the viewer's local time.
 */
export function describeDue(due: string, today = new Date()) {
  const [y, m, d] = due.split("-").map(Number);
  const dueUtc = Date.UTC(y, m - 1, d);
  const days = Math.round((dueUtc - Date.UTC(today.getFullYear(), today.getMonth(), today.getDate())) / DAY_MS);
  const date = (y === today.getFullYear() ? monthDay : monthDayYear).format(dueUtc).replace(".", "");

  if (days < 0) {
    const text = days === -1 ? "Venció ayer" : days >= -6 ? `Venció hace ${-days} días` : `Venció el ${date}`;
    return { tone: "late" as DueTone, text };
  }
  if (days <= DUE_SOON_DAYS) {
    return { tone: "soon" as DueTone, text: days === 0 ? "Vence hoy" : days === 1 ? "Vence mañana" : `Vence en ${days} días` };
  }
  return { tone: "normal" as DueTone, text: date };
}

/** Filter value for tasks without an assignee. */
export const UNASSIGNED = "none";

export type TaskFilters = { labels: string[]; assignees: string[] };

export function readFilters(params: URLSearchParams): TaskFilters {
  return { labels: unique(params.getAll("label")), assignees: unique(params.getAll("assignee")) };
}

export function writeFilters(params: URLSearchParams, filters: TaskFilters) {
  const next = new URLSearchParams(params);
  next.delete("label");
  next.delete("assignee");
  filters.labels.forEach((id) => next.append("label", id));
  filters.assignees.forEach((id) => next.append("assignee", id));
  return next;
}

export function hasFilters(f: TaskFilters) {
  return f.labels.length > 0 || f.assignees.length > 0;
}

/** Any of the chosen labels and any of the chosen assignees. */
export function matchesFilters(task: Task, f: TaskFilters) {
  if (f.labels.length > 0 && !task.labels.some((l) => f.labels.includes(l.id))) return false;
  if (f.assignees.length > 0 && !f.assignees.includes(task.assignee?.id ?? UNASSIGNED)) return false;
  return true;
}

function unique(values: string[]) {
  return [...new Set(values.filter(Boolean))];
}
