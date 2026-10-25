import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cx } from "./cx";

const variants = {
  primary: "bg-accent text-accent-ink border-transparent",
  secondary: "bg-raised text-text border-border-strong",
  ghost: "bg-transparent text-muted border-transparent",
  danger: "bg-danger-soft text-danger border-danger/40",
};

const sizes = {
  sm: "h-[30px] px-3 text-[12.5px]",
  md: "h-[38px] px-4 text-sm",
  lg: "h-11 px-5 text-[15px]",
};

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof variants;
  size?: keyof typeof sizes;
  loading?: boolean;
  full?: boolean;
  icon?: ReactNode;
};

export function Button({
  variant = "secondary",
  size = "md",
  loading = false,
  full = false,
  icon,
  disabled,
  type = "button",
  className,
  children,
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={cx(
        "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[10px] border font-semibold",
        "transition-[filter,transform] duration-150 enabled:cursor-pointer enabled:hover:brightness-110 enabled:active:translate-y-px",
        "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-text",
        "disabled:cursor-not-allowed",
        disabled && "opacity-50",
        variants[variant],
        sizes[size],
        full && "w-full",
        className,
      )}
      {...props}
    >
      {loading ? (
        <span
          aria-hidden
          className="size-3.5 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent [animation-duration:.8s]"
        />
      ) : (
        icon && <span aria-hidden>{icon}</span>
      )}
      {children}
    </button>
  );
}
