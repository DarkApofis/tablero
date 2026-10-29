"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { isEmail, splitEmails } from "@/components/ui/email-chips";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { INVITE_ROLES, roleLabel, type Role } from "@/lib/roles";
import { relative, useNow } from "@/lib/time";
import {
  inviteUrl,
  useCancelInvitation,
  useCreateInviteLink,
  useDeleteInviteLink,
  useInvitations,
  useInvite,
  useInviteLink,
  useRenewInvitation,
  type WorkspaceDetail,
} from "@/lib/workspaces";

function failure(err: unknown) {
  if (err instanceof ApiError && (err.status < 500 || err.code === "network_error")) return err.message;
  return "Algo salió mal. Inténtalo de nuevo en un momento.";
}

// Rounded: a 7-day link reads "7 días" though a few seconds went by (or the clocks differ).
const days = (ms: number) => Math.max(1, Math.round(ms / 86_400_000));

/**
 * "Invitar a <workspace>" (design 2d): by email, with a role; by a link to
 * share; and the invitations still pending. No email is sent.
 */
export function InviteDialog({ workspace, open, onClose }: { workspace: WorkspaceDetail; open: boolean; onClose: () => void }) {
  return (
    <Modal
      open={open}
      width={580}
      title={`Invitar a ${workspace.name}`}
      onClose={onClose}
      footer={<Button onClick={onClose}>Listo</Button>}
    >
      {open && <InviteBody workspace={workspace} />}
    </Modal>
  );
}

function InviteBody({ workspace }: { workspace: WorkspaceDetail }) {
  const invite = useInvite();
  const [text, setText] = useState("");
  const [role, setRole] = useState<Role>(workspace.default_role);
  const [error, setError] = useState<string>();
  const [result, setResult] = useState<string | null>(null);

  const send = () => {
    const emails = splitEmails(text);
    const bad = emails.find((e) => !isEmail(e));
    if (emails.length === 0) return setError("Escribe al menos un correo");
    if (bad) return setError(`«${bad}» no es un correo válido`);
    setError(undefined);
    invite.mutate(
      { workspaceId: workspace.id, emails, role },
      {
        onSuccess: (res) => {
          setText("");
          const parts = [
            res.added.length > 0 && `${res.added.map((m) => m.name).join(", ")} ya ${res.added.length === 1 ? "es miembro" : "son miembros"}`,
            res.invited.length > 0 &&
              `${res.invited.length === 1 ? "1 invitación pendiente" : `${res.invited.length} invitaciones pendientes`} hasta que se registren`,
            res.already_members.length > 0 &&
              `${res.already_members.length === 1 ? "1 persona ya estaba" : `${res.already_members.length} personas ya estaban`} en el workspace`,
          ].filter(Boolean);
          setResult(parts.join(" · "));
        },
        onError: (err) => setError(err instanceof ApiError && err.fields?.emails ? err.fields.emails : failure(err)),
      },
    );
  };

  return (
    <>
      {result && (
        <Notice tone="success" title="Invitación hecha" compact>
          {result}
        </Notice>
      )}
      <form
        noValidate
        className="flex flex-col gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (!invite.isPending) send();
        }}
      >
        <div className="flex flex-wrap gap-2">
          <Input
            aria-label="Correos"
            type="text"
            inputMode="email"
            autoComplete="off"
            placeholder="nombre@empresa.com, otro@empresa.com"
            value={text}
            readOnly={invite.isPending}
            aria-invalid={error ? true : undefined}
            onChange={(e) => {
              setText(e.target.value);
              setError(undefined);
            }}
            className="min-w-[220px] flex-1"
          />
          <label className="sr-only" htmlFor="invite-role">
            Rol de los invitados
          </label>
          <select
            id="invite-role"
            value={role}
            onChange={(e) => setRole(e.target.value as Role)}
            className="h-[38px] cursor-pointer rounded-[10px] border border-border-strong bg-raised px-3 text-[13.5px] text-text focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text"
          >
            {INVITE_ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
          <Button type="submit" variant="primary" loading={invite.isPending}>
            Enviar
          </Button>
        </div>
        {error ? (
          <span className="text-[12.5px] text-danger">! {error}</span>
        ) : (
          <span className="text-[12px] text-faint">
            No enviamos correos: quien ya tiene cuenta entra al momento; el resto, al registrarse con ese correo.
          </span>
        )}
      </form>
      <LinkSection workspace={workspace} />
      <Pending workspaceId={workspace.id} />
    </>
  );
}

