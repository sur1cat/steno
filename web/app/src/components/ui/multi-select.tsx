"use client";

import { useState, useRef, useEffect, useCallback, type KeyboardEvent } from "react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { MAX_SEARCH_QUERY_LEN } from "@/lib/constants";
import { useDropdownDirection } from "@/hooks/use-dropdown-direction";

interface Option {
  value: string;
  label: string;
}

interface MultiSelectProps {
  options: Option[];
  value: string[];
  onChange: (value: string[]) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  emptyText?: string;
  /** Server-side search: called with debounced query string. When provided, local filtering is skipped. */
  onSearch?: (query: string) => void;
  /** Show loading spinner while a server search is in progress. */
  isLoading?: boolean;
}

/**
 * Searchable multi-select with removable chips. Sibling of SearchableSelect
 * (single value) — same look, keyboard model and click-outside behaviour, but
 * holds an array and toggles options instead of replacing. Used where a record
 * can attach to several entities (e.g. an expense/income or a loan covering
 * more than one vehicle). Selecting keeps the dropdown open so several picks
 * land in one go; the search box clears after each pick.
 */
export function MultiSelect({
  options,
  value,
  onChange,
  placeholder = "",
  disabled = false,
  className,
  emptyText,
  onSearch,
  isLoading,
}: MultiSelectProps) {
  const [search, setSearch] = useState("");
  const [isOpen, setIsOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const ref = useRef<HTMLDivElement>(null);
  const optionsRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const dropUp = useDropdownDirection(isOpen && !disabled, ref);

  // Remember every label we've ever seen so a chip keeps its text even after a
  // server search swaps the options out from under it (server-side mode only
  // ever holds the latest query's results). Without this, picking a vehicle and
  // then searching again would blank the chip while the id stays selected.
  const labelCacheRef = useRef<Map<string, string>>(new Map());
  for (const o of options) labelCacheRef.current.set(o.value, o.label);
  const selected = value.map((v) => ({ value: v, label: labelCacheRef.current.get(v) ?? v }));

  const displayed = onSearch
    ? options.filter((o) => !value.includes(o.value))
    : options.filter(
        (o) => !value.includes(o.value) && o.label.toLowerCase().includes(search.toLowerCase()),
      );

  const handleSearchChange = useCallback(
    (query: string) => {
      setSearch(query);
      setIsOpen(true);
      setHighlightedIndex(-1);
      if (onSearch) {
        clearTimeout(debounceRef.current);
        debounceRef.current = setTimeout(() => onSearch(query), 300);
      }
    },
    [onSearch],
  );

  useEffect(() => {
    // pointerdown, not mousedown: iOS fires no mouse event for a tap on a
    // non-interactive element, so a phone tap outside never closed the menu.
    const handler = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setIsOpen(false);
    };
    document.addEventListener("pointerdown", handler);
    return () => document.removeEventListener("pointerdown", handler);
  }, []);

  useEffect(() => () => clearTimeout(debounceRef.current), []);

  useEffect(() => {
    if (highlightedIndex < 0) return;
    const el = optionsRef.current?.children[highlightedIndex] as HTMLElement | undefined;
    if (el && typeof el.scrollIntoView === "function") el.scrollIntoView({ block: "nearest" });
  }, [highlightedIndex]);

  const toggle = (val: string) => {
    onChange(value.includes(val) ? value.filter((v) => v !== val) : [...value, val]);
    setSearch("");
    setHighlightedIndex(-1);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (disabled) return;
    if (e.key === "ArrowDown") {
      e.preventDefault(); e.stopPropagation();
      if (!isOpen) setIsOpen(true);
      setHighlightedIndex((prev) => (displayed.length === 0 ? -1 : (prev + 1) % displayed.length));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault(); e.stopPropagation();
      if (!isOpen) setIsOpen(true);
      setHighlightedIndex((prev) => (displayed.length === 0 ? -1 : (prev - 1 + displayed.length) % displayed.length));
      return;
    }
    if (e.key === "Escape") {
      if (isOpen) { e.preventDefault(); e.stopPropagation(); setIsOpen(false); setHighlightedIndex(-1); }
      return;
    }
    if (e.key === "Backspace" && search === "" && value.length > 0) {
      // Quick-remove the last chip when the search box is empty.
      onChange(value.slice(0, -1));
      return;
    }
    if (e.key === "Enter") {
      if (isOpen && highlightedIndex >= 0 && displayed[highlightedIndex]) {
        e.preventDefault(); e.stopPropagation();
        toggle(displayed[highlightedIndex].value);
      }
    }
  };

  return (
    <div ref={ref} className={cn("relative", className)}>
      <div
        className={cn(
          "flex min-h-10 w-full flex-wrap items-center gap-1.5 rounded-lg border border-[var(--input)] bg-[var(--background)] px-2 py-1.5 text-sm focus-within:ring-2 focus-within:ring-[var(--ring)]",
          disabled && "pointer-events-none opacity-60",
        )}
        onClick={() => setIsOpen(true)}
      >
        {selected.map((o) => (
          <span
            key={o.value}
            className="flex items-center gap-1 rounded-md bg-[var(--muted)] py-0.5 pl-2 pr-1 text-xs text-[var(--foreground)]"
          >
            {o.label}
            <button
              type="button"
              tabIndex={-1}
              onMouseDown={(ev) => { ev.preventDefault(); toggle(o.value); }}
              className="flex h-4 w-4 items-center justify-center rounded text-[var(--muted-foreground)] hover:text-red-500 pointer-coarse:h-6 pointer-coarse:w-6"
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
        <input
          type="text"
          value={search}
          onChange={(e) => handleSearchChange(e.target.value)}
          onFocus={() => { setIsOpen(true); setSearch(""); setHighlightedIndex(-1); if (onSearch) onSearch(""); }}
          onKeyDown={handleKeyDown}
          placeholder={selected.length === 0 ? placeholder : ""}
          disabled={disabled}
          maxLength={MAX_SEARCH_QUERY_LEN}
          role="combobox"
          aria-expanded={isOpen}
          aria-autocomplete="list"
          className="h-6 flex-1 min-w-[80px] bg-transparent px-1 text-sm text-[var(--foreground)] focus:outline-none"
        />
      </div>
      {isOpen && !disabled && (
        <div
          ref={optionsRef}
          role="listbox"
          className={cn(
            "absolute z-50 max-h-60 w-full overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--card)] shadow-lg",
            // Flip above the field when the keyboard/viewport leaves no room below.
            dropUp ? "bottom-full mb-1" : "top-full mt-1",
          )}
        >
          {isLoading ? (
            <div className="flex items-center justify-center px-3 py-3">
              <div className="h-4 w-4 animate-spin rounded-full border-2 border-primary border-t-transparent" />
            </div>
          ) : displayed.length === 0 ? (
            <div className="px-3 py-2 text-sm text-[var(--muted-foreground)]">{emptyText || "—"}</div>
          ) : (
            displayed.map((o, idx) => (
              <button
                key={o.value}
                type="button"
                role="option"
                aria-selected={false}
                tabIndex={-1}
                onMouseDown={(ev) => { ev.preventDefault(); toggle(o.value); }}
                onMouseEnter={() => setHighlightedIndex(idx)}
                className={cn(
                  "flex w-full items-center px-3 py-2 text-left text-sm hover:bg-[var(--muted)] transition-colors",
                  idx === highlightedIndex && "bg-[var(--muted)]",
                )}
              >
                {o.label}
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}
