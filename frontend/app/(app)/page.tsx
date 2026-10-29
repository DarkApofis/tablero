import type { Metadata } from "next";
import { WorkspacesHome } from "./workspace-list";

export const metadata: Metadata = { title: "Workspaces · Tablero" };

export default function Home() {
  return (
    <main className="mx-auto flex w-full max-w-[1180px] flex-col gap-6 px-4 py-8 sm:px-7">
      <WorkspacesHome />
    </main>
  );
}
