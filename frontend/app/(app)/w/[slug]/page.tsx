import { Suspense } from "react";
import type { Metadata } from "next";
import { AddressRedirect } from "./address-redirect";

export const metadata: Metadata = { title: "Tablero" };

// A workspace's address (tablero…/w/<slug>) opens the workspace.
export default function AddressPage({ params }: PageProps<"/w/[slug]">) {
  return (
    <main className="mx-auto flex w-full max-w-[560px] flex-col gap-6 px-4 py-14 sm:px-7">
      <Suspense>
        {params.then(({ slug }) => (
          <AddressRedirect slug={slug} />
        ))}
      </Suspense>
    </main>
  );
}
