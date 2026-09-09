// Панель steno русская и только русская, поэтому полноценной локализации здесь
// нет — есть словарь тех ключей, которые используют компоненты, перенесённые
// из fleety. Интерфейс useTranslation сохранён, чтобы компоненты работали без
// правок и их можно было обновлять из fleety копированием.

const ru: Record<string, string> = {
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
  return {
    // Незнакомый ключ возвращается как есть: это заметно на экране и чинится
    // одной строкой в словаре выше, а не молча ломает вёрстку.
    t: (key: string) => ru[key] ?? key,
    locale: "ru" as const,
  };
}
