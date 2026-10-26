"use client";

import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";

/** A quiet `conflict` that resolved itself, or an `error` that stays until closed. */
export type Toast = { tone: "conflict" | "error"; title: string; text: string };

const CONFLICT_TOAST_MS = 6000;

export function ToastRegion({ toast, onDismiss }: { toast: Toast | null; onDismiss: () => void }) {
  useEffect(() => {
    if (toast?.tone !== "conflict") return;
    const timer = setTimeout(onDismiss, CONFLICT_TOAST_MS);
    return () => clearTimeout(timer);
  }, [toast, onDismiss]);

  return (
    // Always mounted, so screen readers hear what appears in it.
    <div
      aria-live="polite"
      className="fixed bottom-5 right-5 z-50 w-[400px] max-sm:bottom-3 max-sm:left-3 max-sm:right-3 max-sm:w-auto"
    >
      {toast && (
        <Notice
          tone={toast.tone}
          // A conflict that already fixed itself shouldn't interrupt.
          role={toast.tone === "conflict" ? "status" : "alert"}
          title={toast.title}
          floating
          compact
          actions={
            <Button size="sm" variant="ghost" onClick={onDismiss}>
              {toast.tone === "conflict" ? "Entendido" : "Cerrar"}
            </Button>
          }
        >
          {toast.text}
        </Notice>
      )}
    </div>
  );
}
