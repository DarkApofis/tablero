"use client";

import { useEffect } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { Notice } from "@/components/ui/notice";
import { api } from "@/lib/api";

/** Finds the workspace with this address and goes there. */
export function AddressRedirect({ slug }: { slug: string }) {
  const router = useRouter();
  const found = useQuery({
    queryKey: ["workspace-slugs", slug],
    queryFn: ({ signal }) => api.get("/workspace-slugs/{slug}", { path: { slug }, signal }),
    retry: false,
  });

  useEffect(() => {
    if (found.data) router.replace(`/workspaces/${found.data.id}`);
  }, [found.data, router]);

  if (!found.error) {
    return (
      <p role="status" className="text-sm text-muted">
        Abriendo el workspace…
      </p>
    );
  }
  return (
    <Notice
      tone="error"
      title="No encontramos ese workspace"
      actions={
        <Link
          href="/"
          className="rounded text-[13px] font-semibold text-text underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
        >
          Ir a tus workspaces
        </Link>
      }
    >
      Puede que la dirección esté mal o que no seas miembro.
    </Notice>
  );
}
