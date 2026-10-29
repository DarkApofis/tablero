import { Suspense } from "react";
import type { Metadata } from "next";
import { BoardsSkeleton } from "../workspace-view";
import { ArchivedView } from "./archived-view";

export const metadata: Metadata = { title: "Archivados · Tablero" };

export default function ArchivedPage({ params }: PageProps<"/workspaces/[id]/archived">) {
  return (
    <Suspense fallback={<BoardsSkeleton />}>
      {params.then(({ id }) => (
        <ArchivedView workspaceId={id} />
      ))}
    </Suspense>
  );
}
