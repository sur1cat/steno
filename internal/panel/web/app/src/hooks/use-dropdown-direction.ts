"use client";

import { useEffect, useState, type RefObject } from "react";

// Decide whether a dropdown anchored to `anchorRef` should open UP instead of
// down. On a phone the soft keyboard shrinks the visual viewport, so a menu
// rendered below a lower field sits behind the keyboard; flip it above when the
// space below can't hold the menu and there is more room above. Recomputed on
// the visualViewport's resize/scroll (the keyboard opening/closing) and on
// window resize; returns false while closed.
export function useDropdownDirection(
  open: boolean,
  anchorRef: RefObject<HTMLElement | null>,
  estimatedMenuHeight = 240,
): boolean {
  const [dropUp, setDropUp] = useState(false);

  useEffect(() => {
    if (!open) {
      setDropUp(false);
      return;
    }
    const vv = typeof window !== "undefined" ? window.visualViewport : null;
    const compute = () => {
      const el = anchorRef.current;
      if (!el) return;
      const rect = el.getBoundingClientRect();
      const viewTop = vv ? vv.offsetTop : 0;
      const viewBottom = vv ? vv.offsetTop + vv.height : window.innerHeight;
      const spaceBelow = viewBottom - rect.bottom;
      const spaceAbove = rect.top - viewTop;
      // Only flip up when down can't fit AND up has strictly more room — avoids
      // thrashing when both sides are tight.
      setDropUp(spaceBelow < estimatedMenuHeight && spaceAbove > spaceBelow);
    };
    compute();
    vv?.addEventListener("resize", compute);
    vv?.addEventListener("scroll", compute);
    window.addEventListener("resize", compute);
    return () => {
      vv?.removeEventListener("resize", compute);
      vv?.removeEventListener("scroll", compute);
      window.removeEventListener("resize", compute);
    };
  }, [open, anchorRef, estimatedMenuHeight]);

  return dropUp;
}
