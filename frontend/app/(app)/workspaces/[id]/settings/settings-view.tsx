"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { cx } from "@/components/ui/cx";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { ApiError } from "@/lib/api";
import { INVITE_ROLES, type Role } from "@/lib/roles";
import {
  useDeleteWorkspace,
  useMembers,
  useTransferWorkspace,
  useUpdateWorkspace,
  type WorkspaceDetail,
} from "@/lib/workspaces";
import { AddressField, useSlugStatus } from "../../../address-field";
import { useWorkspaceShell } from "../workspace-shell";

function failure(err: unknown) {
  if (err instanceof ApiError && (err.status < 500 || err.code === "network_error")) return err.message;
  return "Algo salió mal. Inténtalo de nuevo en un momento.";
}

/** "Ajustes del workspace" (design 2e), for admins. */
export function SettingsView() {
  const { workspace, isAdmin } = useWorkspaceShell();
  if (!isAdmin) {
    return (
      <Notice tone="info" title="Solo los admins pueden cambiar los ajustes">
        Pide a un admin del workspace que haga el cambio.
      </Notice>
    );
  }
  return (
    <div className="flex max-w-[680px] flex-col gap-5">
      <SettingsForm workspace={workspace} />
      <DangerZone workspace={workspace} />
    </div>
  );
}

function SettingsForm({ workspace }: { workspace: WorkspaceDetail }) {
  const update = useUpdateWorkspace(workspace.id);
  const [name, setName] = useState(workspace.name);
  const [slug, setSlug] = useState(workspace.slug ?? "");
  const [description, setDescription] = useState(workspace.description);
  const [role, setRole] = useState<Role>(workspace.default_role);
  const [errors, setErrors] = useState<Record<string, string | undefined>>({});
  const [saved, setSaved] = useState(false);
  const { status, problem } = useSlugStatus(slug, workspace.slug);
  const dirty =
    name !== workspace.name || slug !== (workspace.slug ?? "") || description !== workspace.description || role !== workspace.default_role;

  const save = () => {
    if (!name.trim()) return setErrors({ name: "Escribe un nombre" });
    if (problem || status === "taken") return setErrors({ slug: problem ?? "Ya la usa otro workspace" });
    setErrors({});
    update.mutate(
      {
        name: name.trim(),
        description,
        default_role: role,
        ...(slug !== (workspace.slug ?? "") && { slug }),
      },
      {
        onSuccess: () => setSaved(true),
        onError: (err) => {
          if (err instanceof ApiError && err.code === "slug_taken") setErrors({ slug: err.message });
          else if (err instanceof ApiError && err.fields) setErrors(err.fields);
          else setErrors({ form: failure(err) });
        },
      },
    );
  };

  return (
    <form
      noValidate
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!update.isPending) save();
      }}
    >
      <div>
        <h2 className="text-[22px] font-semibold tracking-[-.02em]">Ajustes del workspace</h2>
        <p className="mt-1.5 text-sm text-muted">Solo los admins pueden cambiar estos datos.</p>
      </div>
      {errors.form && (
        <Notice tone="error" title="No se guardaron los cambios" compact>
          {errors.form}
        </Notice>
      )}
      <Input
        label="Nombre"
        value={name}
        maxLength={100}
        error={errors.name}
        onChange={(e) => {
          setName(e.target.value);
          setSaved(false);
        }}
      />
      <AddressField
        value={slug}
        status={status}
        error={errors.slug ?? problem}
        onChange={(s) => {
          setSlug(s);
          setSaved(false);
          setErrors({ ...errors, slug: undefined });
        }}
      />
      <div className="flex flex-col gap-1.5">
        <label htmlFor="ws-description" className="text-[13px] font-medium">
          Descripción
        </label>
        <textarea
          id="ws-description"
          rows={2}
          maxLength={500}
          value={description}
          aria-invalid={errors.description ? true : undefined}
          onChange={(e) => {
            setDescription(e.target.value);
            setSaved(false);
          }}
          className={cx(
            "resize-y rounded-[10px] border bg-surface px-3 py-2.5 font-[inherit] text-sm text-text placeholder:text-faint",
            "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text",
            errors.description ? "border-danger" : "border-control-border",
          )}
          placeholder="Para qué es este workspace"
        />
        {errors.description && <span className="text-[12.5px] text-danger">! {errors.description}</span>}
      </div>
      <Segmented
        label="Rol por defecto de los nuevos miembros"
        options={INVITE_ROLES}
        value={role}
        onChange={(r) => {
          setRole(r);
          setSaved(false);
        }}
        className="max-w-[420px]"
      />
      <p className="-mt-3 text-[12.5px] text-faint">El que proponen las invitaciones y con el que entra quien usa un enlace nuevo.</p>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" variant="primary" loading={update.isPending} disabled={!dirty && !update.isPending}>
          Guardar cambios
        </Button>
        <span aria-live="polite" className="text-[12.5px]">
          {dirty ? <span className="text-warn">● Cambios sin guardar</span> : saved && <span className="text-accent-text">✓ Guardado</span>}
        </span>
      </div>
    </form>
  );
}

