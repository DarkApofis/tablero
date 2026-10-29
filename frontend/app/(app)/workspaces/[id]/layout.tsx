import { Suspense } from "react";
import { ShellSkeleton, WorkspaceShell } from "./workspace-shell";

// The workspace is read with the in-memory access token, so its data loads in
// the browser; the server sends the frame and the skeleton.
export default function WorkspaceLayout({ children, params }: LayoutProps<"/workspaces/[id]">) {
  return (
    <main className="mx-auto flex w-full max-w-[1180px] flex-col gap-5 px-4 py-8 sm:px-7">
      <Suspense fallback={<ShellSkeleton />}>
        {params.then(({ id }) => (
          <WorkspaceShell workspaceId={id}>{children}</WorkspaceShell>
        ))}
      </Suspense>
    </main>
  );
}
