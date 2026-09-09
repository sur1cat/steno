import {
  CalendarDays,
  FolderKanban,
  ListChecks,
  Settings,
  Video,
  type LucideIcon,
} from "lucide-react";

// Разделы панели одним списком. Рисуют его двое — колонка на широком экране и
// выдвижная панель на узком, — и оба берут отсюда: разъехавшаяся навигация,
// в которой на телефоне не хватает пункта, заводится ровно из двух копий.
//
// Подпись — не украшение. «Задачи» и «Проекты» рядом звучат одинаково, и без
// строчки о том, чем они отличаются, человек открывает обе, чтобы вспомнить.

export interface NavItem {
  to: string;
  label: string;
  about: string;
  icon: LucideIcon;
  /** Точное совпадение пути: иначе «/» подсвечивался бы на каждой странице. */
  end?: boolean;
  /** Ветки, которые тоже принадлежат разделу: /m/… — открытый созвон. */
  owns?: string[];
}

export const NAV_ITEMS: NavItem[] = [
  {
    to: "/",
    label: "Созвоны",
    about: "Что уже прошло",
    icon: Video,
    end: true,
    owns: ["/m/", "/search"],
  },
  {
    to: "/schedule",
    label: "Расписание",
    about: "Куда бот пойдёт",
    icon: CalendarDays,
  },
  {
    to: "/tasks",
    label: "Задачи",
    about: "Кто что обещал",
    icon: ListChecks,
  },
  {
    to: "/projects",
    label: "Проекты",
    about: "По делу, а не по встречам",
    icon: FolderKanban,
    owns: ["/p/"],
  },
  {
    to: "/settings",
    label: "Настройки",
    about: "Проекты, каналы, секреты",
    icon: Settings,
  },
];

/** Раздел, которому принадлежит адрес. Нужен и подсветке, и заголовку шапки. */
export function activeItem(pathname: string): NavItem | undefined {
  return NAV_ITEMS.find((it) =>
    it.end
      ? pathname === it.to || (it.owns ?? []).some((p) => pathname.startsWith(p))
      : pathname === it.to ||
        pathname.startsWith(it.to + "/") ||
        (it.owns ?? []).some((p) => pathname.startsWith(p)),
  );
}
