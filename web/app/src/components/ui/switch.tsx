"use client";

import { forwardRef, type InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

interface SwitchProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> {
  checked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
}

const Switch = forwardRef<HTMLInputElement, SwitchProps>(
  ({ className, checked, onCheckedChange, disabled, ...props }, ref) => {
    return (
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        className={cn(
          "peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--background)]",
          "disabled:cursor-not-allowed disabled:opacity-50",
          checked
            ? "bg-emerald-600 dark:bg-emerald-500"
            : "bg-zinc-300 dark:bg-zinc-700",
          className
        )}
        onClick={() => onCheckedChange?.(!checked)}
      >
        <span
          className={cn(
            "pointer-events-none block h-5 w-5 rounded-full bg-[var(--background)] shadow-lg ring-0 transition-transform",
            checked ? "translate-x-5" : "translate-x-0"
          )}
        />
        <input
          ref={ref}
          type="checkbox"
          checked={checked}
          onChange={() => {}}
          className="sr-only"
          {...props}
        />
      </button>
    );
  }
);

Switch.displayName = "Switch";

export { Switch };
