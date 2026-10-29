"use client";

import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { cx } from "./cx";

export type MenuItem = {
  label: string;
  onSelect: () => void;
  /** A destructive action: red, set apart from the rest. */
  danger?: boolean;
};

type ActionMenuProps = {
  /** The button's accessible name, e.g. "Acciones del tablero Sprint". */
  label: string;
  items: MenuItem[];
  className?: string;
};

/** A "···" button that opens a menu of actions (WAI-ARIA menu button). */
export function ActionMenu({ label, items, className }: ActionMenuProps) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const menuId = useId();

  const show = (index = 0) => {
    setActive(index);
    setOpen(true);
  };
  const close = (refocus = true) => {
    setOpen(false);
    if (refocus) buttonRef.current?.focus();
  };

  useEffect(() => {
    if (open) itemRefs.current[active]?.focus();
  }, [open, active]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) close(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  const onMenuKeyDown = (e: KeyboardEvent) => {
    const last = items.length - 1;
    const moves: Record<string, number> = {
      ArrowDown: active === last ? 0 : active + 1,
      ArrowUp: active === 0 ? last : active - 1,
      Home: 0,
      End: last,
    };
    if (e.key in moves) {
      e.preventDefault();
      setActive(moves[e.key]);
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if (e.key === "Tab") {
      close(false);
    }
  };

  return (
    <div ref={rootRef} className={cx("relative", className)}>
      <button
        ref={buttonRef}
        type="button"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => (open ? close() : show())}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            show(e.key === "ArrowUp" ? items.length - 1 : 0);
          }
        }}
        className={cx(
          "flex size-[30px] cursor-pointer items-center justify-center rounded-lg text-muted",
          "hover:bg-surface2 hover:text-text focus-visible:outline-2 focus-visible:outline-accent-text",
          open && "border border-border-strong bg-surface2 text-text",
        )}
      >
        <span aria-hidden className="tracking-[1px]">
          ···
        </span>
      </button>
      {open && (
        <div
          id={menuId}
          role="menu"
          aria-label={label}
          onKeyDown={onMenuKeyDown}
          className="absolute right-0 top-9 z-20 flex w-[220px] flex-col rounded-xl border border-border-strong bg-raised p-1.5 shadow-pop"
        >
          {items.map((item, i) => (
            <button
              key={item.label}
              ref={(el) => {
                itemRefs.current[i] = el;
              }}
              type="button"
              role="menuitem"
              tabIndex={i === active ? 0 : -1}
              onPointerMove={() => setActive(i)}
              onClick={() => {
                // Focus goes back to the button first, so a dialog the item opens
                // returns it there when it closes.
                close();
                item.onSelect();
              }}
              className={cx(
                "cursor-pointer rounded-lg px-2.5 py-2 text-left text-[13.5px] outline-none",
                item.danger ? "mt-1 text-danger" : "text-text",
                item.danger && i > 0 && "relative before:absolute before:inset-x-0 before:-top-[3px] before:border-t before:border-border",
                i === active && "bg-surface2",
                "focus-visible:shadow-[inset_0_0_0_2px_var(--accent-text)]",
              )}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
