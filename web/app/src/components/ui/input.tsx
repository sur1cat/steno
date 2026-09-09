import {
  forwardRef,
  type ClipboardEvent,
  type InputHTMLAttributes,
} from "react";
import { cn } from "@/lib/utils";
import { handleDateInputPaste } from "@/lib/date-paste";
import { useTranslation } from "@/lib/i18n";

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
}

const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, label, error, id, ...props }, ref) => {
    const { t } = useTranslation();
    // Zod schemas now carry i18n keys (e.g. "validation.required") as messages.
    // t() is idempotent for unknown keys (returns the key itself), so passing a
    // plain literal still works.
    const errorText = error ? t(error) : undefined;
    // Native <input type="date"> ignores pasted text — let operators Ctrl+V
    // dates copied from 1C/Excel. Composes with a caller-supplied onPaste.
    const onPaste =
      type === "date"
        ? (e: ClipboardEvent<HTMLInputElement>) => {
            props.onPaste?.(e);
            if (!e.defaultPrevented) handleDateInputPaste(e);
          }
        : props.onPaste;
    return (
      <div className="w-full">
        {label && (
          <label
            htmlFor={id}
            className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]"
          >
            {label}
          </label>
        )}
        <input
          type={type}
          id={id}
          className={cn(
            "flex h-12 w-full rounded-xl border-0 bg-[var(--muted)] px-4 py-2 text-sm text-[var(--foreground)] transition-all duration-150 placeholder:text-[var(--muted-foreground)]/50 focus:outline-none focus:ring-2 focus:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-50",
            errorText && "ring-2 ring-[var(--destructive)]/30 focus:ring-[var(--destructive)]/30",
            className
          )}
          ref={ref}
          {...props}
          onPaste={onPaste}
        />
        {errorText && (
          <p className="mt-1.5 text-[13px] text-[var(--destructive)]">{errorText}</p>
        )}
      </div>
    );
  }
);
Input.displayName = "Input";

export { Input };
