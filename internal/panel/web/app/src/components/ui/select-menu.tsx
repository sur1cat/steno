"use client";

import { Fragment, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Check, ChevronDown } from "lucide-react";
import { Popover } from "./popover";
import { cn } from "@/lib/utils";

// Общая выпадашка приложения: кнопка с текущим значением и список под ней.
// Отсюда её берут селектор парка, фильтры тулбаров и поля форм — чтобы одно и
// то же понятие не рисовалось четырьмя разными виджетами, как было с нативным
// <select> (241 использование в 74 файлах на момент выноса).
//
// Нативный <select> тут не годится не из вкусовщины: РАСКРЫТЫЙ список рисует
// браузер своим оформлением — в тёмной теме он выпадает светлым прямоугольником
// поверх карточки, а шрифт и высота строки не совпадают ни с чем вокруг. До его
// содержимого странице не дотянуться никаким классом.
//
// Панель идёт через общий Popover, то есть портируется в body. Первая версия
// этого компонента портал НЕ использовала, и обоснование было — «ручной
// пересчёт координат ломается о CSS zoom». Обоснование протухло: расчёт в
// Popover самокалибруется с 66a26343 и даёт 0px сноса на любом масштабе. А без
// портала панель платила четырьмя дефектами сразу: её резал overflow-контейнер
// (воронка заявок), запирал контекст наложения предка (потребовалось восемь
// заплаток `relative z-20` и сторож-тест, который композиция всё равно обошла),
// она не переворачивалась вверх у нижнего края (PageSizeSelect стоит в подвале
// четырнадцати списков) и не зажималась по краю экрана. Popover умеет всё это
// сам, поэтому здесь остаётся только содержимое и клавиатура.

export interface SelectMenuOption {
  value: string;
  label: string;
  /** Заголовок группы — замена <optgroup>. Рисуется, когда меняется у соседних
   *  строк, поэтому список должен идти уже сгруппированным. */
  group?: string;
}

