import { useState, type ReactNode } from "react";
import { Link, NavLink, useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Search, Video } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { InviteDialog } from "@/components/invite-dialog";

const TABS = [
  { to: "/", label: "Созвоны", end: true },
  { to: "/schedule", label: "Расписание" },
  { to: "/tasks", label: "Задачи" },
  { to: "/projects", label: "Проекты" },
  { to: "/settings", label: "Настройки" },
];

// Оболочка приложения. Шапка стоит на месте, прокручивается только содержимое:
// поиск и вкладки нужны с любой строки часовой расшифровки.
export function Layout({ children }: { children: ReactNode }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const [q, setQ] = useState(params.get("q") ?? "");
  const [inviting, setInviting] = useState(false);

  const logout = useMutation({
    mutationFn: () => api.logout(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["session"] }),
  });

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-[var(--background)] text-[var(--foreground)]">
      <header className="shrink-0 border-b border-[var(--border)] bg-[var(--card)]">
        <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center gap-x-6 gap-y-3 px-5 py-3">
          <Link to="/" className="text-lg tracking-tight">
            ste<span className="text-primary">no</span>
          </Link>

          <nav className="flex flex-wrap items-center gap-1">
            {TABS.map((t) => (
              <NavLink
                key={t.to}
                to={t.to}
                end={t.end}
                className={({ isActive }) =>
                  cn(
                    "rounded-lg px-3 py-1.5 text-sm transition-colors",
                    isActive
                      ? "bg-[var(--muted)] text-[var(--foreground)]"
                      : "text-[var(--muted-foreground)] hover:bg-[var(--muted)]/60",
                  )
                }
              >
                {t.label}
              </NavLink>
            ))}
          </nav>

          <form
            role="search"
            className="relative ml-auto min-w-[200px] flex-1 sm:max-w-xs"
            onSubmit={(e) => {
              e.preventDefault();
              nav(`/search?q=${encodeURIComponent(q.trim())}`);
            }}
          >
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--muted-foreground)]" />
            <input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Поиск по всем расшифровкам…"
              autoComplete="off"
              spellCheck={false}
              className="h-9 w-full rounded-xl border-0 bg-[var(--muted)] pl-9 pr-3 text-sm placeholder:text-[var(--muted-foreground)]/70 focus:outline-none focus:ring-2 focus:ring-primary/30"
            />
          </form>

          <Button variant="outline" size="sm" onClick={() => setInviting(true)}>
            <Video className="h-4 w-4" />
            Позвать бота
          </Button>

          <button
            type="button"
            onClick={() => logout.mutate()}
            className="text-sm text-[var(--muted-foreground)] transition-colors hover:text-[var(--foreground)]"
          >
            выйти
          </button>
        </div>
      </header>

      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-6xl px-5 py-7">{children}</div>
      </main>

      <InviteDialog open={inviting} onClose={() => setInviting(false)} />
    </div>
  );
}

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
    <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 className="text-2xl tracking-tight">{title}</h1>
        {sub && <p className="mt-1 text-sm text-[var(--muted-foreground)]">{sub}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
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
