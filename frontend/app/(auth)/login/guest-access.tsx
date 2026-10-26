"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Notice } from "@/components/ui/notice";
import { api, ApiError, startSession } from "@/lib/api";
import { meQueryKey } from "@/lib/auth";
import { describeError } from "../form-errors";

/**
 * "Entrar como invitado", only when the API runs in demo mode (GET /demo): a
 * temporary account in the demo workspace, straight into its board. It renders
 * nothing until the API says so, below everything else, so nothing shifts.
 */
export function GuestAccess() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [navigating, startNavigation] = useTransition();
  const [starting, setStarting] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const demo = useQuery({
    queryKey: ["demo"],
    queryFn: ({ signal }) => api.get("/demo", { signal }),
    staleTime: Infinity,
    retry: false,
  });

  if (!demo.data?.guest_access) return null;

  const enter = async () => {
    setFailure(null);
    setStarting(true);
    try {
      const session = await api.post("/auth/guest");
      startSession(session);
      queryClient.setQueryData(meQueryKey, session.user);
      startNavigation(() => router.replace(`/boards/${session.board_id}`));
    } catch (err) {
      setStarting(false);
      // 429 and 503 come with a message meant for the visitor.
      setFailure(err instanceof ApiError && (err.status === 429 || err.status === 503) ? err.message : describeError(err));
    }
  };
  const busy = starting || navigating;

  return (
    <section aria-labelledby="guest-access-title" className="flex flex-col gap-3 border-t border-border pt-[18px]">
      <div>
        <h2 id="guest-access-title" className="text-sm font-medium">
          ¿Solo quieres probarlo?
        </h2>
        <p className="mt-1 text-sm text-muted">
          Entra con una cuenta temporal al tablero de demo de Estudio Nube. Se borra a las pocas horas.
        </p>
      </div>
      {failure && (
        <Notice tone="error" title="No pudimos crear la cuenta de invitado" compact>
          {failure}
        </Notice>
      )}
      <Button type="button" size="lg" full loading={busy} onClick={enter}>
        {busy ? "Preparando la demo…" : "Entrar como invitado"}
      </Button>
    </section>
  );
}
