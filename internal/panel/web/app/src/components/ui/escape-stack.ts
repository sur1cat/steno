"use client";

import { useEffect, useRef } from "react";

// Shared LIFO stack of Escape handlers for overlays (Sheet today, Dialog next).
//
// Every open overlay pushes its onClose here INSTEAD of registering its own
// `document.addEventListener("keydown")`. A single global listener invokes only
// the topmost handler, so one Escape closes one overlay.
//
// Load-bearing: do not "simplify" this back into a per-component keydown. That
// is exactly the nested-Dialog bug where a single Escape collapsed the whole
// stack (downtime register: case-dialog → service-center-create-dialog).

type EscapeHandler = () => void;

const stack: EscapeHandler[] = [];
let listening = false;

function handleKeyDown(e: KeyboardEvent) {
  if (e.key !== "Escape") return;
  const top = stack[stack.length - 1];
  if (!top) return;
  e.stopPropagation();
  top();
}

/** Registers `handler` as the topmost layer. Returns its unsubscribe. */
export function pushEscapeHandler(handler: EscapeHandler): () => void {
  stack.push(handler);
  if (!listening) {
    document.addEventListener("keydown", handleKeyDown);
    listening = true;
  }

  return () => {
    const idx = stack.lastIndexOf(handler);
    if (idx !== -1) stack.splice(idx, 1);
    if (stack.length === 0 && listening) {
      document.removeEventListener("keydown", handleKeyDown);
      listening = false;
    }
  };
}

/**
 * React binding: keeps `handler` on the stack for as long as `active`.
 *
 * The subscription deliberately ignores `handler`'s identity. Callers pass an
 * inline `onClose={() => setOpen(false)}`, so re-subscribing on every parent
 * render would splice the handler out of the middle of the stack and push it
 * back on top — silently promoting a background overlay above the one the user
 * is actually looking at. Subscribe once per active-cycle; read the latest
 * callback through a ref.
 */
export function useEscapeHandler(active: boolean, handler: EscapeHandler) {
  const handlerRef = useRef(handler);
  useEffect(() => {
    handlerRef.current = handler;
  });

  useEffect(() => {
    if (!active) return;
    return pushEscapeHandler(() => handlerRef.current());
  }, [active]);
}
