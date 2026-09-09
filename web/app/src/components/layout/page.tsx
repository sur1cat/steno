import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Заголовок страницы с подписью: одна строка о том, что здесь и зачем. */
export function PageHead({
  title,
  sub,
  actions,
}: {
  title: ReactNode;
  sub?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
      <div className="min-w-0">
        <h1 className="text-2xl tracking-tight">{title}</h1>
        {/* Ограничение по ширине здесь несущее: на 1440 подпись в две строки
            растягивалась на весь экран, и глазу приходилось возвращаться от
            правого края к левому ради каждой следующей строки. */}
        {sub && (
          <p className="mt-1 max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
            {sub}
          </p>
        )}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/**
 * Заголовок группы внутри страницы: день в списке созвонов, человек в задачах,
 * раздел в настройках. Единственное место, где эта строка описана, — иначе
 * четыре страницы разъезжаются в мелочах, и однородность, которой и так было
 * слишком много, становится ещё заметнее.
 */
export function GroupHead({
  title,
  count,
  className,
}: {
  title: ReactNode;
  count?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mb-2 flex items-baseline gap-2 px-1", className)}>
      <h2 className="text-sm uppercase tracking-wide text-[var(--muted-foreground)]">{title}</h2>
      {count != null && (
        <span className="text-xs text-[var(--muted-foreground)]/70">{count}</span>
      )}
    </div>
  );
}

/** Пусто — но с объяснением, чего именно ждать. */
export function Empty({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-2xl border border-dashed border-[var(--border)] px-6 py-12 text-center text-sm leading-relaxed text-[var(--muted-foreground)]">
      {children}
    </div>
  );
}

export function Loading() {
  return <div className="py-12 text-center text-sm text-[var(--muted-foreground)]">Загружаю…</div>;
}

export function Failed({ error }: { error: unknown }) {
  const msg = error instanceof Error ? error.message : "что-то сломалось";
  return (
    <div className="rounded-2xl border border-[var(--destructive)]/30 bg-[var(--destructive)]/5 px-5 py-4 text-sm text-[var(--destructive)]">
      {msg}
    </div>
  );
}
