"use client";

import { type ReactNode } from "react";
import { useTranslation } from "@/lib/i18n";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";

interface ConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  isPending?: boolean;
}

// ConfirmDialog is the themed replacement for window.confirm on destructive
// actions. Stays in the app's design system (dark mode, focus rings) and
// allows custom button labels + a loading state for the mutation.
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel,
  cancelLabel,
  destructive = true,
  isPending = false,
}: ConfirmDialogProps) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onClose={onClose}>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
      </DialogHeader>
      {description && (
        <p className="text-sm text-[var(--muted-foreground)] py-2">{description}</p>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onClose} disabled={isPending}>
          {cancelLabel ?? t("common.cancel")}
        </Button>
        <Button
          variant={destructive ? "destructive" : "default"}
          onClick={onConfirm}
          disabled={isPending}
        >
          {confirmLabel ?? t("common.delete")}
        </Button>
      </DialogFooter>
    </Dialog>
  );
}
