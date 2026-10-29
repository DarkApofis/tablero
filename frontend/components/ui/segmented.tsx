"use client";

import { useId, useRef, type KeyboardEvent } from "react";
import { cx } from "./cx";

type SegmentedProps<T extends string> = {
  /** The group's visible label. */
  label: string;
  options: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
  disabled?: boolean;
  className?: string;
};

/**
 * One choice out of a few, side by side (a radio group). Arrow keys move the
 * choice, as with native radio buttons; Tab enters and leaves the group.
 */
export function Segmented<T extends string>({ label, options, value, onChange, disabled, className }: SegmentedProps<T>) {
  const labelId = useId();
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const current = Math.max(
    options.findIndex((o) => o.value === value),
    0,
  );

  const onKeyDown = (e: KeyboardEvent) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    const next = (current + step + options.length) % options.length;
    onChange(options[next].value);
    refs.current[next]?.focus();
  };

  return (
    <div className={cx("flex flex-col gap-1.5", className)}>
      <span id={labelId} className="text-[13px] font-medium">
        {label}
      </span>
      <div
        role="radiogroup"
        aria-labelledby={labelId}
        onKeyDown={onKeyDown}
        className="flex gap-[3px] rounded-[10px] border border-border bg-surface2 p-[3px]"
      >
        {options.map((o, i) => {
          const selected = i === current;
          return (
            <button
              key={o.value}
              ref={(el) => {
                refs.current[i] = el;
              }}
              type="button"
              role="radio"
              aria-checked={selected}
              tabIndex={selected ? 0 : -1}
              disabled={disabled}
              onClick={() => onChange(o.value)}
              className={cx(
                "flex-1 cursor-pointer rounded-lg px-1.5 py-2 text-center text-[13.5px]",
                "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text disabled:cursor-not-allowed",
                selected ? "bg-raised font-semibold text-text shadow-[0_0_0_1px_var(--border-strong)]" : "font-medium text-muted",
              )}
            >
              {o.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
