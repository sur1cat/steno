// Язык панели.
//
// Приезжает атрибутом lang в самой разметке — его подставляет steno, когда
// отдаёт index.html, из того же значения, что у CLI. Так первый кадр рисуется
// сразу на нужном языке; запрос к API успел бы показать чужой и переключиться
// на глазах.
//
// Каталог устроен как gettext: ключ перевода — сама русская строка из кода.
// Незнакомая строка возвращается как есть, по-русски: пропуск виден на экране
// и чинится одной строкой в i18n-en.ts, а не ломает вёрстку пустотой.
import { en } from "./i18n-en";

export const lang: "ru" | "en" =
  (typeof document !== "undefined" ? document.documentElement.lang : "") === "ru" ? "ru" : "en";

export const isRU = lang === "ru";

/** Локаль для Intl: даты и числа панель просит у браузера, а не верстает сама. */
export const locale = isRU ? "ru-RU" : "en-US";

export function t(ru: string): string {
  if (isRU) return ru;
  return en[ru] ?? ru;
}

// Ключи тех компонентов, что перенесены из fleety: они зовут t("common.save"),
// а не русскую строку. Словарь переводит ключ в русский оригинал, дальше
// работает общий каталог — так у одной и той же надписи один перевод.
const legacy: Record<string, string> = {
  "common.cancel": "Отмена",
  "common.close": "Закрыть",
  "common.delete": "Удалить",
  "common.save": "Сохранить",
  "common.confirm": "Подтвердить",
  "common.loading": "Загрузка…",
  "common.search": "Поиск",
  "common.nothingFound": "Ничего не найдено",
  "common.selected": "Выбрано",
  "common.clear": "Очистить",
};

export function useTranslation() {
  return { t: (key: string) => t(legacy[key] ?? key), locale: lang };
}
