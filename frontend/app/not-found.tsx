import type { Metadata } from "next";
import Link from "next/link";
import { Logo } from "@/components/logo";

export const metadata: Metadata = { title: "Página no encontrada · Tablero" };

export default function NotFound() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-3 px-4 py-16 text-center">
      <Logo />
      <h1 className="mt-4 text-2xl font-semibold tracking-[-.01em]">No encontramos esta página</h1>
      <p className="max-w-sm text-sm leading-[1.55] text-muted">Puede que el enlace esté mal o que la página ya no exista.</p>
      <Link
        href="/"
        className="mt-3 inline-flex h-[38px] items-center rounded-[10px] bg-accent px-4 text-sm font-semibold text-accent-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
      >
        Ir a tus workspaces
      </Link>
    </main>
  );
}
