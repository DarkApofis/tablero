"use client";

import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { cx } from "@/components/ui/cx";
import { roleLabel, roles, type Role } from "@/lib/roles";

type RoleMenuProps = {
  value: Role;
  /** Whose role this is, for the accessible name. */
  memberName: string;
  disabled?: boolean;
  pending?: boolean;
  onSelect: (role: Role) => void;
};

/** Button + listbox with each role's description, as in the design. */
export function RoleMenu({ value, memberName, disabled, pending, onSelect }: RoleMenuProps) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const optionRefs = useRef<(HTMLDivElement | null)[]>([]);
  const listId = useId();

  const show = (index = roles.findIndex((r) => r.value === value)) => {
    setActive(Math.max(index, 0));
    setOpen(true);
  };
  const close = (refocus = true) => {
    setOpen(false);
    if (refocus) buttonRef.current?.focus();
  };
  const choose = (role: Role) => {
    close();
    if (role !== value) onSelect(role);
  };

  useEffect(() => {
    if (open) optionRefs.current[active]?.focus();
  }, [open, active]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) close(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  const onButtonKeyDown = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      show();
    }
  };

  const onListKeyDown = (e: KeyboardEvent) => {
    const last = roles.length - 1;
    const moves: Record<string, number> = {
      ArrowDown: Math.min(active + 1, last),
      ArrowUp: Math.max(active - 1, 0),
      Home: 0,
      End: last,
    };
    if (e.key in moves) {
      e.preventDefault();
      setActive(moves[e.key]);
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      choose(roles[active].value);
    } else if (e.key === "Escape") {
      e.preventDefault();
      close();
    } else if (e.key === "Tab") {
      close(false);
    }
  };

  return (
    <div ref={rootRef} className="relative">
      <button
        ref={buttonRef}
        type="button"
        disabled={disabled || pending}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-label={`Rol de ${memberName}: ${roleLabel(value)}`}
        aria-busy={pending || undefined}
        onClick={() => (open ? close() : show())}
        onKeyDown={onButtonKeyDown}
        className={cx(
          "flex h-[34px] min-w-[140px] items-center justify-between gap-2 rounded-[9px] border border-border-strong px-3 text-[13.5px] font-medium",
          "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text",
          "enabled:cursor-pointer enabled:bg-raised disabled:cursor-not-allowed disabled:text-muted",
        )}
      >
        {roleLabel(value)}
        {pending ? (
          <span aria-hidden className="size-3 animate-spin rounded-full border-2 border-current border-t-transparent" />
        ) : (
          !disabled && (
            <span aria-hidden className="text-faint">
              {open ? "▴" : "▾"}
            </span>
          )
        )}
      </button>
      {open && (
        <div
          id={listId}
          role="listbox"
          aria-label={`Rol de ${memberName}`}
          onKeyDown={onListKeyDown}
          className="absolute right-0 top-11 z-10 w-[min(270px,calc(100vw-32px))] rounded-xl border border-border-strong bg-raised p-1.5 shadow-pop"
        >
          {roles.map((role, i) => {
            const selected = role.value === value;
            return (
              <div
                key={role.value}
                ref={(el) => {
                  optionRefs.current[i] = el;
                }}
                role="option"
                aria-selected={selected}
                tabIndex={i === active ? 0 : -1}
                onClick={() => choose(role.value)}
                onPointerMove={() => setActive(i)}
                className={cx(
                  "flex cursor-pointer gap-2 rounded-lg px-2.5 py-[9px] outline-none",
                  selected ? "bg-accent-soft" : i === active && "bg-surface2",
                  "focus-visible:shadow-[inset_0_0_0_2px_var(--accent-text)]",
                )}
              >
                <span aria-hidden className="w-3.5 shrink-0 font-bold text-accent-text">
                  {selected ? "✓" : ""}
                </span>
                <div>
                  <div className="text-[13.5px] font-semibold">{role.label}</div>
                  <div className="text-xs leading-[1.4] text-muted">{role.description}</div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
