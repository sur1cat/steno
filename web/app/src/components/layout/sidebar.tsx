import { Link, useLocation } from "react-router-dom";
import { Upload, Video } from "lucide-react";
import { cn } from "@/lib/utils";
import { NAV_ITEMS, activeItem } from "./nav-items";

// Навигация стоит колонкой слева, а не строкой вкладок в шапке.
//
// Строка вкладок ломалась дважды. На широком экране она сплющивала логотип,
// пять разделов, поиск и две кнопки в одну линию — и всё это читалось как один
// ряд одинаковых слов, в котором не видно, где кончается навигация и начинается
// действие. На узком та же строка просто переносилась и разваливалась.
// Колонке добавить шестой раздел ничего не стоит, а на телефоне она уезжает
// целиком в выдвижную панель — тем же списком, из того же nav-items.ts.
//
// Цвета берутся из --sidebar-*: они уже лежали в globals.css, перенесённые из
// fleety вместе с компонентами, и не использовались. Тёмная колонка на светлом
// фоне — это то самое «за что зацепиться глазу», чего не хватало: раздел, где
// человек находится, виден раньше, чем прочитан.

const ROW =
  "flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition-colors outline-offset-[-2px]";
const IDLE =
  "text-[var(--sidebar-text)] hover:bg-[var(--sidebar-hover-bg)] hover:text-white";
const ACTIVE = "bg-[var(--sidebar-active-bg)] text-[var(--sidebar-active-text)]";

/** Список разделов. Один на колонку и на выдвижную панель. */
export function NavList({ onNavigate }: { onNavigate?: () => void }) {
  const { pathname } = useLocation();
  const active = activeItem(pathname);

  return (
    <nav className="flex flex-col gap-0.5 p-3">
      {NAV_ITEMS.map((item) => {
        const on = active?.to === item.to;
        return (
          <Link
            key={item.to}
            to={item.to}
            onClick={onNavigate}
            aria-current={on ? "page" : undefined}
            className={cn(ROW, on ? ACTIVE : IDLE)}
          >
            <item.icon className="h-[18px] w-[18px] shrink-0" />
            <span className="min-w-0 flex-1">
              <span className="block truncate">{item.label}</span>
              {/* Подпись нужна не всем, но «Задачи» и «Проекты» без неё
                  различаются только на ощупь. */}
              <span className="block truncate text-[11px] text-[var(--sidebar-text-muted)]">
                {item.about}
              </span>
            </span>
          </Link>
        );
      })}
    </nav>
  );
}

/** Два действия, которые заводят созвон: бот сходит сам или запись принесут. */
export function NavActions({
  onInvite,
  onUpload,
  onNavigate,
}: {
  onInvite: () => void;
  onUpload: () => void;
  onNavigate?: () => void;
}) {
  const act = (fn: () => void) => () => {
    onNavigate?.();
    fn();
  };
  return (
    <div className="flex flex-col gap-1.5 border-t border-[var(--sidebar-border)] p-3">
      <button
        type="button"
        onClick={act(onInvite)}
        className="flex items-center gap-3 rounded-xl bg-primary/15 px-3 py-2.5 text-sm text-[var(--sidebar-active-text)] transition-colors hover:bg-primary/25"
      >
        <Video className="h-[18px] w-[18px] shrink-0" />
        Позвать бота
      </button>
      <button
        type="button"
        onClick={act(onUpload)}
        className={cn(ROW, IDLE, "w-full")}
      >
        <Upload className="h-[18px] w-[18px] shrink-0" />
        <span className="min-w-0 flex-1 text-left">
          <span className="block">Загрузить запись</span>
          <span className="block truncate text-[11px] text-[var(--sidebar-text-muted)]">
            Zoom, телефон, что угодно
          </span>
        </span>
      </button>
    </div>
  );
}

export function Wordmark({ onClick }: { onClick?: () => void }) {
  return (
    <Link
      to="/"
      onClick={onClick}
      className="flex h-14 shrink-0 items-center gap-2 border-b border-[var(--sidebar-border)] px-5 text-lg tracking-tight text-[var(--sidebar-logo-text)]"
    >
      ste<span className="-ml-2 text-[var(--sidebar-active-text)]">no</span>
    </Link>
  );
}

/** Колонка на широком экране. Ниже lg её заменяет выдвижная панель. */
export function Sidebar({
  onInvite,
  onUpload,
}: {
  onInvite: () => void;
  onUpload: () => void;
}) {
  return (
    <aside className="hidden w-60 shrink-0 flex-col bg-[var(--sidebar-bg)] lg:flex">
      <Wordmark />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <NavList />
      </div>
      <NavActions onInvite={onInvite} onUpload={onUpload} />
    </aside>
  );
}
