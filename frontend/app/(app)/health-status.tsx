"use client";

import { useEffect, useState } from "react";

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

type Health =
  | { state: "loading" }
  | { state: "ok" }
  | { state: "down"; detail: string };

export function HealthStatus() {
  const [health, setHealth] = useState<Health>({ state: "loading" });

  useEffect(() => {
    const controller = new AbortController();

    async function check() {
      try {
        const res = await fetch(`${API_URL}/health`, {
          signal: controller.signal,
          cache: "no-store",
        });
        if (res.ok) {
          setHealth({ state: "ok" });
          return;
        }
        const body: { error?: { code: string; message: string } } = await res
          .json()
          .catch(() => ({}));
        setHealth({
          state: "down",
          detail: `${res.status} · ${body.error?.message ?? res.statusText}`,
        });
      } catch (err) {
        if (controller.signal.aborted) return;
        setHealth({
          state: "down",
          detail: err instanceof Error ? err.message : "error de red",
        });
      }
    }

    check();
    return () => controller.abort();
  }, []);

  const styles = {
    loading: { dot: "bg-faint", label: "Comprobando…", text: "text-muted" },
    ok: { dot: "bg-accent", label: "Backend operativo", text: "text-accent-text" },
    down: { dot: "bg-danger", label: "Backend no disponible", text: "text-danger" },
  }[health.state];

  return (
    <div className="rounded-xl border border-border bg-surface p-5">
      <div className="flex items-center gap-3">
        <span className={`size-2.5 rounded-full ${styles.dot}`} aria-hidden />
        <span className={`font-medium ${styles.text}`} role="status">
          {styles.label}
        </span>
      </div>
      <p className="mt-2 font-mono text-xs text-faint">
        GET {API_URL}/health
        {health.state === "down" && ` — ${health.detail}`}
      </p>
    </div>
  );
}
