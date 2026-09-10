"use client";

import { useEffect, useRef, useState } from "react";
import { HelpCircle, Info } from "lucide-react";
import { Popover } from "@/components/ui/popover";

interface InfoHintProps {
  title: string;
  // Newline-separated lines, one per panel row.
  body: string;
  // "info" — секционная справка (i, как в ОПиУ); "help" — метрика-подсказка
  // (круг с «?», game-style).
  variant?: "info" | "help";
}

// Задержка закрытия после ухода курсора: панель отпортализована в body и
// отделена от триггера 4px-зазором — без паузы её нельзя достичь курсором.
const hoverCloseDelayMs = 150;

// A small (i)/(?) button explaining a section or metric: what it shows and
// where its numbers come from. Opens on hover (desktop) and on click/tap
// (touch fallback). Content is passed as plain strings so callers keep it
// in i18n.
export function InfoHint({ title, body, variant = "info" }: InfoHintProps) {
  const [open, setOpen] = useState(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lines = body.split("\n").filter((l) => l.trim().length > 0);
  const Icon = variant === "help" ? HelpCircle : Info;

  const cancelClose = () => {
    if (closeTimer.current) {
      clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
  };
  const hoverOpen = () => {
    cancelClose();
    setOpen(true);
  };
  const hoverClose = () => {
    cancelClose();
    closeTimer.current = setTimeout(() => setOpen(false), hoverCloseDelayMs);
  };
  useEffect(() => cancelClose, []);

  return (
    <span className="relative inline-flex">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        onMouseEnter={hoverOpen}
        onMouseLeave={hoverClose}
        aria-label={title}
        className="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--muted-foreground)] transition-colors hover:bg-[var(--muted)] hover:text-[var(--foreground)] pointer-coarse:h-6 pointer-coarse:w-6"
      >
        <Icon className="h-4 w-4" />
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        className="w-80 max-w-[calc(100vw-1rem)] px-4 py-3"
      >
        {/* Курсор, переехавший с триггера на панель, держит её открытой. */}
        <div onMouseEnter={hoverOpen} onMouseLeave={hoverClose}>
          <p className="mb-1.5 text-sm font-semibold">{title}</p>
          <div className="space-y-1">
            {lines.map((line, i) => (
              <p key={i} className="text-xs leading-relaxed text-[var(--muted-foreground)]">
                {line}
              </p>
            ))}
          </div>
        </div>
      </Popover>
    </span>
  );
}
