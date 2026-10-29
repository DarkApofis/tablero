"use client";

import { useEffect, useId, useRef, type ReactNode } from "react";
import { Button } from "./button";

type ConfirmDialogProps = {
  open: boolean;
  title: string;
  children?: ReactNode;
  confirmLabel: string;
  tone?: "primary" | "danger";
  pending?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
};

/** Modal confirmation on the native <dialog>: focus trap, Escape and backdrop come built in. */
export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  tone = "primary",
  pending = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const textId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
    // A hidden page (see Modal) must not keep its modal open.
    return () => {
      if (dialog.open) dialog.close();
    };
  }, [open]);

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={children ? textId : undefined}
      onCancel={(e) => {
        e.preventDefault(); // Escape: let the parent close it, unless busy
        if (!pending) onCancel();
      }}
      className="m-auto w-[min(420px,calc(100vw-32px))] rounded-2xl border border-border-strong bg-raised p-5 text-text shadow-pop backdrop:bg-black/50"
    >
      <h2 id={titleId} className="text-base font-semibold">
        {title}
      </h2>
      {children && (
        <div id={textId} className="mt-2 text-[13.5px] leading-normal text-muted">
          {children}
        </div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onCancel} disabled={pending} autoFocus>
          Cancelar
        </Button>
        <Button variant={tone} loading={pending} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </dialog>
  );
}
