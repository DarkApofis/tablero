"use client";

import { useTransition } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { Logo } from "@/components/logo";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { logout, useMe } from "@/lib/auth";

export function AppHeader() {
  const { data: me } = useMe();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [leaving, startLeaving] = useTransition();

  const signOut = () =>
    startLeaving(async () => {
      await logout().catch(() => {}); // the local session is cleared either way
      queryClient.clear();
      router.replace("/login");
    });

  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-surface px-4 sm:px-7">
      <Link href="/" className="rounded-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text">
        <Logo />
      </Link>
      <div className="flex items-center gap-3">
        {me ? (
          <Avatar name={me.name} size={30} />
        ) : (
          <span aria-hidden className="size-[30px] animate-pulse rounded-full bg-border" />
        )}
        <Button variant="ghost" size="sm" loading={leaving} onClick={signOut}>
          Cerrar sesión
        </Button>
      </div>
    </header>
  );
}
