import type { ReactNode } from "react";
import { cx } from "./cx";

// Full class strings so Tailwind can see them.
const tones = {
  conflict: {
    glyph: "⇄",
    role: "alert",
    color: "text-warn border-warn",
    box: "bg-warn-soft border-warn/45",
    floating: "bg-[color-mix(in_oklab,var(--warn)_10%,var(--raised))] border-warn/45",
  },
  error: {
    glyph: "!",
    role: "alert",
    color: "text-danger border-danger",
    box: "bg-danger-soft border-danger/45",
    floating: "bg-[color-mix(in_oklab,var(--danger)_10%,var(--raised))] border-danger/45",
  },
  offline: {
    glyph: "○",
    role: "status",
    color: "text-warn border-warn",
    box: "bg-warn-soft border-warn/45",
    floating: "bg-[color-mix(in_oklab,var(--warn)_10%,var(--raised))] border-warn/45",
  },
  info: {
    glyph: "i",
    role: "status",
    color: "text-accent-text border-accent-text",
    box: "bg-accent-soft border-accent-text/45",
    floating: "bg-[color-mix(in_oklab,var(--accent-text)_10%,var(--raised))] border-accent-text/45",
  },
  success: {
    glyph: "✓",
    role: "status",
    color: "text-accent-text border-accent-text",
    box: "bg-accent-soft border-accent-text/45",
    floating: "bg-[color-mix(in_oklab,var(--accent-text)_10%,var(--raised))] border-accent-text/45",
  },
};

type NoticeProps = {
  tone?: keyof typeof tones;
  title: ReactNode;
  children?: ReactNode;
  /** Buttons, usually `<Button size="sm">`. */
  actions?: ReactNode;
  compact?: boolean;
  /** Raised with a shadow, for toasts over the board. */
  floating?: boolean;
  className?: string;
};

export function Notice({
  tone = "info",
  title,
  children,
  actions,
  compact = false,
  floating = false,
  className,
}: NoticeProps) {
  const t = tones[tone];
  return (
    <div
      role={t.role}
      className={cx(
        "flex w-full items-start gap-3 rounded-xl border",
        compact ? "px-3 py-2.5" : "px-3.5 py-3",
        floating ? cx(t.floating, "shadow-pop") : t.box,
        className,
      )}
    >
      <span
        aria-hidden
        className={cx(
          "flex size-6 shrink-0 items-center justify-center rounded-lg border-[1.5px] text-[13px] font-bold",
          t.color,
        )}
      >
        {t.glyph}
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="text-[13.5px] font-semibold leading-[1.35] text-pretty text-text">{title}</div>
        {children && <div className="text-[13px] leading-normal text-pretty text-muted">{children}</div>}
        {actions && <div className="mt-[9px] flex flex-wrap gap-2">{actions}</div>}
      </div>
    </div>
  );
}
