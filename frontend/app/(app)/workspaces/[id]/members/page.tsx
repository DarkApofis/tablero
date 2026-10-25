import { Suspense } from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { MembersSkeleton, MembersView } from "./members-view";

export const metadata: Metadata = { title: "Miembros · Tablero" };

export default function MembersPage({ params }: PageProps<"/workspaces/[id]/members">) {
  return (
    <main className="mx-auto flex w-full max-w-[900px] flex-col gap-[22px] px-4 py-8 sm:px-10">
      <Suspense fallback={<MembersSkeleton />}>
        {params.then(({ id }) => (
          <>
            <Link
              href={`/workspaces/${id}`}
              className="w-fit rounded text-[13.5px] text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
            >
              ← Tableros
            </Link>
            <MembersView workspaceId={id} />
          </>
        ))}
      </Suspense>
    </main>
  );
}
