"use client";

import { useEffect, useState } from "react";
import { Input } from "@/components/ui/input";

interface NumberFieldProps {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  /** Committed when the field is left empty or invalid on blur. Defaults to min ?? 0. */
  emptyValue?: number;
  disabled?: boolean;
  className?: string;
}

/**
 * Controlled numeric input that can actually be cleared. A plain
 * `<Input type="number" value={n} onChange={parseInt(...) || 0} />` coerces an
 * empty field straight back to 0, so the leading 0 is impossible to delete —
 * to type "1" you have to type "10" and erase the 0. NumberField keeps a local
 * string mirror: it lets the box be empty / mid-edit and only commits a clamped
 * number, falling back to `emptyValue` (default `min ?? 0`) when blurred empty.
 */
export function NumberField({
  value,
  onChange,
  min,
  max,
  step,
  emptyValue,
  disabled,
  className,
}: NumberFieldProps) {
  const [text, setText] = useState(() => String(value));

  // Re-sync when the value changes from the outside (park switch, save reset),
  // but leave an in-progress edit alone when it already equals the value
  // (e.g. "05" while value is 5 — normalised on blur, not mid-keystroke).
  useEffect(() => {
    if (text === "" || Number(text) !== value) setText(String(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  const clamp = (n: number) => {
    if (min !== undefined) n = Math.max(min, n);
    if (max !== undefined) n = Math.min(max, n);
    return n;
  };

  return (
    <Input
      type="number"
      className={className}
      min={min}
      max={max}
      step={step}
      disabled={disabled}
      value={text}
      onChange={(e) => {
        const raw = e.target.value;
        setText(raw);
        if (raw === "") return; // allow empty while typing; committed on blur
        const n = Number(raw);
        if (!Number.isNaN(n)) onChange(clamp(n));
      }}
      onBlur={() => {
        const fallback = emptyValue ?? min ?? 0;
        const n =
          text === "" || Number.isNaN(Number(text)) ? fallback : clamp(Number(text));
        setText(String(n));
        onChange(n);
      }}
    />
  );
}
