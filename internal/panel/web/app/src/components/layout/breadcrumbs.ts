import { NAV_ITEMS, activeItem } from "./nav-items";
import { t } from "@/lib/i18n";

// Где человек сейчас — словами, в шапке. Раньше об этом говорила только
// подсветка вкладки, а на открытом созвоне не говорило ничего: заголовок
// страницы называл сам созвон, и из какого он раздела приходилось помнить.

export interface Crumb {
  label: string;
  /** Последняя крошка никуда не ведёт: это и есть текущая страница. */
  to?: string;
}

/** «Созвоны / Созвон», «Проекты / Платежи». */
export function buildBreadcrumbs(pathname: string): Crumb[] {
  // Поиск — не раздел: попадают в него из строки в шапке, и в колонке слева
  // его нет. Но и безымянным он оставаться не должен.
  if (pathname.startsWith("/search")) {
    return [{ label: t("Созвоны"), to: "/" }, { label: t("Поиск") }];
  }
  if (pathname.startsWith("/m/")) {
    return [{ label: t("Созвоны"), to: "/" }, { label: t("Созвон") }];
  }
  if (pathname.startsWith("/s/")) {
    return [{ label: t("Проекты"), to: "/projects" }, { label: t("ТЗ") }];
  }
  if (pathname.startsWith("/p/")) {
    return [
      { label: t("Проекты"), to: "/projects" },
      { label: decodeURIComponent(pathname.slice("/p/".length)) },
    ];
  }
  return [{ label: (activeItem(pathname) ?? NAV_ITEMS[0]).label }];
}
