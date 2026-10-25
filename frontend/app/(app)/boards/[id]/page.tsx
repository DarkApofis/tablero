import { Suspense } from "react";
import type { Metadata } from "next";
import { BoardSkeleton } from "./board-skeleton";
import { BoardView } from "./board-view";

export const metadata: Metadata = { title: "Tablero" };

// The board is read with the in-memory access token, so its data loads in the
// browser; the server sends the frame and the skeleton.
export default function BoardPage({ params }: PageProps<"/boards/[id]">) {
  return (
    <main className="flex h-[calc(100dvh-3.5rem)] min-h-[420px] flex-col">
      <Suspense fallback={<BoardSkeleton />}>
        {params.then(({ id }) => (
          <BoardView boardId={id} />
        ))}
      </Suspense>
    </main>
  );
}
