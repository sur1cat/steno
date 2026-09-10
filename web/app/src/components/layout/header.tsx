import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LogOut, Menu, Search } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { buildBreadcrumbs } from "./breadcrumbs";
import { ThemeToggle } from "./theme-toggle";
import { t } from "@/lib/i18n";

// Шапка. Раньше в неё было забито всё сразу: логотип, пять вкладок, поиск, две
// кнопки и «выйти» — одной строкой во весь экран. Разделы уехали в колонку
// слева, и здесь осталось то, что к шапке и относится: где я сейчас (слева) и
// чем управляю (справа).

export function Header({ onOpenNav }: { onOpenNav: () => void }) {
  const { pathname } = useLocation();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const [q, setQ] = useState(params.get("q") ?? "");

  // Уйдя с поиска, строка обязана очиститься: иначе прошлый запрос висит в
  // шапке на всех страницах и выглядит как действующий фильтр.
  useEffect(() => {
    setQ(pathname.startsWith("/search") ? (params.get("q") ?? "") : "");
  }, [pathname, params]);

  const logout = useMutation({
    mutationFn: () => api.logout(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["session"] }),
  });

  const crumbs = buildBreadcrumbs(pathname);

  return (
    <header className="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--border)] bg-[var(--card)] px-3 sm:px-5">
      <button
        type="button"
        onClick={onOpenNav}
        aria-label={t("Разделы")}
        className="-ml-1 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-[var(--muted-foreground)] transition-colors hover:bg-[var(--muted)] hover:text-[var(--foreground)] lg:hidden"
      >
        <Menu className="h-5 w-5" />
      </button>

      <nav aria-label={t("Хлебные крошки")} className="flex min-w-0 items-center gap-1.5">
        {crumbs.map((c, i) => (
          <span key={`${c.label}-${i}`} className="flex min-w-0 items-center gap-1.5">
            {i > 0 && <span className="text-xs text-[var(--muted-foreground)]">/</span>}
            {c.to ? (
              <Link
                to={c.to}
                className="truncate text-sm text-[var(--muted-foreground)] transition-colors hover:text-[var(--foreground)]"
              >
                {c.label}
              </Link>
            ) : (
              <span className="truncate text-sm">{c.label}</span>
            )}
          </span>
        ))}
      </nav>

      <form
        role="search"
        className="relative ml-auto hidden w-full max-w-xs md:block"
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
          placeholder={t("Поиск по расшифровкам…")}
          autoComplete="off"
          spellCheck={false}
          className="h-9 w-full rounded-xl border-0 bg-[var(--muted)] pl-9 pr-3 text-sm placeholder:text-[var(--muted-foreground)]/70 focus:outline-none focus:ring-2 focus:ring-primary/30"
        />
      </form>

      {/* Ниже md строка поиска не помещается, но и пропасть не должна: своя
          строка есть у страницы поиска, туда и ведём. */}
      <Link
        to="/search"
        aria-label={t("Поиск")}
        className="ml-auto flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-[var(--muted-foreground)] transition-colors hover:bg-[var(--muted)] hover:text-[var(--foreground)] md:ml-0 md:hidden"
      >
        <Search className="h-[18px] w-[18px]" />
      </Link>

      <div className="flex shrink-0 items-center gap-1">
        <ThemeToggle />
        <Button
          variant="ghost"
          size="sm"
          className="h-9 gap-2 px-2 text-[var(--muted-foreground)] sm:px-3"
          onClick={() => logout.mutate()}
        >
          <LogOut className="h-4 w-4" />
          <span className="hidden sm:inline">{t("выйти")}</span>
        </Button>
      </div>
    </header>
  );
}
