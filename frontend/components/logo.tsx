export function Logo({ size = "md" }: { size?: "md" | "lg" }) {
  return (
    <span className="flex items-center gap-2.5">
      <span aria-hidden className="flex items-end gap-[3px]">
        <span className="h-5 w-[7px] rounded-[2px] bg-accent" />
        <span className="h-[13px] w-[7px] rounded-[2px] bg-accent opacity-70" />
        <span className="h-[17px] w-[7px] rounded-[2px] bg-accent opacity-45" />
      </span>
      <span className={size === "lg" ? "text-xl font-semibold tracking-[-.01em]" : "text-[17px] font-semibold"}>
        Tablero
      </span>
    </span>
  );
}