// Кнопка-триггер + список. Содержимое списка отдаёт вызывающий: простому
// выбору хватает строк, селектору парка нужна ещё звезда на каждой.
//
// className вешается на ОБЁРТКУ, а не на кнопку: в раскладке родителя участвует
// именно она. Пока класс уходил на кнопку, `sm:hidden` прятал кнопку, но
// оставлял обёртку flex-ребёнком — лишний зазор в ряду и висящий список без
// триггера, если раздвинуть окно с открытым меню. Вид самой кнопки — через
// triggerClassName.
export function MenuShell({
  summary,
  children,
  label,
  onBlur,
  align = "left",
  disabled,
  className,
  triggerClassName,
  panelClassName = "w-56",
  // По умолчанию ВКЛЮЧЕНО: список не должен быть уже своей кнопки. Отдельного
  // w-full у панели быть не может — она портируется в body, и «сто процентов»
  // там означают ширину страницы, а не поля (на создании водителя список
  // гражданства раскрывался во весь экран). w-56 остаётся полом для узких
  // тулбарных кнопок.
  matchWidth = true,
  placeholderShown,
}: {
  summary: string;
  children: (close: () => void) => ReactNode;
  label?: string;
  /** Приходил спредом {...field} от Controller. Формы проекта валидируются по
   *  сабмиту, поэтому сейчас ни на что не влияет, — но проброс бесплатный, а
   *  без него переключение формы на mode: "onBlur" молча перестало бы работать. */
  onBlur?: () => void;
  align?: "left" | "right";
  disabled?: boolean;
  className?: string;
  triggerClassName?: string;
  panelClassName?: string;
  /** Панель шириной не уже триггера. Для «выпадашки во всю ширину поля». */
  matchWidth?: boolean;
  /** Значение не выбрано — подпись показывается приглушённой, как placeholder. */
  placeholderShown?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const listId = useId();
  const labelId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Закрытие возвращает фокус на кнопку — иначе он проваливается в body, и
  // обход с клавиатуры начинается заново с начала документа.
  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };

  // Выключение на лету закрывает открытый список. Атрибут disabled гасит только
  // кнопку: пока панель оставалась смонтированной, строки продолжали писать
  // значение — например, прямо во время сохранения формы настроек парка.
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);

  const optionEls = () =>
    Array.from(listRef.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? []);

  // Фокус на выбранной строке при открытии — иначе стрелкам не от чего плясать.
  useEffect(() => {
    if (!open) return;
    const list = optionEls();
    (list.find((o) => o.getAttribute("aria-selected") === "true") ?? list[0])?.focus();
  }, [open]);

  // Клавиатура. Нативный <select> давал её бесплатно: стрелки, Home/End и
  // прыжок по первой букве. Своя кнопка со списком не даёт ничего, и человек,
  // дошедший до неё табом, оказывался в тупике.
  const onKeyDown = (e: React.KeyboardEvent) => {
    const list = optionEls();
    if (list.length === 0) return;
    const current = list.findIndex((o) => o === document.activeElement);
    const focusAt = (i: number) => {
      e.preventDefault();
      list[(i + list.length) % list.length]?.focus();
    };
    switch (e.key) {
      case "ArrowDown":
        return focusAt(current + 1);
      case "ArrowUp":
        return focusAt(current - 1);
      case "Home":
        return focusAt(0);
      case "End":
        return focusAt(list.length - 1);
      case "Tab":
        // Закрываем ВМЕСТЕ с возвратом фокуса: панель снимается синхронно, и
        // без возврата Tab уводил бы обход с начала документа, а не со
        // следующего за кнопкой элемента.
        close();
        return;
      default:
        if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
          const key = e.key.toLowerCase();
          const from = current + 1;
          const ordered = list
            .map((o, i) => ({ el: o, dist: (i - from + list.length) % list.length }))
            .filter(({ el }) => (el.textContent ?? "").trim().toLowerCase().startsWith(key))
            .sort((a, b) => a.dist - b.dist);
          if (ordered.length > 0) focusAt(list.indexOf(ordered[0].el));
        }
    }
  };

  return (
    <div className={cn("relative", className)}>
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        onBlur={onBlur}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
            e.preventDefault();
            setOpen(true);
          }
        }}
        // role=combobox — паттерн ARIA 1.2 для выпадашки без поля ввода:
        // кнопка, раскрывающая listbox. Иначе скринридер объявляет её обычной
        // кнопкой, не сообщая ни про список, ни про раскрытость.
        //
        // Имя — через aria-labelledby на скрытую подпись, а НЕ из содержимого:
        // combobox по спецификации имя из содержимого не берёт, и с ролью, но
        // без labelledby контрол оставался безымянным вовсе. Текст кнопки при
        // этом читается как ЗНАЧЕНИЕ — ровно то разделение, которого не было у
        // aria-label (тот перекрывал значение подписью).
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-labelledby={label ? labelId : undefined}
        className={cn(
          "flex h-9 max-w-[220px] items-center justify-between gap-1.5 rounded-md border border-[var(--input)] bg-[var(--background)] px-2.5 text-sm text-[var(--foreground)] transition-colors hover:bg-[var(--muted)] disabled:cursor-not-allowed disabled:opacity-50 focus:outline-none focus:ring-2 focus:ring-[var(--ring)]",
          triggerClassName,
        )}
      >
        {/* Подпись отдельным скрытым узлом, а не aria-label: тот ПЕРЕКРЫВАЕТ
            текст кнопки при вычислении имени, и выбранное значение переставало
            произноситься вовсе. */}
        {label && <span id={labelId} className="sr-only">{label}</span>}
        <span className={cn("truncate", placeholderShown && "text-[var(--muted-foreground)]")}>
          {summary}
        </span>
        <ChevronDown
          className={cn(
            "h-4 w-4 shrink-0 text-[var(--muted-foreground)] transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        align={align}
        matchWidth={matchWidth}
        className={cn("max-h-72 overflow-y-auto", panelClassName)}
      >
        <div ref={listRef} id={listId} role="listbox" aria-label={label} onKeyDown={onKeyDown}>
          {children(close)}
        </div>
      </Popover>
    </div>
  );
}

