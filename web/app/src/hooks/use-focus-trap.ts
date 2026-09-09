"use client";

import { useEffect, type RefObject } from "react";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Confines Tab focus to `panelRef` while `active`, restoring the previously
 * focused element on deactivation.
 *
 * Callers must pass `open && mounted`, not just `open`. An overlay that portals
 * after mount renders nothing on the first pass, so an `[open]`-only dep list
 * runs this effect once against `panelRef.current === null` and the trap never
 * attaches — silently, and only on the mount-with-open path.
 */
export function useFocusTrap(panelRef: RefObject<HTMLElement | null>, active: boolean) {
  useEffect(() => {
    if (!active) return;
    const panel = panelRef.current;
    if (!panel) return;

    const restore = document.activeElement as HTMLElement | null;
    const focusables = () => Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE));
    // Don't steal focus from an element the panel already placed it on — an
    // `autoFocus` input (React commits it before this effect runs) or a caller
    // that focused something itself. Only pull focus in when it is still
    // outside the panel.
    if (!panel.contains(document.activeElement)) {
      (focusables()[0] ?? panel).focus();
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Tab") return;
      const items = focusables();
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };

    panel.addEventListener("keydown", onKeyDown);
    return () => {
      panel.removeEventListener("keydown", onKeyDown);
      restore?.focus?.();
    };
  }, [active, panelRef]);
}
