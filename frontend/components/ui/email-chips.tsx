"use client";

import { useId, useState, type KeyboardEvent } from "react";
import { cx } from "./cx";

// Like the API's check (internal/domain/user.go): something@something.tld.
const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export const isEmail = (s: string) => EMAIL.test(s);

/** Splits pasted or typed text into emails: commas, semicolons, spaces, new lines. */
export const splitEmails = (text: string) =>
  text
    .split(/[\s,;]+/)
    .map((e) => e.trim().toLowerCase())
    .filter(Boolean);

type EmailChipsProps = {
  label: string;
  /** Shown after the label, quieter: "· opcional". */
  hint?: string;
  value: string[];
  onChange: (emails: string[]) => void;
  disabled?: boolean;
  placeholder?: string;
};

/**
 * Emails as chips: typing a comma, a space or Enter turns what was typed into
 * one; Backspace on an empty field removes the last. An invalid one stays, in
 * red, with a message, until it is fixed or removed.
 */
export function EmailChips({ label, hint, value, onChange, disabled, placeholder }: EmailChipsProps) {
  const [draft, setDraft] = useState("");
  const inputId = useId();
  const errorId = useId();
  const invalid = value.find((e) => !isEmail(e));

  const add = (text: string) => {
    const emails = splitEmails(text).filter((e) => !value.includes(e));
    if (emails.length > 0) onChange([...value, ...emails]);
    setDraft("");
  };
  const remove = (email: string) => onChange(value.filter((e) => e !== email));

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if ((e.key === "Enter" || e.key === "," || e.key === " " || e.key === ";") && draft.trim()) {
      e.preventDefault();
      add(draft);
    } else if (e.key === "Backspace" && !draft && value.length > 0) {
      remove(value[value.length - 1]);
    }
  };

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={inputId} className="text-[13px] font-medium">
        {label}
        {hint && <span className="font-normal text-faint"> {hint}</span>}
      </label>
      <div
        className={cx(
          "flex min-h-10 flex-wrap items-center gap-1.5 rounded-[10px] border bg-surface p-1.5",
          "focus-within:outline-2 focus-within:outline-offset-1 focus-within:outline-accent-text",
          invalid ? "border-danger" : "border-control-border",
        )}
      >
        {value.map((email) => {
          const bad = !isEmail(email);
          return (
            <span
              key={email}
              className={cx(
                "inline-flex h-[26px] items-center gap-1.5 rounded-[7px] border pl-2.5 pr-1 text-[12.5px]",
                bad ? "border-danger bg-danger-soft text-danger" : "border-border-strong bg-raised",
              )}
            >
              {bad && <span aria-hidden>!</span>}
              {email}
              <button
                type="button"
                aria-label={`Quitar ${email}`}
                disabled={disabled}
                onClick={() => remove(email)}
                className="cursor-pointer rounded px-1 text-sm text-current opacity-75 hover:opacity-100 focus-visible:outline-2 focus-visible:outline-accent-text"
              >
                ×
              </button>
            </span>
          );
        })}
        <input
          id={inputId}
          type="email"
          value={draft}
          disabled={disabled}
          placeholder={value.length === 0 ? placeholder : undefined}
          aria-invalid={invalid ? true : undefined}
          aria-describedby={invalid ? errorId : undefined}
          onChange={(e) => {
            // A paste of several at once becomes chips straight away.
            if (/[\s,;]/.test(e.target.value)) add(e.target.value);
            else setDraft(e.target.value);
          }}
          onKeyDown={onKeyDown}
          onBlur={() => draft.trim() && add(draft)}
          className="min-w-[160px] flex-1 bg-transparent px-1.5 text-sm text-text outline-none placeholder:text-faint"
        />
      </div>
      {invalid && (
        <span id={errorId} className="text-[12.5px] text-danger">
          <span aria-hidden>! </span>«{invalid}» no es un correo válido. Corrígelo o quítalo.
        </span>
      )}
    </div>
  );
}