/** Transfer ownership and delete the workspace. */
function DangerZone({ workspace }: { workspace: WorkspaceDetail }) {
  const [dialog, setDialog] = useState<"transfer" | "delete" | null>(null);
  const tasks = workspace.task_count === 1 ? "1 tarea" : `${workspace.task_count} tareas`;
  const boards = workspace.board_count === 1 ? "su tablero" : `sus ${workspace.board_count} tableros`;

  return (
    <section aria-labelledby="danger-title" className="rounded-[14px] border border-danger/40">
      <h2 id="danger-title" className="border-b border-border px-4 py-3 text-[13px] font-semibold text-danger">
        Zona de peligro
      </h2>
      <div className="flex flex-wrap items-center gap-4 border-b border-border px-4 py-3.5">
        <div className="min-w-[200px] flex-1">
          <div className="text-sm font-semibold">Transferir propiedad</div>
          <div className="text-[12.5px] text-muted">
            {workspace.may_transfer
              ? "Otro admin pasará a ser el propietario."
              : "Solo el propietario puede transferir el workspace."}
          </div>
        </div>
        <Button size="sm" disabled={!workspace.may_transfer} onClick={() => setDialog("transfer")}>
          Transferir
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-4 px-4 py-3.5">
        <div className="min-w-[200px] flex-1">
          <div className="text-sm font-semibold">Eliminar workspace</div>
          <div className="text-[12.5px] text-muted">
            {workspace.is_default
              ? "Es el workspace al que entran las cuentas nuevas: no se puede eliminar."
              : `Se borran ${boards} y ${tasks}. No se puede deshacer.`}
          </div>
        </div>
        <Button size="sm" variant="danger" disabled={workspace.is_default} onClick={() => setDialog("delete")}>
          Eliminar
        </Button>
      </div>
      <TransferDialog workspace={workspace} open={dialog === "transfer"} onClose={() => setDialog(null)} />
      <DeleteWorkspaceDialog workspace={workspace} open={dialog === "delete"} onClose={() => setDialog(null)} />
    </section>
  );
}

function TransferDialog({ workspace, open, onClose }: { workspace: WorkspaceDetail; open: boolean; onClose: () => void }) {
  const members = useMembers(workspace.id);
  const transfer = useTransferWorkspace(workspace.id);
  const admins = (members.data ?? []).filter((m) => m.role === "admin" && m.user_id !== workspace.owner_id);
  const [target, setTarget] = useState<string | null>(null);
  const chosen = admins.find((m) => m.user_id === target) ?? admins[0];

  return (
    <Modal
      open={open}
      busy={transfer.isPending}
      title="Transferir propiedad"
      description="El nuevo propietario podrá transferirlo y eliminarlo; tú seguirás siendo admin."
      onClose={onClose}
      onSubmit={() => chosen && transfer.mutate(chosen.user_id, { onSuccess: onClose })}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={transfer.isPending}>
            Cancelar
          </Button>
          <Button type="submit" variant="primary" loading={transfer.isPending} disabled={!chosen}>
            Transferir
          </Button>
        </>
      }
    >
      {transfer.error && (
        <Notice tone="error" title="No se pudo transferir" compact>
          {failure(transfer.error)}
        </Notice>
      )}
      {admins.length === 0 ? (
        <p className="text-sm text-muted">
          No hay otro admin. Dale el rol de admin a alguien en «Miembros» y vuelve aquí.
        </p>
      ) : (
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-[13px] font-medium">Nuevo propietario</legend>
          {admins.map((m) => (
            <label
              key={m.user_id}
              className={cx(
                "flex cursor-pointer items-center gap-2.5 rounded-[10px] border px-3 py-2.5 text-sm",
                chosen?.user_id === m.user_id ? "border-accent-text bg-accent-soft" : "border-border",
              )}
            >
              <input
                type="radio"
                name="new-owner"
                checked={chosen?.user_id === m.user_id}
                onChange={() => setTarget(m.user_id)}
                className="accent-[var(--accent-text)]"
              />
              <span className="font-medium">{m.name}</span>
              <span className="truncate text-muted">{m.email}</span>
            </label>
          ))}
        </fieldset>
      )}
    </Modal>
  );
}

/** Deleting asks for the workspace's name, typed: it takes everything with it. */
function DeleteWorkspaceDialog({ workspace, open, onClose }: { workspace: WorkspaceDetail; open: boolean; onClose: () => void }) {
  const router = useRouter();
  const remove = useDeleteWorkspace(workspace.id);
  const [typed, setTyped] = useState("");
  const matches = typed.trim() === workspace.name;

  return (
    <Modal
      open={open}
      busy={remove.isPending}
      width={480}
      title={`¿Eliminar «${workspace.name}»?`}
      description="Se borran sus tableros, tareas, miembros e invitaciones. No se puede deshacer."
      onClose={() => {
        setTyped("");
        onClose();
      }}
      onSubmit={() => matches && remove.mutate(undefined, { onSuccess: () => router.replace("/") })}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={remove.isPending}>
            Cancelar
          </Button>
          <Button type="submit" variant="danger" loading={remove.isPending} disabled={!matches}>
            Eliminar workspace
          </Button>
        </>
      }
    >
      {remove.error && (
        <Notice tone="error" title="No se pudo eliminar" compact>
          {failure(remove.error)}
        </Notice>
      )}
      <Input
        label={`Escribe «${workspace.name}» para confirmar`}
        value={typed}
        autoComplete="off"
        onChange={(e) => setTyped(e.target.value)}
      />
    </Modal>
  );
}
