import { useId, type InputHTMLAttributes } from "react";
import { cx } from "./cx";

const sizes = {
  md: "h-[38px]",
  lg: "h-[42px]",
};

type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "size"> & {
  /** Visible label. Without one, pass `aria-label`. */
  label?: string;
  /** Field error; marks the input invalid and is read out with it. */
  error?: string;
  size?: keyof typeof sizes;
};

export function Input({ label, error, size = "md", id, className, ...props }: InputProps) {
  const autoId = useId();
  const inputId = id ?? autoId;
  const errorId = `${inputId}-error`;
  const invalid = Boolean(error) || props["aria-invalid"] === true || props["aria-invalid"] === "true";

  const input = (
    <input
      {...props}
      id={inputId}
      aria-invalid={invalid || undefined}
      aria-describedby={cx(error && errorId, props["aria-describedby"]) || undefined}
      className={cx(
        "w-full rounded-[10px] border bg-surface px-3 font-[inherit] text-sm text-text placeholder:text-faint",
        "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent-text",
        "disabled:cursor-not-allowed disabled:opacity-60",
        invalid ? "border-danger" : "border-border-strong",
        sizes[size],
        !label && !error && className,
      )}
    />
  );

  if (!label && !error) return input;

  return (
    <div className={cx("flex flex-col gap-1.5", className)}>
      {label && (
        <label htmlFor={inputId} className="text-[13px] font-medium">
          {label}
        </label>
      )}
      {input}
      {error && (
        <span id={errorId} className="text-[12.5px] text-danger">
          <span aria-hidden>! </span>
          {error}
        </span>
      )}
    </div>
  );
}
