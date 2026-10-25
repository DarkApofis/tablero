import { useEffect, useRef } from "react";

/**
 * The fields are disabled while submitting, so they cannot take focus from the
 * submit handler. Returns `focusLater(name)`, which focuses the field once the
 * submission ends, so keyboard users land on what needs fixing.
 */
export function useFocusAfterSubmit<Name extends string>(isSubmitting: boolean, setFocus: (name: Name) => void) {
  const pending = useRef<Name | null>(null);
  useEffect(() => {
    if (!isSubmitting && pending.current) {
      setFocus(pending.current);
      pending.current = null;
    }
  }, [isSubmitting, setFocus]);
  return (name: Name) => {
    pending.current = name;
  };
}
