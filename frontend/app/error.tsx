"use client";

import { useEffect, useRef } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";

/** Anything a screen didn't handle itself: say so in Spanish and offer a way out. */
export default function ScreenError({ error, retry }: { error: unknown; retry: () => void }) {
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    console.error(error);
    // What the user was on is gone; start them here.
    heading.current?.focus();
  }, [error]);

  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-3 px-4 py-16 text-center">
      <div
        aria-hidden
        className="flex size-11 items-center justify-center rounded-[14px] border-[1.5px] border-danger bg-danger-soft text-xl font-bold text-danger"
      >
        !
      </div>
      <h1 ref={heading} tabIndex={-1} className="mt-1.5 text-xl font-semibold tracking-[-.01em] outline-none">
        Algo salió mal
      </h1>
      <p className="max-w-sm text-sm leading-[1.55] text-muted">
        No pudimos mostrar esta pantalla. Los cambios ya guardados están a salvo.
      </p>
      <div className="mt-3 flex flex-wrap justify-center gap-2.5">
        <Button variant="primary" onClick={retry}>
          Reintentar
        </Button>
        <Link
          href="/"
          className="inline-flex h-[38px] items-center rounded-[10px] px-4 text-sm font-semibold text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
        >
          Ir a tus workspaces
        </Link>
      </div>
    </main>
  );
}
