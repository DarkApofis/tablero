import { cx } from "./cx";

type AvatarProps = {
  name: string;
  /** oklch hue (0–360). Defaults to a stable hue derived from `name`. */
  hue?: number;
  size?: number;
  /** Accent ring, e.g. for the current user or someone editing. */
  ring?: boolean;
  className?: string;
};

export function initials(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean);
  const letters = words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] ?? "?").slice(0, 2);
  return letters.toUpperCase();
}

export function hueFor(seed: string) {
  let h = 0;
  for (const ch of seed) h = (h * 31 + ch.codePointAt(0)!) % 360;
  return h;
}

export function Avatar({ name, hue, size = 28, ring = false, className }: AvatarProps) {
  return (
    <div
      role="img"
      aria-label={name}
      title={name}
      className={cx(
        "flex shrink-0 select-none items-center justify-center rounded-full font-semibold tracking-[.02em] text-white",
        className,
      )}
      style={{
        width: size,
        height: size,
        fontSize: Math.round(size * 0.4),
        background: `oklch(0.4 0.09 ${hue ?? hueFor(name)})`,
        boxShadow: ring
          ? "0 0 0 2px var(--surface), 0 0 0 4px var(--accent)"
          : "0 0 0 2px var(--surface)",
      }}
    >
      {initials(name)}
    </div>
  );
}