// Строка списка. Галочка занимает место всегда (opacity, а не условный рендер),
// иначе выбранная строка сдвигалась бы относительно остальных.
//
// role="option" стоит на самой строке: на вложенном узле скринридер объявлял бы
// список без единого пункта. trailing (например звезда «парк по умолчанию»)
// кладётся сюда же соседом.
export function MenuRow({
  label,
  checked,
  muted,
  onClick,
  trailing,
}: {
  label: string;
  checked: boolean;
  muted?: boolean;
  onClick: () => void;
  trailing?: ReactNode;
}) {
  return (
    <div
      role="option"
      aria-selected={checked}
      tabIndex={-1}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      className="group flex cursor-pointer items-center hover:bg-[var(--muted)] focus:bg-[var(--muted)] focus:outline-none"
    >
      <span
        className={cn(
          "flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left text-sm",
          muted && "text-[var(--muted-foreground)]",
        )}
      >
        <Check className={cn("h-4 w-4 shrink-0 text-primary", !checked && "opacity-0")} />
        <span className="truncate">{label}</span>
      </span>
      {trailing}
    </div>
  );
}

export function MenuDivider() {
  return <div className="my-1 h-px bg-[var(--border)]" />;
}

// Заголовок группы строк — то, чем был <optgroup label>. Не option: сам по
// себе он не выбирается, и в listbox'е его быть пунктом не должно.
export function MenuNotice({ text }: { text: string }) {
  return (
    <div role="presentation" className="px-3 py-2 text-xs text-[var(--muted-foreground)]">
      {text}
    </div>
  );
}

export function MenuGroupLabel({ label }: { label: string }) {
  return (
    <div
      role="presentation"
      className="px-3 pb-1 pt-2 text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]"
    >
      {label}
    </div>
  );
}

// Обычный одиночный выбор — замена нативному <select> в тулбарах и формах.
//
// placeholder показывается приглушённым, пока значение пустое. Если пустое
// значение — это ОТВЕТ («все», «не указан»), передайте его отдельным option:
// тогда оно выбирается как обычная строка и в списке видно, что оно выбрано.
export function SelectMenu({
  options,
  value,
  onChange,
  placeholder,
  label,
  onBlur,
  notice,
  align,
  disabled,
  className,
  triggerClassName,
  panelClassName,
  matchWidth = true,
}: {
  options: readonly SelectMenuOption[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  label?: string;
  onBlur?: () => void;
  /** Пояснение над списком — например «справочник не прочитался». Не опция:
   *  выбирать в нём нечего, а второй строкой с пустым значением он ставил
   *  галочку сразу на двух пунктах. */
  notice?: string;
  align?: "left" | "right";
  disabled?: boolean;
  className?: string;
  triggerClassName?: string;
  panelClassName?: string;
  matchWidth?: boolean;
}) {
  const selected = options.find((o) => o.value === value);
  return (
    <MenuShell
      summary={selected?.label ?? placeholder ?? ""}
      placeholderShown={!selected}
      label={label}
      align={align}
      disabled={disabled}
      className={className}
      triggerClassName={triggerClassName}
      panelClassName={panelClassName}
      matchWidth={matchWidth}
      onBlur={onBlur}
    >
      {(close) => (
        <>
          {notice && <MenuNotice text={notice} />}
          {options.map((o, idx) => (
            <Fragment key={o.value}>
              {o.group && o.group !== options[idx - 1]?.group && <MenuGroupLabel label={o.group} />}
              <MenuRow
                label={o.label}
                checked={o.value === value}
                onClick={() => {
                  // Повторный выбор того же значения НЕ считается изменением:
                  // нативный <select> никогда не слал change на неизменное
                  // значение, а вызывающие вешают на onChange запись на сервер
                  // и сброс страницы — холостой клик стоил бы POST'а и прыжка
                  // на первую страницу.
                  if (o.value !== value) onChange(o.value);
                  close();
                }}
              />
            </Fragment>
          ))}
        </>
      )}
    </MenuShell>
  );
}
