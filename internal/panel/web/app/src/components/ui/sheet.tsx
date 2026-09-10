"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { motion, AnimatePresence } from "framer-motion";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useTranslation } from "@/lib/i18n";
import { useFocusTrap } from "@/hooks/use-focus-trap";
import { useEscapeHandler } from "./escape-stack";

// Slide-over panel. Portals to document.body for the same reason Dialog does:
// a transformed/isolated ancestor (framer-motion navbar, sticky header) pins
// any z-index the panel could claim, and a portal is the only stable fix.
//
// No body scroll-lock here: globals.css already pins `html, body { overflow:
// hidden }` and <main> is the app's only scroll container, so toggling
// body.style.overflow would be a no-op in both directions.
//
// Slides in from the left, the only geometry with a caller. A `side` prop and
// its lookup table belong to the phase that adds the second geometry (the GPS
// bottom-sheet), together with its test — not here, ahead of any consumer.

const HIDDEN = { x: "-100%" };
const SHOWN = { x: 0 };

interface SheetProps {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  /** Accessible name — the primitive renders no visible title of its own. */
  label: string;
  className?: string;
}

export function Sheet({ open, onClose, children, label, className }: SheetProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const [mounted, setMounted] = useState(false);
  const { t } = useTranslation();

  useEffect(() => setMounted(true), []);

  useEscapeHandler(open, onClose);
  useFocusTrap(panelRef, open && mounted);

  if (!mounted) return null;

  return createPortal(
    <AnimatePresence>
      {open && (
        <div className="fixed inset-0 z-[1000]">
          <motion.div
            aria-hidden="true"
            onClick={onClose}
            className="absolute inset-0 bg-black/50"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
          />
          <motion.div
            ref={panelRef}
            role="dialog"
            aria-modal="true"
            aria-label={label}
            tabIndex={-1}
            initial={HIDDEN}
            animate={SHOWN}
            exit={HIDDEN}
            transition={{ duration: 0.2, ease: "easeOut" }}
            className={cn(
              "absolute inset-y-0 left-0 flex w-[min(20rem,85vw)] flex-col border-r border-[var(--border)] bg-[var(--card)] shadow-xl",
              className
            )}
          >
            <button
              type="button"
              onClick={onClose}
              aria-label={t("common.close")}
              className="absolute right-3 top-3 z-10 flex h-6 w-6 items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100"
            >
              <X className="h-4 w-4" />
            </button>
            {/* The panel itself must not scroll: an absolutely-positioned child
                of a scroll container scrolls away with the content, and the
                close button is that child. Scroll the content instead.
                `min-h-0` lets this flex child shrink below its content size —
                without it it would grow past the panel and never scroll. */}
            <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
          </motion.div>
        </div>
      )}
    </AnimatePresence>,
    document.body
  );
}
