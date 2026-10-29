"use client";

import { useRef, useState, type FormEvent } from "react";
import { ApiError } from "@/lib/api";
import { Button } from "./button";
import { cx } from "./cx";
import { Input } from "./input";

type Props = {
  /** The field's label; with `hideLabel`, read by screen readers only. */
  label: string;
  hideLabel?: boolean;
  placeholder?: string;
  submitLabel: string;
  maxLength?: number;
  /** Creates the thing; a rejection shows its message under the field. */
  onCreate: (name: string) => Promise<unknown>;
  /** Shown with Escape, and as a "Cancelar" button. */
  onCancel?: () => void;
  autoFocus?: boolean;
  /** One line (field and button side by side) or stacked, for narrow places. */
  layout?: "inline" | "stacked";
  className?: string;
};

/**
 * A form that creates something from a name: a workspace, a board, a column
 * or a task. It clears and keeps focus after each success, so several can be
 * added in a row.
 */
export function CreateForm({
  label,
  hideLabel = false,
  placeholder,
  submitLabel,
  maxLength = 100,
  onCreate,
  onCancel,
  autoFocus,
  layout = "inline",
  className,
}: Props) {
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const name = value.trim();
    if (!name) {
      setError("Escribe un nombre");
      input.current?.focus();
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await onCreate(name);
      setValue("");
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
      input.current?.focus();
    }
  };

  return (
    <form
      noValidate
      onSubmit={submit}
      onKeyDown={(e) => {
        if (e.key === "Escape" && onCancel) {
          e.stopPropagation();
          onCancel();
        }
      }}
      className={cx("flex gap-2", layout === "inline" ? "items-start" : "flex-col", className)}
    >
      <Input
        ref={input}
        label={hideLabel ? undefined : label}
        aria-label={hideLabel ? label : undefined}
        placeholder={placeholder}
        value={value}
        maxLength={maxLength}
        autoFocus={autoFocus}
        autoComplete="off"
        error={error}
        // Read-only, not disabled: a disabled field drops focus, and it must
        // keep it for the next one.
        readOnly={busy}
        aria-busy={busy || undefined}
        onChange={(e) => {
          setValue(e.target.value);
          if (error) setError(undefined);
        }}
        className={layout === "inline" ? "min-w-0 flex-1" : undefined}
      />
      <div className={cx("flex gap-2", layout === "inline" && !hideLabel && "mt-[23px]")}>
        <Button type="submit" variant="primary" loading={busy}>
          {submitLabel}
        </Button>
        {onCancel && (
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            Cancelar
          </Button>
        )}
      </div>
    </form>
  );
}

// The API's messages are written for people (Spanish, with the reason); a
// server error or a lost connection get one of ours.
function describe(err: unknown): string {
  if (err instanceof ApiError) {
    const field = err.fields && Object.values(err.fields)[0];
    if (field) return field;
    if (err.code === "network_error") return err.message;
    if (err.status < 500) return err.message;
  }
  return "No se pudo crear. Inténtalo de nuevo en un momento.";
}
