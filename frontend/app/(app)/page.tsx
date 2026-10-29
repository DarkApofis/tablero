import type { Metadata } from "next";
import { NewWorkspace, WorkspaceList } from "./workspace-list";

export const metadata: Metadata = { title: "Workspaces · Tablero" };

export default function Home() {
  return (
    <main className="mx-auto flex w-full max-w-[1180px] flex-col gap-6 px-4 py-8 sm:px-7">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-[28px] font-semibold tracking-[-.02em]">Tus workspaces</h1>
          <p className="mt-1.5 text-sm text-muted">Elige un equipo para ver sus tableros.</p>
        </div>
        <NewWorkspace />
      </div>
      <WorkspaceList />
    </main>
  );
}
