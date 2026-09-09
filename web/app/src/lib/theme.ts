import { useCallback, useSyncExternalStore } from "react";

// Тема панели: светлая, тёмная или «как в системе».
//
// Системная была единственной, и это заметно мешало: панель открывают рядом с
// редактором, у которого тема выбрана руками, и подстроиться под неё было
// нечем. Три состояния, а не переключатель на два, — потому что «как в
// системе» это отдельный осмысленный выбор, а не начальное значение, которое
// исчезает после первого нажатия.
//
// Класс на <html> ставится ДО первой отрисовки скриптом в index.html — иначе
// тёмная тема на секунду показывает светлый экран. Тот скрипт читает этот же
// ключ и повторяет логику resolve() ниже; если менять здесь, надо менять и там.

export type ThemeChoice = "light" | "dark" | "system";

/** Ключ в localStorage. Тот же читает скрипт в index.html. */
export const THEME_STORAGE_KEY = "ui:theme";

/** Смена темы в соседней вкладке долетает через storage; в своей — через это. */
const THEME_EVENT = "steno:theme-changed";

function isChoice(v: unknown): v is ThemeChoice {
  return v === "light" || v === "dark" || v === "system";
}

export function readTheme(): ThemeChoice {
  try {
    const saved = localStorage.getItem(THEME_STORAGE_KEY);
    if (isChoice(saved)) return saved;
  } catch {
    // Приватный режим отдаёт отказ на чтение — это не повод падать.
  }
  return "system";
}

function systemDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** Во что выбор превращается на экране: «системная» смотрит на matchMedia. */
export function resolveTheme(choice: ThemeChoice): "light" | "dark" {
  if (choice === "system") return systemDark() ? "dark" : "light";
  return choice;
}

/**
 * Тот же обмен классов, что делает скрипт в index.html. color-scheme нужен не
 * для красоты: от него зависит цвет системных полос прокрутки и подложки, а её
 * браузер рисует раньше, чем наш фон.
 */
export function applyTheme(choice: ThemeChoice): void {
  const root = document.documentElement;
  const dark = resolveTheme(choice) === "dark";
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
}

const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  window.addEventListener("storage", cb);
  // При выборе «как в системе» смена темы в macOS обязана долетать сама.
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const onSystem = () => {
    if (readTheme() === "system") applyTheme("system");
    cb();
  };
  mq.addEventListener("change", onSystem);
  window.addEventListener(THEME_EVENT, cb);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("storage", cb);
    mq.removeEventListener("change", onSystem);
    window.removeEventListener(THEME_EVENT, cb);
  };
}

/**
 * Центр и радиус круга раскрытия в координатах псевдоэлементов перехода.
 *
 * На входе всё в экранных пикселях: прямоугольник нажатой кнопки и размер окна.
 * Радиус берётся до самого дальнего угла окна — иначе круг остановится, не
 * докрыв экран. Деление на zoom в конце — переход в пространство, в котором
 * браузер рисует ::view-transition-*: масштаб интерфейса ставит `zoom` на
 * <html>, а getBoundingClientRect отдаёт пиксели уже после него.
 */
export function revealGeometry(
  box: { left: number; top: number; width: number; height: number },
  viewport: { width: number; height: number },
  zoom: number,
): { x: number; y: number; r: number } {
  const x = box.left + box.width / 2;
  const y = box.top + box.height / 2;
  const r = Math.hypot(
    Math.max(x, viewport.width - x),
    Math.max(y, viewport.height - y),
  );
  const scale = zoom > 0 ? zoom : 1;
  return { x: x / scale, y: y / scale, r: r / scale };
}

function canReveal(): boolean {
  return (
    typeof document.startViewTransition === "function" &&
    !window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

// Второе нажатие, пока первое раскрытие ещё идёт, заставляет браузер бросить
// первый переход — и его finished разрешается посреди второго. Без счётчика
// первый снял бы маркер из-под второго, и живое раскрытие свалилось бы в
// обычный кроссфейд.
let revealSeq = 0;

function write(choice: ThemeChoice) {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, choice);
  } catch {
    // Не сохранилось — тема всё равно сменится, просто до перезагрузки.
  }
  applyTheme(choice);
  emit();
  window.dispatchEvent(new Event(THEME_EVENT));
}

/**
 * Смена темы. С переданным элементом новая тема расходится кругом оттуда, где
 * человек нажал, а не подменяет кадр целиком: движение показывает, откуда
 * пришло изменение, и заодно прячет тот единственный кадр, на котором половина
 * экрана уже перекрашена, а половина нет. Сама анимация живёт в globals.css —
 * псевдоэлементы ::view-transition-* стилизуются только оттуда.
 */
export function setTheme(choice: ThemeChoice, origin?: Element | null): void {
  const box = origin?.getBoundingClientRect();
  if (!box || !canReveal()) {
    write(choice);
    return;
  }

  const root = document.documentElement;
  const { x, y, r } = revealGeometry(
    box,
    { width: window.innerWidth, height: window.innerHeight },
    parseFloat(getComputedStyle(root).zoom) || 1,
  );
  root.style.setProperty("--theme-reveal-x", `${x}px`);
  root.style.setProperty("--theme-reveal-y", `${y}px`);
  root.style.setProperty("--theme-reveal-r", `${r}px`);
  // Маркер включает правила перехода ровно на нашу смену темы: без него они
  // поймали бы и любой другой view transition, появись он позже.
  root.dataset.themeReveal = "";

  const seq = ++revealSeq;
  const transition = document.startViewTransition(() => write(choice));
  // .then(clear, clear), а не .finally: finished отклоняется, если колбэк
  // бросил, и висящий отказ уехал бы в консоль поверх настоящей ошибки.
  const clear = () => {
    if (seq === revealSeq) delete root.dataset.themeReveal;
  };
  transition.finished.then(clear, clear);
}

/** Выбранная тема и то, во что она развернулась на этом экране. */
export function useTheme(): {
  choice: ThemeChoice;
  resolved: "light" | "dark";
  setTheme: (choice: ThemeChoice, origin?: Element | null) => void;
} {
  const choice = useSyncExternalStore(subscribe, readTheme, () => "system" as const);
  const resolved = useSyncExternalStore(
    subscribe,
    () => (document.documentElement.classList.contains("dark") ? "dark" : "light"),
    () => "light" as const,
  );
  return { choice, resolved, setTheme: useCallback(setTheme, []) };
}
