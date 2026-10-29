import { Suspense } from "react";
import type { Metadata } from "next";
import { JoinView } from "./join-view";

export const metadata: Metadata = { title: "Invitación · Tablero" };

export default function JoinPage({ params }: PageProps<"/join/[token]">) {
  return (
    <main className="mx-auto flex w-full max-w-[560px] flex-col gap-6 px-4 py-14 sm:px-7">
      <Suspense>
        {params.then(({ token }) => (
          <JoinView token={token} />
        ))}
      </Suspense>
    </main>
  );
}
