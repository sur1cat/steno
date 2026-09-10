"use client";

import {
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useTranslation } from "@/lib/i18n";
import { useFocusTrap } from "@/hooks/use-focus-trap";
import { useEscapeHandler } from "./escape-stack";

// The panel's aria-labelledby id, shared down to DialogTitle so the modal is
// named by its own heading (WCAG 4.1.2) without every one of the ~72 consumers
// wiring an id by hand. A title-less dialog leaves the reference dangling,
// which assistive tech treats as "no name" — the same as before.
const DialogTitleIdContext = createContext<string | undefined>(undefined);

interface DialogProps {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  className?: string;
}

// Dialog portals to document.body so the modal escapes any ancestor
// stacking context that would otherwise pin it under sibling chrome
// (navbar dropdowns, sticky headers, framer-motion transformed elements).
// Plain z-index battles do not survive a transformed/isolated ancestor —
// portal is the only fix that's stable across all consumers.
export function Dialog({ open, onClose, children, className }: DialogProps) {
  const [mounted, setMounted] = useState(false);
  const { t } = useTranslation();
  const backdropPointer = useRef("mouse");
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useId();

  useEffect(() => {
    setMounted(true);
  }, []);

  // Escape goes through the shared LIFO stack, not a per-Dialog document
  // listener — otherwise one Escape collapses every nested dialog at once (the
  // case-dialog → service-center bug). No body scroll-lock: globals.css already
  // pins html,body{overflow:hidden} and <main> is the only scroll container, so
  // toggling body.style.overflow is a no-op that also unlocks a still-open
  // parent when a nested dialog closes.
  useEscapeHandler(open, onClose);
  useFocusTrap(panelRef, open && mounted);

  if (!open || !mounted) return null;

  return createPortal(
    <div className="fixed inset-0 z-[1000] flex items-center justify-center p-4">
      {/* Dismissal belongs on the backdrop, never on the overlay. The backdrop is
          `fixed inset-0`, so it is the hit target for every click outside the
          panel and an `e.target === overlay` guard can never match. Keeping the
          handler off the overlay also means a text selection dragged out of the
          panel — whose `click` retargets to their common ancestor — cannot close
          a half-filled form.

          Touch only. A phone has no Escape key and the close button scrolls away
          on a tall dialog, so the backdrop is its last way out. A desktop keeps
          Escape and the button, and ~40 of these dialogs are forms with no
          dirty-check — one slipped mouse click must not erase one. */}
      <div
        aria-hidden="true"
        onPointerDown={(e) => {
          backdropPointer.current = e.pointerType;
        }}
        onClick={() => {
          if (backdropPointer.current === "touch") onClose();
        }}
        className="fixed inset-0 bg-black/50"
      />
      {/* `dvh`, not `vh`: the mobile address bar collapses on scroll and `vh` does
          not follow it. Consumers that set their own height or pin
          `overflow-hidden` still win — `className` is passed last and twMerge
          drops the base of each conflicting pair. */}
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className={cn(
          "relative w-full max-w-lg rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl max-h-[90dvh] overflow-y-auto focus:outline-none",
          className
        )}
      >
        {/* The 16px icon is under the WCAG 2.2 AA 24px floor. Growing the box —
            what sheet.tsx does — would move the icon 4px off the corner on every
            device; this X is a landmark in 72 dialogs. A ::before overlay grows
            the hit area alone, on touch alone, and shifts nothing. */}
        <button
          type="button"
          onClick={onClose}
          aria-label={t("common.close")}
          className="absolute right-4 top-4 rounded-sm opacity-70 hover:opacity-100 transition-opacity pointer-coarse:before:absolute pointer-coarse:before:-inset-2 pointer-coarse:before:content-['']"
        >
          <X className="h-4 w-4" />
        </button>
        <DialogTitleIdContext.Provider value={titleId}>
          {children}
        </DialogTitleIdContext.Provider>
      </div>
    </div>,
    document.body,
  );
}

export function DialogHeader({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mb-4 space-y-1.5", className)}>{children}</div>
  );
}

export function DialogTitle({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  // Adopt the panel's aria-labelledby target so the modal is named by its title.
  const titleId = useContext(DialogTitleIdContext);
  return (
    <h2 id={titleId} className={cn("text-lg font-semibold", className)}>
      {children}
    </h2>
  );
}

export function DialogDescription({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <p className={cn("text-sm text-[var(--muted-foreground)]", className)}>
      {children}
    </p>
  );
}

export function DialogFooter({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mt-6 flex justify-end gap-3", className)}>
      {children}
    </div>
  );
}
