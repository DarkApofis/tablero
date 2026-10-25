import { cx } from "./cx";

type TagProps = {
  label: string;
  /**
   * Any CSS color, e.g. a label's `color` from the API. Only its hue is used, so every
   * tag keeps the same lightness and chroma and stays legible in both themes.
   */
  color?: string;
  /** oklch hue (0–360), used when there is no `color`. */
  hue?: number;
  className?: string;
};

export function Tag({ label, color, hue = 250, className }: TagProps) {
  // Relative color syntax keeps only the hue of an arbitrary color.
  const h = color ? "h" : String(hue);
  const from = color ? `from ${color} ` : "";
  return (
    <span
      className={cx(
        "inline-flex h-5 items-center gap-[5px] whitespace-nowrap rounded-md px-2 text-[11.5px] font-medium leading-none",
        className,
      )}
      style={{
        background: `oklch(${from}0.7 0.12 ${h} / 0.16)`,
        color: `oklch(${from}var(--tag-l) 0.13 ${h})`,
      }}
    >
      <span aria-hidden className="size-[5px] rounded-full bg-current" />
      {label}
    </span>
  );
}
