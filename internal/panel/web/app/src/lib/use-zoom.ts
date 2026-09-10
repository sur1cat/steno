"use client";

import { useCallback, useEffect, useState } from "react";
import { ZOOM_OPTIONS, DEFAULT_ZOOM, ZOOM_STORAGE_KEY } from "@/lib/constants";

// Same-tab broadcast channel — `storage` events fire cross-tab only, so sibling
// controls in the same document sync via this CustomEvent. Mirrors the
// useParkSelector / useFinanceParks idiom (storage event + CustomEvent).
// Экспортируется: на смену масштаба пересчитывает свою геометрию ещё и
// Popover (components/ui/popover.tsx) — CSS zoom не поднимает resize.
export const ZOOM_EVENT = "fleety:zoom-changed";

function isValidZoom(n: number): boolean {
  return (ZOOM_OPTIONS as readonly number[]).includes(n);
}

function readStoredZoom(): number {
  if (typeof window === "undefined") return DEFAULT_ZOOM;
  try {
    const saved = Number(localStorage.getItem(ZOOM_STORAGE_KEY));
    if (isValidZoom(saved)) return saved;
  } catch {}
  return DEFAULT_ZOOM;
}

function applyZoom(percent: number) {
  if (typeof document === "undefined") return;
  // CSS `zoom` on the root element is the closest thing to browser Ctrl+zoom:
  // it scales the whole document (px, rem, and body-rooted portals) and reflows.
  // Idempotent: skip the write (and its reflow) when the value already matches —
  // on a normal load the pre-hydration script in app/layout.tsx already set it.
  //
  // Gated OFF below the sm breakpoint (640px): `zoom` does not rescale viewport
  // units and, worse, Tailwind computes breakpoints from the UN-zoomed width —
  // a 1.5x scale would make `sm:` fire at a phone width and break the mobile
  // layout. Clearing to "" restores the browser default (100%).
  const mobile = typeof window !== "undefined" && window.innerWidth < 640;
  const next = mobile ? "" : String(percent / 100);
  if (document.documentElement.style.zoom === next) return;
  document.documentElement.style.zoom = next;
}

// useZoom is the source of truth for the per-browser UI scale. The DOM is also
// set by the pre-hydration script in app/layout.tsx (to avoid FOUC); the effect
// here re-applies the validated value on mount, which also reconciles the DOM if
// localStorage held a tampered/out-of-range value the lenient script let through.
// Cross-control + cross-tab sync mirrors useParkSelector/useFinanceParks. The
// read/validate/persist shape parallels usePageSize but is NOT extracted into a
// shared helper — it is the second copy (DRY rule of three) and useZoom also owns
// the DOM side-effect + sync that usePageSize lacks.
export function useZoom(): [number, (next: number) => void] {
  const [zoom, setZoomState] = useState<number>(readStoredZoom);

  useEffect(() => {
    applyZoom(zoom);
    // Re-apply when the window crosses the sm breakpoint (desktop resize, tablet
    // rotate) so the gate engages/releases without a reload.
    const onResize = () => applyZoom(zoom);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [zoom]);

  // Stay in sync with sibling controls (same tab, via ZOOM_EVENT) and with other
  // tabs (via the `storage` event, which re-reads the authoritative localStorage).
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key !== ZOOM_STORAGE_KEY) return;
      setZoomState(readStoredZoom());
    };
    const onLocal = (e: Event) => {
      // Validate the broadcast value (symmetry with onStorage) so a stray
      // same-named event can't push a bad scale into state / the DOM.
      const next = (e as CustomEvent<number>).detail;
      if (isValidZoom(next)) setZoomState(next);
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(ZOOM_EVENT, onLocal as EventListener);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(ZOOM_EVENT, onLocal as EventListener);
    };
  }, []);

  const setZoom = useCallback((next: number) => {
    if (!isValidZoom(next)) return;
    setZoomState(next);
    try {
      localStorage.setItem(ZOOM_STORAGE_KEY, String(next));
    } catch {}
    window.dispatchEvent(new CustomEvent(ZOOM_EVENT, { detail: next }));
  }, []);

  return [zoom, setZoom];
}
