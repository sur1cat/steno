"use client";

import { useState, useRef, useEffect, useCallback, type KeyboardEvent } from "react";
import { cn } from "@/lib/utils";
import { MAX_SEARCH_QUERY_LEN } from "@/lib/constants";
import { Popover } from "@/components/ui/popover";

interface Option {
  value: string;
  label: string;
}

interface SearchableSelectProps {
  options: Option[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  emptyText?: string;
  /** Server-side search: called with debounced query string. When provided, local filtering is skipped. */
  onSearch?: (query: string) => void;
  /** Show loading spinner when server search is in progress. */
  isLoading?: boolean;
  /** Label for the saved value when it isn't present in `options` (paginated /
   *  filtered / async-loaded list). Without this the input renders blank
   *  even though the form value is set, and operators think the field is empty. */
  fallbackOptionLabel?: string;
}

export function SearchableSelect({
  options,
  value,
  onChange,
  placeholder = "",
  disabled = false,
  className,
  emptyText,
  onSearch,
  isLoading,
  fallbackOptionLabel,
}: SearchableSelectProps) {
  const [search, setSearch] = useState("");
  const [isOpen, setIsOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const ref = useRef<HTMLDivElement>(null);
  const optionsRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const effectiveOptions =
    value && fallbackOptionLabel && !options.some((o) => o.value === value)
      ? [{ value, label: fallbackOptionLabel }, ...options]
      : options;
  const selectedLabel = effectiveOptions.find((o) => o.value === value)?.label || "";

  const handleSearchChange = useCallback(
    (query: string) => {
      setSearch(query);
      if (!isOpen) setIsOpen(true);
      setHighlightedIndex(-1);
      if (onSearch) {
        clearTimeout(debounceRef.current);
        debounceRef.current = setTimeout(() => onSearch(query), 300);
      }
    },
    [isOpen, onSearch],
  );

  useEffect(() => {
    return () => clearTimeout(debounceRef.current);
  }, []);

  // Local filtering only when onSearch is not provided (client-side mode)
  const displayed = onSearch
    ? effectiveOptions
    : effectiveOptions.filter((o) => o.label.toLowerCase().includes(search.toLowerCase()));

  useEffect(() => {
    if (highlightedIndex < 0) return;
    const list = optionsRef.current;
    if (!list) return;
    const el = list.children[highlightedIndex] as HTMLElement | undefined;
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "nearest" });
    }
  }, [highlightedIndex]);

  const selectByIndex = (index: number) => {
    const opt = displayed[index];
    if (!opt) return;
    onChange(opt.value);
    setSearch("");
    setIsOpen(false);
    setHighlightedIndex(-1);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (disabled) return;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      e.stopPropagation();
      if (!isOpen) setIsOpen(true);
      setHighlightedIndex((prev) =>
        displayed.length === 0 ? -1 : (prev + 1) % displayed.length,
      );
      return;
    }

    if (e.key === "ArrowUp") {
      e.preventDefault();
      e.stopPropagation();
      if (!isOpen) setIsOpen(true);
      setHighlightedIndex((prev) =>
        displayed.length === 0
          ? -1
          : (prev - 1 + displayed.length) % displayed.length,
      );
      return;
    }

    if (e.key === "Escape") {
      if (isOpen) {
        e.preventDefault();
        e.stopPropagation();
        setIsOpen(false);
        setHighlightedIndex(-1);
      }
      return;
    }

    // Enter only consumes the event when picking from the open dropdown —
    // otherwise it must bubble so the parent form can handle Enter normally.
    if (e.key === "Enter") {
      if (isOpen && highlightedIndex >= 0 && displayed[highlightedIndex]) {
        e.preventDefault();
        e.stopPropagation();
        selectByIndex(highlightedIndex);
      }
    }
  };

  return (
    <div ref={ref} className={cn("relative", className)}>
      <input
        type="text"
        value={isOpen ? search : selectedLabel}
        onChange={(e) => handleSearchChange(e.target.value)}
        onFocus={() => {
          setIsOpen(true);
          setSearch("");
          setHighlightedIndex(-1);
          if (onSearch) onSearch("");
        }}
        onBlur={() => {
          // Close the dropdown when focus leaves the input (Tab/Enter form-
          // navigation moves focus to the next field). Option clicks use
          // mousedown.preventDefault() to keep the input focused, so picking
          // by mouse still lands before this fires.
          setIsOpen(false);
          setHighlightedIndex(-1);
        }}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        disabled={disabled}
        maxLength={MAX_SEARCH_QUERY_LEN}
        role="combobox"
        aria-expanded={isOpen}
        aria-autocomplete="list"
        className="h-10 w-full rounded-lg border border-[var(--input)] bg-[var(--background)] px-3 text-sm text-[var(--foreground)] focus:outline-none focus:ring-2 focus:ring-[var(--ring)]"
      />
      {/* Меню уезжает в портал через Popover, а не висит absolute внутри поля.
          Абсолютного ребёнка режет ЛЮБОЙ предок с overflow:auto/hidden, а поле
          сплошь и рядом стоит внутри такого: тело модалки скроллится, и в
          «массовой смене тарифа» из двух десятков тарифов было видно три —
          остальные приходилось выкручивать скроллом самой модалки, который
          уносил и меню. Portal снимает обрезание целиком, а заодно приносит
          переворот вверх у нижнего края экрана и z-слой выше диалога. */}
      <Popover
        open={isOpen && !disabled}
        onClose={() => {
          setIsOpen(false);
          setHighlightedIndex(-1);
        }}
        matchWidth
        className="max-h-[min(70vh,24rem)] overflow-y-auto"
      >
        <div ref={optionsRef} role="listbox">
          {isLoading ? (
            <div className="flex items-center justify-center px-3 py-3">
              <div className="h-4 w-4 animate-spin rounded-full border-2 border-primary border-t-transparent" />
            </div>
          ) : displayed.length === 0 ? (
            <div className="px-3 py-2 text-sm text-[var(--muted-foreground)]">
              {emptyText || "—"}
            </div>
          ) : (
            displayed.map((o, idx) => (
              <button
                key={o.value}
                type="button"
                role="option"
                aria-selected={o.value === value}
                // tabIndex={-1} keeps these out of the form-level
                // FOCUSABLE_SELECTOR — Enter/Tab navigation in the parent
                // form must not step into and across each option button.
                tabIndex={-1}
                // mousedown fires before the input's blur, so the click lands
                // even if the input was about to lose focus and close us.
                onMouseDown={(ev) => {
                  ev.preventDefault();
                  selectByIndex(idx);
                }}
                onMouseEnter={() => setHighlightedIndex(idx)}
                className={cn(
                  "flex w-full items-center px-3 py-2 text-left text-sm hover:bg-[var(--muted)] transition-colors",
                  o.value === value && "bg-[var(--muted)] font-medium",
                  idx === highlightedIndex && "bg-[var(--muted)]",
                )}
              >
                {o.label}
              </button>
            ))
          )}
        </div>
      </Popover>
    </div>
  );
}
