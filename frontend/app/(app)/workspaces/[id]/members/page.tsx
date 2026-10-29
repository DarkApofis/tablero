import { Suspense } from "react";
import type { Metadata } from "next";
import { MembersSkeleton, MembersView } from "./members-view";

export const metadata: Metadata = { title: "Miembros · Tablero" };

export default function MembersPage({ params }: PageProps<"/workspaces/[id]/members">) {
  return (
    <div className="flex max-w-[900px] flex-col gap-[18px]">
      <Suspense fallback={<MembersSkeleton />}>
        {params.then(({ id }) => (
          <MembersView workspaceId={id} />
        ))}
      </Suspense>
    </div>
  );
}
