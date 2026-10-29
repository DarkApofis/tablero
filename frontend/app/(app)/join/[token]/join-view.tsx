"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { api, ApiError } from "@/lib/api";
import { roleLabel } from "@/lib/roles";
import { workspaceKeys } from "@/lib/workspaces";

const linkClass =
  "rounded text-[13px] font-semibold text-text underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text";

/** Where an invitation link leads, and a button to join. */
export function JoinView({ token }: { token: string }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const info = useQuery({
    queryKey: ["invite-links", token],
    queryFn: ({ signal }) => api.get("/invite-links/{token}", { path: { token }, signal }),
    retry: false,
  });
  const join = useMutation({
    mutationFn: () => api.post("/invite-links/{token}/join", { path: { token } }),
    onSuccess: ({ workspace_id }) => {
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.all, exact: true });
      router.replace(`/workspaces/${workspace_id}`);
    },
  });

  if (info.isPending) {
    return (
      <div role="status" aria-busy="true" className="flex flex-col gap-3">
        <span className="sr-only">Comprobando la invitación…</span>
        <div className="h-7 w-64 animate-pulse rounded bg-border" />
        <div className="h-4 w-48 animate-pulse rounded bg-border" />
      </div>
    );
  }
  if (info.error) {
    const invalid = info.error instanceof ApiError && info.error.status === 404;
    return (
      <Notice
        tone="error"
        title={invalid ? "Esta invitación no es válida" : "No pudimos comprobar la invitación"}
        actions={
          <Link href="/" className={linkClass}>
            Ir a tus workspaces
          </Link>
        }
      >
        {invalid ? "El enlace caducó o lo reemplazaron. Pide uno nuevo a quien te lo envió." : "Inténtalo de nuevo en un momento."}
      </Notice>
    );
  }

  const { workspace_name, role, already_member, workspace_id } = info.data;
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-[26px] font-semibold tracking-[-.02em]">Te invitaron a {workspace_name}</h1>
        <p className="mt-1.5 text-sm text-muted">
          {already_member ? "Ya eres miembro de este workspace." : `Entrarás como ${roleLabel(role)}.`}
        </p>
      </div>
      {join.error && (
        <Notice tone="error" title="No pudiste unirte" compact>
          {join.error instanceof ApiError && join.error.status < 500 ? join.error.message : "Inténtalo de nuevo en un momento."}
        </Notice>
      )}
      <div className="flex gap-2.5">
        {already_member ? (
          <Button variant="primary" onClick={() => router.push(`/workspaces/${workspace_id}`)}>
            Ir al workspace
          </Button>
        ) : (
          <Button variant="primary" loading={join.isPending} onClick={() => join.mutate()}>
            Unirme a {workspace_name}
          </Button>
        )}
        <Link
          href="/"
          className="inline-flex h-[38px] items-center rounded-[10px] px-4 text-sm font-semibold text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
        >
          Ahora no
        </Link>
      </div>
    </div>
  );
}
