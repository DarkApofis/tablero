import { Suspense } from "react";
import type { Metadata } from "next";
import { BoardsSkeleton, WorkspaceView } from "./workspace-view";

export const metadata: Metadata = { title: "Tableros · Tablero" };

export default function WorkspacePage({ params }: PageProps<"/workspaces/[id]">) {
  return (
    <Suspense fallback={<BoardsSkeleton />}>
      {params.then(({ id }) => (
        <WorkspaceView workspaceId={id} />
      ))}
    </Suspense>
  );
}
