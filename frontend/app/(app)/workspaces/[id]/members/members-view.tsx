"use client";

import { useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { useMe } from "@/lib/auth";
import { canManageMembers, roleLabel, type Role } from "@/lib/roles";
import { useChangeRole, useMembers, useRemoveMember, type Member } from "@/lib/workspaces";
import { useWorkspaceShell } from "../workspace-shell";
import { RoleMenu } from "./role-menu";

type Pending =
  | { kind: "remove"; member: Member }
  | { kind: "demote-self"; member: Member; role: Role }
  | null;

export function MembersView({ workspaceId }: { workspaceId: string }) {
  const router = useRouter();
  const { data: me } = useMe();
  const members = useMembers(workspaceId);
  const { workspace } = useWorkspaceShell();
  const changeRole = useChangeRole(workspaceId);
  const removeMember = useRemoveMember(workspaceId);
  const [confirm, setConfirm] = useState<Pending>(null);
  const [failure, setFailure] = useState<{ title: string; text: string } | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);

  if (members.isPending || !me) return <MembersSkeleton />;

  if (members.error) {
    const forbidden = members.error instanceof ApiError && members.error.status === 403;
    return (
      <Notice
        tone="error"
        title={forbidden ? "No tienes acceso a este workspace" : "No pudimos cargar los miembros"}
        actions={
          forbidden ? (
            <Link
              href="/"
              // The error notice's tinted background: plain text color, underlined, keeps 4.5:1.
              className="rounded text-[13px] font-semibold text-text underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
            >
              Volver a tus workspaces
            </Link>
          ) : (
            <Button size="sm" variant="primary" loading={members.isRefetching} onClick={() => members.refetch()}>
              Reintentar
            </Button>
          )
        }
      >
        {forbidden ? "Puede que te hayan quitado de él o que ya no exista." : describe(members.error)}
      </Notice>
    );
  }

  const list = members.data;
  const myRole = list.find((m) => m.user_id === me.id)?.role;
  const isAdmin = canManageMembers(myRole);
  const adminCount = list.filter((m) => m.role === "admin").length;
  const busyId = changeRole.isPending
    ? changeRole.variables.userId
    : removeMember.isPending
      ? removeMember.variables
      : null;

  const onError = (err: unknown) => {
    if (err instanceof ApiError && err.code === "last_admin") {
      setFailure({ title: "El workspace necesita al menos un admin", text: err.message });
    } else if (err instanceof ApiError && err.code === "owner_stays_admin") {
      setFailure({ title: "El propietario sigue siendo admin", text: err.message });
    } else if (err instanceof ApiError && err.status === 403) {
      setFailure({ title: "Tu rol ya no permite gestionar miembros", text: "Actualizamos la lista con tus permisos actuales." });
    } else if (err instanceof ApiError && err.code === "member_not_found") {
      setFailure({ title: "Esa persona ya no es miembro", text: "Actualizamos la lista." });
    } else {
      setFailure({ title: "No pudimos guardar el cambio", text: describe(err) });
    }
    if (err instanceof ApiError && err.status < 500 && err.status > 0) void members.refetch();
  };

  const applyRole = (member: Member, role: Role) => {
    setFailure(null);
    changeRole.mutate(
      { userId: member.user_id, role },
      {
        onError,
        onSuccess: () => {
          // Demoting yourself removes the controls, focus included.
          if (member.user_id === me.id && role !== "admin") requestAnimationFrame(() => headingRef.current?.focus());
        },
        onSettled: () => setConfirm(null),
      },
    );
  };

  const requestRole = (member: Member, role: Role) => {
    if (member.user_id === me.id && member.role === "admin" && role !== "admin") {
      setConfirm({ kind: "demote-self", member, role });
    } else {
      applyRole(member, role);
    }
  };

  const remove = (member: Member) => {
    setFailure(null);
    const leaving = member.user_id === me.id;
    removeMember.mutate(member.user_id, {
      onError,
      onSuccess: () => {
        if (leaving) router.replace("/");
        // The button that opened the dialog went away with the row.
        else requestAnimationFrame(() => headingRef.current?.focus());
      },
      onSettled: () => setConfirm(null),
    });
  };

  return (
    <>
      <div>
        <h2 ref={headingRef} tabIndex={-1} className="text-[22px] font-semibold tracking-[-.02em] outline-none">
          Miembros
        </h2>
        <p className="mt-1.5 text-sm text-muted">{list.length === 1 ? "1 persona" : `${list.length} personas`}</p>
        {!isAdmin && (
          <p className="mt-2 text-[13px] text-faint">Solo los admins pueden cambiar roles o quitar miembros.</p>
        )}
      </div>

      {failure && (
        <Notice tone="error" title={failure.title} compact>
          {failure.text}
        </Notice>
      )}

      {list.length === 0 ? (
        <p className="rounded-[14px] border-[1.5px] border-dashed border-border-strong p-8 text-center text-muted">
          Este workspace no tiene miembros.
        </p>
      ) : (
        <ul className="rounded-[14px] border border-border bg-surface">
          {list.map((m) => {
            const isMe = m.user_id === me.id;
            const isOwner = m.user_id === workspace.owner_id;
            const lastAdmin = m.role === "admin" && adminCount === 1;
            // The owner stays an admin until they hand the workspace over (Ajustes).
            const locked = lastAdmin || isOwner;
            const busy = busyId === m.user_id;
            return (
              <li
                key={m.user_id}
                className="flex flex-wrap items-center gap-x-3.5 gap-y-2 border-b border-border px-4 py-3 last:border-b-0"
              >
                <Avatar name={m.name} size={36} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-semibold">
                    {m.name}
                    {isMe && <span className="font-normal text-faint"> (tú)</span>}
                    {isOwner && <span className="font-normal text-faint"> · Propietario</span>}
                  </div>
                  <div className="truncate text-[12.5px] text-muted">{m.email}</div>
                </div>
                <div className="flex items-center gap-2">
                  {isAdmin ? (
                    <>
                      {lastAdmin && !isOwner && <span className="text-xs text-faint">Único admin</span>}
                      <RoleMenu
                        value={m.role}
                        memberName={m.name}
                        disabled={locked || (busyId !== null && !busy)}
                        pending={busy && changeRole.isPending}
                        onSelect={(role) => requestRole(m, role)}
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={locked || busyId !== null}
                        title={
                          isOwner
                            ? "El propietario no puede salir ni ser quitado: antes transfiere el workspace"
                            : lastAdmin
                              ? "No se puede quitar al único admin"
                              : undefined
                        }
                        aria-label={isMe ? "Salir del workspace" : `Quitar a ${m.name}`}
                        onClick={() => setConfirm({ kind: "remove", member: m })}
                      >
                        {isMe ? "Salir" : "Quitar"}
                      </Button>
                    </>
                  ) : (
                    <span className="rounded-md border border-border-strong px-2 py-0.5 text-xs font-semibold text-muted">
                      {roleLabel(m.role)}
                    </span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <ConfirmDialog
        open={confirm?.kind === "remove"}
        tone="danger"
        title={
          confirm?.member.user_id === me.id ? "¿Salir del workspace?" : `¿Quitar a ${confirm?.member.name ?? ""}?`
        }
        confirmLabel={confirm?.member.user_id === me.id ? "Salir" : "Quitar"}
        pending={removeMember.isPending}
        onCancel={() => setConfirm(null)}
        onConfirm={() => confirm && remove(confirm.member)}
      >
        {confirm?.member.user_id === me.id
          ? "Perderás el acceso a sus tableros. Para volver, un admin tendrá que agregarte."
          : "Perderá el acceso a los tableros del workspace y sus tareas quedarán sin responsable."}
      </ConfirmDialog>

      <ConfirmDialog
        open={confirm?.kind === "demote-self"}
        title="¿Dejar de ser admin?"
        confirmLabel={`Cambiar a ${confirm?.kind === "demote-self" ? roleLabel(confirm.role) : ""}`}
        pending={changeRole.isPending}
        onCancel={() => setConfirm(null)}
        onConfirm={() => confirm?.kind === "demote-self" && applyRole(confirm.member, confirm.role)}
      >
        Ya no podrás gestionar miembros ni tableros de este workspace. Solo otro admin podrá devolverte el rol.
      </ConfirmDialog>
    </>
  );
}

function describe(err: unknown) {
  if (err instanceof ApiError && err.code === "network_error") return err.message;
  return "Algo salió mal. Inténtalo de nuevo en un momento.";
}

export function MembersSkeleton() {
  return (
    <div className="flex flex-col gap-[22px]" role="status" aria-busy="true">
      <span className="sr-only">Cargando miembros…</span>
      <div className="flex flex-col gap-2">
        <div className="h-7 w-40 animate-pulse rounded bg-border" />
        <div className="h-4 w-56 animate-pulse rounded bg-border" />
      </div>
      <ul className="rounded-[14px] border border-border bg-surface">
        {[0, 1, 2, 3].map((i) => (
          <li key={i} className="flex items-center gap-3.5 border-b border-border px-4 py-3 last:border-b-0">
            <div className="size-9 animate-pulse rounded-full bg-border" />
            <div className="flex flex-1 flex-col gap-1.5">
              <div className="h-4 w-32 animate-pulse rounded bg-border" />
              <div className="h-3 w-48 animate-pulse rounded bg-border" />
            </div>
            <div className="h-[34px] w-[140px] animate-pulse rounded-[9px] bg-border" />
          </li>
        ))}
      </ul>
    </div>
  );
}
