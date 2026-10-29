"use client";

import { useEffect, useId, useRef, type FormEvent, type ReactNode } from "react";
import { cx } from "./cx";

type ModalProps = {
  open: boolean;
  title: string;
  /** A line under the title. */
  description?: ReactNode;
  children: ReactNode;
  /** The bottom bar, usually Cancel and the main action. */
  footer?: ReactNode;
  /** Escape, the × and the backdrop close it, unless busy. */
  onClose: () => void;
  busy?: boolean;
  /** With onSubmit the body is a form, so Enter submits it. */
  onSubmit?: () => void;
  width?: number;
};

/**
 * A dialog on the native <dialog>, like ConfirmDialog: focus moves in and is
 * trapped, Escape closes it, and focus goes back to what opened it.
 */
export function Modal({ open, title, description, children, footer, onClose, busy = false, onSubmit, width = 540 }: ModalProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const textId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
    // Next keeps the page navigated away from mounted but hidden: an open
    // modal there would stay in the top layer and block the new page.
    return () => {
      if (dialog.open) dialog.close();
    };
  }, [open]);

  const body = (
    <>
      <div className="flex flex-col gap-4 overflow-y-auto px-6 py-5">{children}</div>
      {footer && <div className="flex flex-wrap justify-end gap-2.5 border-t border-border px-6 py-3.5">{footer}</div>}
    </>
  );

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={description ? textId : undefined}
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) onClose();
      }}
      onClick={(e) => {
        // A click on the backdrop lands on the dialog itself.
        if (e.target === e.currentTarget && !busy) onClose();
      }}
      style={{ width: `min(${width}px, calc(100vw - 32px))` }}
      className="m-auto max-h-[calc(100dvh-32px)] rounded-[18px] border border-border-strong bg-surface p-0 text-text shadow-pop backdrop:bg-black/55 open:flex open:flex-col"
    >
      <div className="flex items-start justify-between gap-3 px-6 pt-[22px]">
        <div className="min-w-0">
          <h2 id={titleId} className="text-xl font-semibold tracking-[-.01em]">
            {title}
          </h2>
          {description && (
            <p id={textId} className="mt-1.5 text-[13.5px] text-muted">
              {description}
            </p>
          )}
        </div>
        <button
          type="button"
          aria-label="Cerrar"
          disabled={busy}
          onClick={onClose}
          className="flex size-[30px] shrink-0 cursor-pointer items-center justify-center rounded-lg text-xl text-muted hover:bg-surface2 hover:text-text focus-visible:outline-2 focus-visible:outline-accent-text disabled:cursor-not-allowed"
        >
          ×
        </button>
      </div>
      {onSubmit ? (
        <form
          noValidate
          className={cx("flex min-h-0 flex-col")}
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            if (!busy) onSubmit();
          }}
        >
          {body}
        </form>
      ) : (
        <div className="flex min-h-0 flex-col">{body}</div>
      )}
    </dialog>
  );
}
