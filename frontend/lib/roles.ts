import type { Schemas } from "./api";

export type Role = Schemas["Role"];

export const roles: { value: Role; label: string; description: string }[] = [
  { value: "admin", label: "Admin", description: "Gestiona miembros, ajustes y tableros." },
  { value: "member", label: "Miembro", description: "Crea, mueve y edita tareas." },
  { value: "viewer", label: "Solo lectura", description: "Puede ver el tablero, sin editar." },
];

export const roleLabel = (role: Role) => roles.find((r) => r.value === role)?.label ?? role;

/** The roles to invite with, the usual one first (as in the design). */
export const INVITE_ROLES: { value: Role; label: string }[] = [
  { value: "member", label: "Miembro" },
  { value: "viewer", label: "Solo lectura" },
  { value: "admin", label: "Admin" },
];

// Mirrors the API's permission table, only to decide what to show. The API
// authorizes every request on its own.
export const canManageMembers = (role: Role | undefined) => role === "admin";
/** Create, rename and delete boards and columns. */
export const canManageBoards = (role: Role | undefined) => role === "admin";
/** Create, move, edit and delete tasks. */
export const canEditTasks = (role: Role | undefined) => role === "admin" || role === "member";
