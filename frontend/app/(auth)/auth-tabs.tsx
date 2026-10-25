"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { cx } from "@/components/ui/cx";

const tabs = [
  { href: "/login", label: "Iniciar sesión" },
  { href: "/register", label: "Crear cuenta" },
] as const;

export function AuthTabs() {
  const pathname = usePathname();
  return (
    <nav
      aria-label="Acceso"
      className="grid grid-cols-2 gap-[3px] rounded-[10px] border border-border bg-surface p-[3px]"
    >
      {tabs.map((tab) => {
        const current = pathname === tab.href;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            replace
            aria-current={current ? "page" : undefined}
            className={cx(
              "rounded-lg p-2 text-center text-[13.5px]",
              "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text",
              current ? "bg-raised font-semibold shadow-[0_0_0_1px_var(--border-strong)]" : "text-muted hover:text-text",
            )}
          >
            {tab.label}
          </Link>
        );
      })}
    </nav>
  );
}
