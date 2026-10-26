"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { cx } from "@/components/ui/cx";

const panelBox =
  "flex min-h-0 w-[440px] shrink-0 flex-col border-l border-border bg-surface outline-none max-lg:fixed max-lg:bottom-0 max-lg:right-0 max-lg:top-14 max-lg:z-40 max-lg:w-[min(440px,100vw)] max-lg:shadow-[var(--shadow-lg)]";
const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";

/** The board's side panel: a header with a status and a close button, and a scrolling body. */
export function PanelFrame({
  label,
  chip,
  status,
  live = "",
  onClose,
  children,
}: {
  label: string;
  chip?: string;
  status: ReactNode;
  live?: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  // Opening moves focus here, so the panel is read from its label.
  useEffect(() => ref.current?.focus(), []);

  return (
    <aside
      ref={ref}
      tabIndex={-1}
      aria-label={label}
      className={panelBox}
      onKeyDown={(e) => {
        // Menus inside stop their own Escape first.
        if (e.key === "Escape" && !e.defaultPrevented) onClose();
      }}
    >
      <div className="flex shrink-0 items-center gap-2.5 border-b border-border px-5 py-3">
        {chip && (
          <span className="max-w-[45%] truncate rounded-md border border-border px-2 py-0.5 text-xs text-muted">{chip}</span>
        )}
        <div className="min-w-0 flex-1 truncate">{status}</div>
        <span className="sr-only" role="status">
          {live}
        </span>
        <button
          type="button"
          aria-label="Cerrar panel"
          onClick={onClose}
          className={cx("flex size-[30px] items-center justify-center rounded-lg text-xl text-muted hover:bg-surface2", focusRing)}
        >
          ×
        </button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-[18px] overflow-y-auto px-5 pb-6 pt-[18px]">{children}</div>
    </aside>
  );
}

/** Stands in for a side panel while its code loads. */
export function PanelSkeleton() {
  return (
    <aside aria-busy="true" aria-label="Cargando panel" className={panelBox}>
      <div className="flex h-[55px] shrink-0 items-center border-b border-border px-5">
        <div className="h-4 w-24 animate-pulse rounded bg-border" />
      </div>
      <div className="flex flex-col gap-3 p-5">
        <div className="h-6 w-3/4 animate-pulse rounded bg-border" />
        <div className="h-4 w-1/2 animate-pulse rounded bg-border" />
        <div className="h-4 w-2/3 animate-pulse rounded bg-border" />
      </div>
    </aside>
  );
}
