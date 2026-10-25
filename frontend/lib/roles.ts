import type { Schemas } from "./api";

export type Role = Schemas["Role"];

export const roles: { value: Role; label: string; description: string }[] = [
  { value: "admin", label: "Admin", description: "Gestiona miembros, ajustes y tableros." },
  { value: "member", label: "Miembro", description: "Crea, mueve y edita tareas." },
  { value: "viewer", label: "Solo lectura", description: "Puede ver el tablero, sin editar." },
];

export const roleLabel = (role: Role) => roles.find((r) => r.value === role)?.label ?? role;

// Mirrors the API's permission table, only to decide what to show. The API
// authorizes every request on its own.
export const canManageMembers = (role: Role | undefined) => role === "admin";
