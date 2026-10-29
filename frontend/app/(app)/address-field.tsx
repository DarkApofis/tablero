"use client";

import { useEffect, useId, useState } from "react";
import { cx } from "@/components/ui/cx";
import { ApiError } from "@/lib/api";
import { normalizeSlug, slugProblem } from "@/lib/slug";
import { addressPrefix, checkSlug } from "@/lib/workspaces";

export type SlugStatus = "idle" | "checking" | "available" | "taken" | "invalid" | "unknown";

/**
 * Whether an address is free, asked a moment after the user stops typing.
 * `current` is the workspace's own address, which counts as free.
 */
export function useSlugStatus(slug: string, current?: string | null): { status: SlugStatus; problem: string | null } {
  const problem = slug ? slugProblem(slug) : null;
  const [result, setResult] = useState<{ slug: string; status: SlugStatus }>({ slug: "", status: "idle" });

  useEffect(() => {
    if (!slug || problem || slug === current) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      checkSlug({ slug }, controller.signal)
        .then((r) => setResult({ slug, status: r.available ? "available" : "taken" }))
        .catch((err) => {
          if (!controller.signal.aborted) setResult({ slug, status: err instanceof ApiError && err.status === 422 ? "invalid" : "unknown" });
        });
    }, 300);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [slug, problem, current]);

  if (!slug) return { status: "idle", problem: null };
  if (problem) return { status: "invalid", problem };
  if (slug === current) return { status: "available", problem: null };
  return { status: result.slug === slug ? result.status : "checking", problem: null };
}

type AddressFieldProps = {
  value: string;
  onChange: (slug: string) => void;
  status: SlugStatus;
  /** Why it is not valid, or the API's reason. */
  error?: string | null;
  disabled?: boolean;
};

/** "tablero-orcin.vercel.app/w/" + the address, with whether it is free. */
export function AddressField({ value, onChange, status, error, disabled }: AddressFieldProps) {
  const id = useId();
  const statusId = useId();
  // The site's host is only known in the browser; the server renders "/w/".
  const [prefix] = useState(addressPrefix);

  const message =
    error ??
    {
      idle: null,
      checking: "Comprobando…",
      available: "✓ Disponible",
      taken: "Ya la usa otro workspace",
      invalid: null,
      unknown: "No pudimos comprobarla",
    }[status];
  const bad = Boolean(error) || status === "taken";

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-[13px] font-medium">
        Dirección
      </label>
      <div
        className={cx(
          "flex h-10 overflow-hidden rounded-[10px] border font-mono text-[13px]",
          "focus-within:outline-2 focus-within:outline-offset-1 focus-within:outline-accent-text",
          bad ? "border-danger" : "border-control-border",
        )}
      >
        <span
          suppressHydrationWarning
          className="flex max-w-[45%] items-center truncate border-r border-border bg-surface2 px-2.5 text-faint"
          aria-hidden
        >
          {prefix}
        </span>
        <input
          id={id}
          value={value}
          disabled={disabled}
          maxLength={40}
          spellCheck={false}
          autoComplete="off"
          aria-invalid={bad || undefined}
          aria-describedby={statusId}
          onChange={(e) => onChange(normalizeSlug(e.target.value))}
          className="min-w-0 flex-1 bg-surface px-2.5 text-text outline-none"
        />
      </div>
      <span
        id={statusId}
        aria-live="polite"
        className={cx("min-h-[18px] text-[12.5px]", bad ? "text-danger" : status === "available" ? "text-accent-text" : "text-faint")}
      >
        {bad && <span aria-hidden>! </span>}
        {message}
      </span>
    </div>
  );
}