/** "O comparte un enlace": copy it, make a new one, or turn it off. */
function LinkSection({ workspace }: { workspace: WorkspaceDetail }) {
  const link = useInviteLink(workspace.id);
  const create = useCreateInviteLink(workspace.id);
  const remove = useDeleteInviteLink(workspace.id);
  const now = useNow(60_000);
  const [copied, setCopied] = useState(false);
  const current = link.data;
  const expired = current ? Date.parse(current.expires_at) <= now : false;
  const url = current ? inviteUrl(current.token) : "";

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Without clipboard access the link is still there to select.
    }
  };

  return (
    <section className="flex flex-col gap-2" aria-labelledby="invite-link-title">
      <h3 id="invite-link-title" className="text-[13px] font-medium">
        O comparte un enlace
      </h3>
      {link.isPending ? (
        <div className="h-[38px] animate-pulse rounded-[10px] bg-border" />
      ) : current && !expired ? (
        <>
          <div className="flex items-center gap-2">
            <code className="flex h-[38px] min-w-0 flex-1 items-center overflow-hidden whitespace-nowrap rounded-[10px] border border-dashed border-border-strong px-3 font-mono text-[12.5px] text-muted">
              {url.replace(/^https?:\/\//, "")}
            </code>
            <Button onClick={copy} aria-live="polite">
              {copied ? "Copiado" : "Copiar"}
            </Button>
          </div>
          <span className="text-[12px] text-faint">
            Entran como {roleLabel(current.role)} · caduca en {days(Date.parse(current.expires_at) - now)}{" "}
            {days(Date.parse(current.expires_at) - now) === 1 ? "día" : "días"} ·{" "}
            <button
              type="button"
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
              className="cursor-pointer rounded text-accent-text underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-accent-text"
            >
              Desactivar enlace
            </button>
          </span>
        </>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button loading={create.isPending} onClick={() => create.mutate(workspace.default_role)}>
            {expired ? "Crear un enlace nuevo" : "Crear enlace"}
          </Button>
          <span className="text-[12px] text-faint">
            {expired ? "El enlace anterior caducó." : `Entrarán como ${roleLabel(workspace.default_role)}; caduca en 7 días.`}
          </span>
        </div>
      )}
      {(create.error || remove.error) && <span className="text-[12.5px] text-danger">! {failure(create.error ?? remove.error)}</span>}
    </section>
  );
}

/** Invitations by email waiting for their account: renew or cancel each. */
function Pending({ workspaceId }: { workspaceId: string }) {
  const invitations = useInvitations(workspaceId);
  const renew = useRenewInvitation(workspaceId);
  const cancel = useCancelInvitation(workspaceId);
  const now = useNow(60_000);
  if (!invitations.data || invitations.data.length === 0) return null;

  return (
    <section className="flex flex-col gap-2" aria-labelledby="pending-title">
      <h3 id="pending-title" className="text-[13px] font-medium">
        Pendientes
      </h3>
      <ul className="rounded-xl border border-border">
        {invitations.data.map((inv) => {
          const late = Date.parse(inv.expires_at) <= now;
          const busy = (renew.isPending && renew.variables === inv.id) || (cancel.isPending && cancel.variables === inv.id);
          return (
            <li key={inv.id} className="flex flex-wrap items-center gap-3 border-b border-border px-3 py-2.5 last:border-b-0">
              <div className="min-w-0 flex-1">
                <div className="truncate text-[13.5px]">{inv.email}</div>
                <div className={late ? "text-xs text-warn" : "text-xs text-faint"}>
                  {roleLabel(inv.role)} · {late ? `Caducó · ${relative(inv.expires_at, now)}` : `Invitada ${relative(inv.created_at, now)}`}
                </div>
              </div>
              <Button size="sm" disabled={busy} onClick={() => renew.mutate(inv.id)} aria-label={`Renovar la invitación de ${inv.email}`}>
                Renovar
              </Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => cancel.mutate(inv.id)} aria-label={`Cancelar la invitación de ${inv.email}`}>
                Cancelar
              </Button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
