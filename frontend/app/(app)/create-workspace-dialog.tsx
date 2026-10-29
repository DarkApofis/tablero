"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { EmailChips, isEmail } from "@/components/ui/email-chips";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Notice } from "@/components/ui/notice";
import { Segmented } from "@/components/ui/segmented";
import { ApiError } from "@/lib/api";
import { INVITE_ROLES, type Role } from "@/lib/roles";
import { slugify } from "@/lib/slug";
import { checkSlug, useCreateWorkspace, useInvite } from "@/lib/workspaces";
import { AddressField, useSlugStatus } from "./address-field";

/**
 * "Crear workspace" (design 2a): name, address, people to invite and their
 * role. Whoever creates it is its admin and owner; then it opens.
 */
export function CreateWorkspaceDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      open={open}
      busy={busy}
      title="Crear workspace"
      description="Un espacio para tu equipo y sus tableros. Serás admin."
      onClose={onClose}
    >
      {/* Mounted while open only, so it starts empty every time. */}
      {open && <CreateWorkspaceForm onClose={onClose} onBusy={setBusy} />}
    </Modal>
  );
}

function CreateWorkspaceForm({ onClose, onBusy }: { onClose: () => void; onBusy: (busy: boolean) => void }) {
  const router = useRouter();
  const create = useCreateWorkspace();
  const [name, setName] = useState("");
  // Until the user types an address it follows the name: at once from the
  // name, then the free one the API would give it.
  const [typedSlug, setTypedSlug] = useState<string | null>(null);
  const [suggestion, setSuggestion] = useState({ name: "", slug: "" });
  const slug = typedSlug ?? (suggestion.name === name ? suggestion.slug : slugify(name));
  const [emails, setEmails] = useState<string[]>([]);
  const [role, setRole] = useState<Role>("member");
  const [errors, setErrors] = useState<{ name?: string; slug?: string; form?: string }>({});
  const [created, setCreated] = useState<{ id: string } | null>(null);
  const invite = useInvite();
  // A suggested address is only checked once the API suggested it (it is free then).
  const { status, problem } = useSlugStatus(typedSlug ?? (suggestion.name === name ? suggestion.slug : ""));
  const busy = create.isPending || invite.isPending;
  useEffect(() => onBusy(busy), [busy, onBusy]);

  useEffect(() => {
    if (typedSlug !== null || !name.trim()) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      checkSlug({ name }, controller.signal)
        .then((r) => setSuggestion({ name, slug: r.slug }))
        .catch(() => {});
    }, 300);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [name, typedSlug]);

  const sendInvites = async (workspaceId: string) => {
    if (emails.length === 0) return true;
    try {
      await invite.mutateAsync({ workspaceId, emails, role });
      return true;
    } catch (err) {
      setErrors({ form: err instanceof ApiError && err.status < 500 ? err.message : "No pudimos enviar las invitaciones." });
      setCreated({ id: workspaceId });
      return false;
    }
  };

  const submit = async () => {
    const next: typeof errors = {};
    if (!name.trim()) next.name = "Escribe un nombre";
    // An address the user typed must be valid and free; one that follows the
    // name is left to the API, which numbers it if it is taken meanwhile.
    if (typedSlug !== null && problem) next.slug = problem;
    else if (typedSlug !== null && status === "taken") next.slug = "Ya la usa otro workspace";
    if (emails.some((e) => !isEmail(e)) || Object.keys(next).length > 0) {
      setErrors(next);
      return;
    }
    setErrors({});
    try {
      const ws = await create.mutateAsync({ name: name.trim(), slug: typedSlug ?? undefined });
      if (await sendInvites(ws.id)) {
        onClose();
        router.push(`/workspaces/${ws.id}`);
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === "slug_taken") setErrors({ slug: err.message });
      else if (err instanceof ApiError && err.fields) setErrors({ name: err.fields.name, slug: err.fields.slug });
      else setErrors({ form: err instanceof ApiError && err.status < 500 ? err.message : "No pudimos crear el workspace. Inténtalo de nuevo." });
    }
  };

  // The workspace exists but the invitations failed: say so, and let them go on.
  if (created) {
    return (
      <>
        <Notice tone="error" title="El workspace se creó, pero no pudimos invitar a nadie" compact>
          {errors.form} Puedes volver a invitar desde el workspace, con «Invitar».
        </Notice>
        <div className="flex justify-end gap-2.5">
          <Button
            variant="primary"
            onClick={() => {
              onClose();
              router.push(`/workspaces/${created.id}`);
            }}
          >
            Ir al workspace
          </Button>
        </div>
      </>
    );
  }

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (!busy) void submit();
      }}
    >
      {errors.form && (
        <Notice tone="error" title="No se pudo crear" compact>
          {errors.form}
        </Notice>
      )}
      <Input
        label="Nombre del workspace"
        value={name}
        autoFocus
        maxLength={100}
        autoComplete="off"
        error={errors.name}
        readOnly={busy}
        onChange={(e) => {
          setName(e.target.value);
          if (errors.name) setErrors({ ...errors, name: undefined });
        }}
      />
      <AddressField
        value={slug}
        status={status}
        error={errors.slug ?? problem}
        disabled={busy}
        onChange={(s) => {
          setTypedSlug(s);
          if (errors.slug) setErrors({ ...errors, slug: undefined });
        }}
      />
      <EmailChips
        label="Invitar personas"
        hint="· opcional"
        placeholder="nombre@empresa.com"
        value={emails}
        onChange={setEmails}
        disabled={busy}
      />
      <Segmented label="Rol de los invitados" options={INVITE_ROLES} value={role} onChange={setRole} disabled={busy} />
      <p className="-mt-1 text-[12.5px] text-faint">
        No enviamos correos: quien ya tiene cuenta entra al momento, y el resto cuando se registre con ese correo.
      </p>
      <div className="-mx-6 -mb-5 mt-1 flex justify-end gap-2.5 border-t border-border px-6 py-3.5">
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancelar
        </Button>
        <Button type="submit" variant="primary" loading={busy}>
          Crear workspace
        </Button>
      </div>
    </form>
  );
}
