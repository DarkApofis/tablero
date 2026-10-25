import { Suspense } from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { WorkspaceSkeleton, WorkspaceView } from "./workspace-view";

export const metadata: Metadata = { title: "Tableros · Tablero" };

export default function WorkspacePage({ params }: PageProps<"/workspaces/[id]">) {
  return (
    <main className="mx-auto flex w-full max-w-[1180px] flex-col gap-6 px-4 py-8 sm:px-7">
      <Link
        href="/"
        className="w-fit rounded text-[13.5px] text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text"
      >
        ← Tus workspaces
      </Link>
      <Suspense fallback={<WorkspaceSkeleton />}>
        {params.then(({ id }) => (
          <WorkspaceView workspaceId={id} />
        ))}
      </Suspense>
    </main>
  );
}
