import { HealthStatus } from "./health-status";

export default function Home() {
  return (
    <main className="mx-auto flex w-full max-w-md flex-1 flex-col justify-center gap-6 px-4">
      <h1 className="text-2xl font-semibold tracking-tight">
        Tablero <span className="text-accent">·</span> Kanban colaborativo
      </h1>
      <HealthStatus />
    </main>
  );
}
