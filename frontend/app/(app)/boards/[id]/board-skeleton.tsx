import { columnBox } from "./styles";

const bar = "animate-pulse rounded bg-border";

/** Placeholder with the board's layout, shared by the route's Suspense and the query. */
export function BoardSkeleton() {
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-busy="true" aria-label="Cargando tablero">
      <div className="flex h-14 shrink-0 items-center gap-3 border-b border-border bg-surface px-3 sm:px-5">
        <div className="size-8 animate-pulse rounded-lg bg-border" />
        <div className={`${bar} h-4 w-40`} />
      </div>
      <div className="flex h-[51px] shrink-0 items-center gap-2 border-b border-border px-3 sm:px-5">
        <div className="h-[30px] w-24 animate-pulse rounded-[9px] bg-border" />
        <div className="h-[30px] w-28 animate-pulse rounded-[9px] bg-border" />
      </div>
      <div className="flex min-h-0 flex-1 items-start gap-4 overflow-hidden p-5 max-sm:gap-2.5 max-sm:p-3">
        {[3, 2, 3, 1].map((cards, i) => (
          <div key={i} className={columnBox}>
            <div className="flex items-center justify-between py-1 pl-1.5 pr-1">
              <div className={`${bar} h-3 w-[90px]`} />
              <div className="h-[18px] w-7 animate-pulse rounded-full bg-border" />
            </div>
            {Array.from({ length: cards }, (_, j) => (
              <div key={j} className="flex flex-col gap-2.5 rounded-xl border border-border bg-surface2 p-3.5">
                <div className={`${bar} h-3.5 w-2/5`} />
                <div className={`${bar} h-2.5 w-full`} />
                <div className={`${bar} h-2.5 w-[70%]`} />
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
