"use client";

import { useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Popover } from "@/components/ui/popover";

interface DropdownMenuProps {
  trigger: ReactNode;
  children: ReactNode;
  align?: "left" | "right";
  className?: string;
}

// Built on Popover so the menu portals to <body>: an in-flow absolute menu is
// clipped by any overflow:auto/hidden ancestor (e.g. the driver card's
// overflow-x-auto tab bar), which trapped the «Ещё» menu inside the bar instead
// of floating on top. Popover also owns positioning, outside-tap dismissal
// (pointerdown, so iOS taps work) and Escape.
export function DropdownMenu({
  trigger,
  children,
  align = "right",
  className,
}: DropdownMenuProps) {
  const [open, setOpen] = useState(false);

  return (
    // inline-block hugs the trigger so Popover measures its box; Popover treats
    // this wrapper as the trigger, so the toggle below cleanly opens/closes.
    <div className="relative inline-block">
      <div onClick={() => setOpen((o) => !o)} className="cursor-pointer">
        {trigger}
      </div>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        align={align}
        // w-max: size to the widest item, not the trigger, so long labels don't wrap.
        className={cn("w-max min-w-[180px]", className)}
      >
        <div onClick={() => setOpen(false)}>{children}</div>
      </Popover>
    </div>
  );
}

interface DropdownItemProps {
  children: ReactNode;
  onClick?: () => void;
  className?: string;
  destructive?: boolean;
}

export function DropdownItem({
  children,
  onClick,
  className,
  destructive,
}: DropdownItemProps) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 px-3 py-2 text-sm transition-colors hover:bg-[var(--muted)]",
        destructive && "text-[var(--destructive)]",
        className
      )}
    >
      {children}
    </button>
  );
}

export function DropdownSeparator() {
  return <div className="my-1 h-px bg-[var(--border)]" />;
}
