"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { cx } from "@/components/ui/cx";

export type FilterOption = { value: string; label: string; icon?: ReactNode; count: number };

type Props = {
  label: string;
  options: FilterOption[];
  selected: string[];
  onChange: (selected: string[]) => void;
  emptyText: string;
};

/** A disclosure button with a panel of checkboxes; several options can be on at once. */
export function FilterMenu({ label, options, selected, onChange, emptyText }: Props) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const panelId = useId();

  useEffect(() => {
    if (!open) return;
    rootRef.current?.querySelector<HTMLInputElement>("input")?.focus();
    const onPointerDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  const toggle = (value: string, on: boolean) =>
    onChange(on ? [...selected, value] : selected.filter((v) => v !== value));

  return (
    <div
      ref={rootRef}
      className="relative"
      onKeyDown={(e) => {
        if (e.key === "Escape" && open) {
          e.stopPropagation();
          setOpen(false);
          buttonRef.current?.focus();
        }
      }}
      onBlur={(e) => {
        // Tabbing out closes it; clicks outside are handled on pointerdown.
        if (e.relatedTarget && !rootRef.current?.contains(e.relatedTarget)) setOpen(false);
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((o) => !o)}
        className={cx(
          "inline-flex h-[30px] items-center gap-1.5 rounded-[9px] border px-2.5 text-[12.5px] font-semibold transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text",
          selected.length > 0
            ? "border-accent-text/50 bg-accent-soft text-text"
            : "border-border-strong bg-raised text-text hover:border-faint",
        )}
      >
        {label}
        {selected.length > 0 && (
          <span className="rounded-full bg-accent px-1.5 text-[11px] leading-[17px] text-accent-ink tabular-nums">
            {selected.length}
            <span className="sr-only">{selected.length === 1 ? " seleccionado" : " seleccionados"}</span>
          </span>
        )}
        <span aria-hidden className={cx("text-[10px] text-muted transition-transform", open && "rotate-180")}>
          ▾
        </span>
      </button>

      {open && (
        <div
          id={panelId}
          className="absolute left-0 top-full z-20 mt-1.5 w-[min(272px,calc(100vw-24px))] rounded-xl border border-border-strong bg-raised p-1.5 shadow-[var(--shadow-lg)]"
        >
          <fieldset>
            <legend className="sr-only">Filtrar por {label.toLowerCase()}</legend>
            {options.length === 0 ? (
              <p className="px-2.5 py-2 text-[13px] text-muted">{emptyText}</p>
            ) : (
              <ul className="max-h-72 overflow-y-auto">
                {options.map((o) => (
                  <li key={o.value}>
                    <label className="flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] hover:bg-surface2 has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-accent-text">
                      <input
                        type="checkbox"
                        checked={selected.includes(o.value)}
                        onChange={(e) => toggle(o.value, e.target.checked)}
                        className="size-4 shrink-0 accent-[var(--accent)] focus-visible:outline-none"
                      />
                      {o.icon}
                      <span className="min-w-0 flex-1 truncate">{o.label}</span>
                      <span className="text-xs tabular-nums text-faint">
                        {o.count}
                        <span className="sr-only">{o.count === 1 ? " tarea" : " tareas"}</span>
                      </span>
                    </label>
                  </li>
                ))}
              </ul>
            )}
          </fieldset>
        </div>
      )}
    </div>
  );
}
