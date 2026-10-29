"use client";

import { useRef, useState, type KeyboardEvent } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { cx } from "@/components/ui/cx";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { BOARD_TEMPLATES, DEFAULT_TEMPLATE, useCreateBoard, type Visibility, type WorkspaceDetail } from "@/lib/workspaces";

const ACCESS: { value: Visibility; label: string; description: (members: number) => string }[] = [
  {
    value: "workspace",
    label: "Todo el workspace",
    description: (n) => `${n === 1 ? "El único miembro puede" : `Los ${n} miembros pueden`} verlo según su rol.`,
  },
  { value: "private", label: "Solo personas invitadas", description: () => "Privado hasta que lo compartas. Los admins lo ven siempre." },
];

/** "Nuevo tablero" (design 2c): name, template and who may open it. */
export function NewBoardDialog({ workspace, open, onClose }: { workspace: WorkspaceDetail; open: boolean; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal open={open} busy={busy} width={600} title="Nuevo tablero" description={`En ${workspace.name}`} onClose={onClose}>
      {open && <NewBoardForm workspace={workspace} onClose={onClose} onBusy={setBusy} />}
    </Modal>
  );
}

function NewBoardForm({ workspace, onClose, onBusy }: { workspace: WorkspaceDetail; onClose: () => void; onBusy: (b: boolean) => void }) {
  const router = useRouter();
  const create = useCreateBoard(workspace.id);
  const [name, setName] = useState("");
  const [template, setTemplate] = useState(DEFAULT_TEMPLATE.id);
  const [visibility, setVisibility] = useState<Visibility>("workspace");
  const [error, setError] = useState<{ name?: string; form?: string }>({});
  const templateRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const submit = () => {
    if (!name.trim()) return setError({ name: "Escribe un nombre" });
    const columns = BOARD_TEMPLATES.find((t) => t.id === template)?.columns ?? [];
    onBusy(true);
    create.mutate(
      { name: name.trim(), columns, visibility },
      {
        onSuccess: (board) => {
          onBusy(false);
          onClose();
          router.push(`/boards/${board.id}`);
        },
        onError: (err) => {
          onBusy(false);
          if (err instanceof ApiError && err.fields?.name) setError({ name: err.fields.name });
          else setError({ form: err instanceof ApiError && err.status < 500 ? err.message : "No pudimos crear el tablero. Inténtalo de nuevo." });
        },
      },
    );
  };

  // The templates are a radio group: arrows move the choice.
  const onTemplateKey = (e: KeyboardEvent, i: number) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    const next = (i + step + BOARD_TEMPLATES.length) % BOARD_TEMPLATES.length;
    setTemplate(BOARD_TEMPLATES[next].id);
    templateRefs.current[next]?.focus();
  };

  return (
    <form
      noValidate
      className="flex flex-col gap-[18px]"
      onSubmit={(e) => {
        e.preventDefault();
        if (!create.isPending) submit();
      }}
    >
      {error.form && (
        <Notice tone="error" title="No se pudo crear" compact>
          {error.form}
        </Notice>
      )}
      <Input
        label="Nombre del tablero"
        value={name}
        autoFocus
        maxLength={100}
        autoComplete="off"
        error={error.name}
        readOnly={create.isPending}
        onChange={(e) => {
          setName(e.target.value);
          if (error.name) setError({});
        }}
      />

      <div className="flex flex-col gap-2">
        <span id="template-label" className="text-[13px] font-medium">
          Plantilla
        </span>
        <div role="radiogroup" aria-labelledby="template-label" className="grid gap-2.5 sm:grid-cols-3">
          {BOARD_TEMPLATES.map((t, i) => {
            const selected = t.id === template;
            return (
              <button
                key={t.id}
                ref={(el) => {
                  templateRefs.current[i] = el;
                }}
                type="button"
                role="radio"
                aria-checked={selected}
                tabIndex={selected ? 0 : -1}
                onClick={() => setTemplate(t.id)}
                onKeyDown={(e) => onTemplateKey(e, i)}
                className={cx(
                  "flex cursor-pointer flex-col gap-1.5 rounded-xl border-[1.5px] p-3 text-left",
                  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text",
                  selected ? "border-accent-text bg-accent-soft" : "border-border-strong",
                )}
              >
                <span className="text-sm font-semibold">{t.name}</span>
                <span className="text-xs leading-[1.4] text-muted">{t.description}</span>
                <span aria-hidden className="mt-1 flex min-h-7 gap-1">
                  {t.columns.map((c) => (
                    <span key={c} className="h-7 flex-1 rounded-[5px] border border-border bg-surface2" />
                  ))}
                </span>
                {t.columns.length > 0 && <span className="sr-only">Columnas: {t.columns.join(", ")}</span>}
              </button>
            );
          })}
        </div>
      </div>

      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-[13px] font-medium">Acceso</legend>
        {ACCESS.map((a) => (
          <label
            key={a.value}
            className={cx(
              "flex cursor-pointer items-start gap-2.5 rounded-[10px] border px-3 py-2.5",
              visibility === a.value ? "border-accent-text" : "border-border",
            )}
          >
            <input
              type="radio"
              name="board-access"
              value={a.value}
              checked={visibility === a.value}
              onChange={() => setVisibility(a.value)}
              className="mt-1 accent-[var(--accent-text)]"
            />
            <span>
              <span className="block text-[13.5px] font-semibold">{a.label}</span>
              <span className="block text-[12.5px] text-muted">{a.description(workspace.member_count)}</span>
            </span>
          </label>
        ))}
      </fieldset>

      <div className="-mx-6 -mb-5 flex justify-end gap-2.5 border-t border-border px-6 py-3.5">
        <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
          Cancelar
        </Button>
        <Button type="submit" variant="primary" loading={create.isPending}>
          Crear tablero
        </Button>
      </div>
    </form>
  );
}
