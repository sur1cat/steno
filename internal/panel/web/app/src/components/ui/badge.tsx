import { type HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-full border-0 px-3 py-1 text-xs font-medium transition-colors leading-relaxed",
  {
    variants: {
      variant: {
        default:
          "bg-[var(--muted)] text-[var(--muted-foreground)]",
        secondary:
          "bg-[var(--muted)] text-[var(--muted-foreground)]",
        destructive:
          "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
        outline: "border border-[var(--border)] text-[var(--foreground)]",
        success:
          "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
        warning:
          "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
);

export interface BadgeProps
  extends HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

export { Badge, badgeVariants };
