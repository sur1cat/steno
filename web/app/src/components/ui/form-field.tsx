"use client";

import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown } from "lucide-react";

import { cn } from "@/lib/utils";
import { InfoHint } from "@/components/ui/info-hint";
import { useEscapeHandler } from "@/components/ui/escape-stack";

// Подпись поля с необязательной подсказкой в кружке. Вынесена отдельно, потому
// что подпись обязана занимать одну строку при ЛЮБОЙ длине пояснения: поля
// стоят в сетке, и абзац под одним из них тянет вниз весь ряд — на форме
// операции с балансом это уже разъезжалось.
function FieldLabel({ label, hint, required }: { label: string; hint?: string; required?: boolean }) {
  return (
    <div className="mb-1.5 flex h-5 items-center gap-1">
      <span className="text-sm font-medium">
        {label}
        {required && <span className="ml-0.5 text-red-500">*</span>}
      </span>
      {hint && <InfoHint title={label} body={hint} />}
    </div>
  );
}

// Общая геометрия контролов формы: та же высота и радиус у select и input,
// иначе соседние поля в ряду стоят на разной высоте.
const CONTROL =
  "h-10 w-full rounded-lg border border-[var(--input)] bg-[var(--background)] px-3 text-sm text-[var(--foreground)] transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--ring)] disabled:cursor-not-allowed disabled:opacity-50";

export interface SelectOption {
  value: string;
  label: string;
}

interface SelectFieldProps {
  label: string;
  hint?: string;
  required?: boolean;
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  // placeholder — подпись пустого выбора («Выберите тип»). Пустая строка
  // остаётся валидным значением: у части полей «не указан» — это ответ, а не
  // отсутствие ответа.
  placeholder?: string;
  className?: string;
  disabled?: boolean;
}

// Выпадашка формы — кнопка со значением и список под ней; вид взят у выбора
// парков в осмотрах ТС (components/ops-park-selector.tsx): галочка на
// выбранном, ховер строкой, геометрия приложения.
//
// Нативный select тут не годится: РАСКРЫТЫЙ список рисует браузер своим
// оформлением — в тёмной теме он выпадает светлым прямоугольником поверх
// карточки, а шрифт и высота строки не совпадают ни с чем вокруг. До его
// содержимого странице не дотянуться никаким классом.
//
// В отличие от селектора парков список НЕ портируется в body. Портал там нужен,
// потому что тулбар живёт внутри overflow-контейнера, который обрезал бы меню;
// здесь поле стоит в карточке, у которой обрезающих родителей нет. Портал же
// тянет за собой position:fixed и ручной пересчёт координат — а тот зависит от
// UI-масштаба приложения (CSS zoom на <html>), и при включённом масштабе список
// уезжал от своего поля вправо и вниз. Обычный absolute внутри relative-обёртки
// позиционирует браузер: left-0 right-0 даёт ровно ширину поля при любом
// масштабе, и разъехаться там нечему.
export function SelectField({
  label,
  hint,
  required,
  value,
  onChange,
  options,
  placeholder,
  className,
  disabled,
}: SelectFieldProps) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const selected = options.find((o) => o.value === value);

  // Escape — через общий стек, чтобы выпадашка, открытая поверх диалога, забрала
  // нажатие себе и не закрыла диалог под собой.
  useEscapeHandler(open, () => setOpen(false));

  useEffect(() => {
    if (!open) return;
    // pointerdown, не mousedown: iOS не синтезирует mouse-событие для тапа по
    // неинтерактивному элементу, и меню не закрывалось бы тапом мимо.
    const onPointerDown = (e: PointerEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  const pick = (next: string) => {
    onChange(next);
    setOpen(false);
  };

  return (
    <div className={className}>
      <FieldLabel label={label} hint={hint} required={required} />
      <div ref={wrapRef} className="relative">
        <button
          type="button"
          disabled={disabled}
          onClick={() => setOpen((v) => !v)}
          aria-haspopup="listbox"
          aria-expanded={open}
          className={cn(CONTROL, "flex items-center justify-between gap-2 text-left")}
        >
          <span className={cn("truncate", !selected && "text-[var(--muted-foreground)]")}>
            {selected?.label ?? placeholder ?? ""}
          </span>
          <ChevronDown
            className={cn(
              "h-4 w-4 shrink-0 text-[var(--muted-foreground)] transition-transform",
              open && "rotate-180",
            )}
          />
        </button>
        {open && (
          <div
            role="listbox"
            className="absolute left-0 right-0 top-full z-50 mt-1 max-h-72 overflow-y-auto rounded-lg border border-[var(--border)] bg-[var(--popover)] py-1 shadow-lg"
          >
            {placeholder !== undefined && (
              <OptionRow label={placeholder} muted checked={!selected} onClick={() => pick("")} />
            )}
            {options.map((o) => (
              <OptionRow
                key={o.value}
                label={o.label}
                checked={o.value === value}
                onClick={() => pick(o.value)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// Строка списка. Галочка занимает место всегда (opacity, а не условный
// рендер), иначе выбранная строка сдвигалась бы относительно остальных.
function OptionRow({
  label,
  checked,
  muted,
  onClick,
}: {
  label: string;
  checked: boolean;
  muted?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={checked}
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-[var(--muted)]",
        muted && "text-[var(--muted-foreground)]",
      )}
    >
      <Check className={cn("h-4 w-4 shrink-0 text-primary", !checked && "opacity-0")} />
      <span className="truncate">{label}</span>
    </button>
  );
}

interface InputFieldProps {
  label: string;
  hint?: string;
  required?: boolean;
  type?: "text" | "number" | "date";
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  step?: string;
  min?: string;
  max?: string;
  className?: string;
  // suffix — единица измерения справа в поле (₸). Не placeholder: тот исчезает,
  // как только оператор начал печатать, а валюту надо видеть всегда.
  suffix?: string;
}

export function InputField({
  label,
  hint,
  required,
  type = "text",
  value,
  onChange,
  placeholder,
  step,
  min,
  max,
  className,
  suffix,
}: InputFieldProps) {
  return (
    <div className={className}>
      <FieldLabel label={label} hint={hint} required={required} />
      <div className="relative">
        <input
          type={type}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          step={step}
          min={min}
          max={max}
          className={cn(CONTROL, suffix && "pr-9")}
        />
        {suffix && (
          <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-[var(--muted-foreground)]">
            {suffix}
          </span>
        )}
      </div>
    </div>
  );
}

interface SegmentedProps<T extends string> {
  value: T;
  onChange: (value: T) => void;
  options: { value: T; label: string }[];
  className?: string;
}

// Переключатель режима на две-три позиции. Ровно та же пилюля, что уже стоит
// над историей операций и в пресетах периода, — только крупнее, потому что
// здесь это не фильтр списка, а выбор того, ЧЬИ деньги двигаются.
//
// Пришёл на смену паре нативных радиокнопок: те рисуются системным виджетом,
// в тёмной теме выглядят инородно и не показывают, что варианты
// взаимоисключающие, — два кружка легко прочитать как два независимых флажка.
export function Segmented<T extends string>({ value, onChange, options, className }: SegmentedProps<T>) {
  return (
    <div
      role="tablist"
      className={cn(
        "inline-flex gap-1 rounded-lg border border-[var(--border)] bg-[var(--muted)] p-1",
        className,
      )}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="tab"
          aria-selected={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
            value === o.value
              ? "bg-[var(--background)] text-[var(--foreground)] shadow-sm"
              : "text-[var(--muted-foreground)] hover:text-[var(--foreground)]",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
