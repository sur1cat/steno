import { useRef, useState, type ClipboardEvent, type KeyboardEvent } from "react";
import { Plus, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

// Список значений, который набирают руками: чей календарь смотреть, из какого
// чата принимать ссылки, как проект называют вслух.
//
// Раньше это было обычное поле, куда всё писали через запятую. Ошибиться там
// было нечем помочь: лишний пробел, точка с запятой вместо запятой, забытая
// кавычка — и значение молча уезжало на сервер неправильным, а увидеть, что
// именно там лежит, можно было только вчитавшись в строку. Значение, ставшее
// чипом, видно поштучно, и убирается оно крестиком, а не вычёсыванием текста
// между двумя запятыми.
//
// Проволочный формат при этом не меняется: наружу уходит та же строка через
// запятую, склейка и разбор живут у вызывающего.

/**
 * Метка на обёртке поля и проверка для формы вокруг него.
 *
 * Enter в текстовом поле внутри формы — это «отправить форму», и одного
 * preventDefault на keydown хватать не обязано: браузер отправляет форму как
 * действие по умолчанию, но синтетические события (а в этой панели ими же
 * снимаются проверочные снимки) доходят до отправки и с погашенным default.
 * Цена ошибки несимметрична: не сработавший Enter — это «значение не
 * добавилось», а сработавшая отправка — это закрытая модалка и потерянное
 * недописанное значение. Поэтому форма ещё и спрашивает, откуда пришёл Enter.
 */
const CHIPS_MARKER = "data-chips-input";

/** Пришла ли отправка формы из набора значений — тогда сохранять рано. */
export function submittedFromChips(): boolean {
  const el = document.activeElement;
  return el instanceof Element && el.closest(`[${CHIPS_MARKER}]`) !== null;
}

interface ChipsInputProps {
  value: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  /** Подпись кнопки для тех, кто читает экран: у неё только плюс. */
  addLabel?: string;
  id?: string;
  disabled?: boolean;
}

/** Разбор того, что человек ввёл или вставил: запятая, точка с запятой, перенос. */
function parts(raw: string): string[] {
  return raw
    .split(/[,;\n]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function ChipsInput({
  value,
  onChange,
  placeholder,
  addLabel = t("Добавить"),
  id,
  disabled,
}: ChipsInputProps) {
  const [draft, setDraft] = useState("");
  const input = useRef<HTMLInputElement>(null);

  // Повторы молча не добавляем: два одинаковых чипа ничего не значат, а
  // выглядят как недосмотр интерфейса.
  const add = (raw: string) => {
    const next = [...value];
    for (const p of parts(raw)) if (!next.includes(p)) next.push(p);
    if (next.length !== value.length) onChange(next);
    setDraft("");
  };

  const commit = () => {
    if (draft.trim()) add(draft);
    input.current?.focus();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      // Enter внутри формы иначе отправляет её целиком, не дописав значение.
      e.preventDefault();
      commit();
      return;
    }
    if (e.key === "Backspace" && draft === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  // Вставленный из почты или таблицы список сам разложится по чипам, а не ляжет
  // одной строкой, которую потом разбирать руками.
  const onPaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const text = e.clipboardData.getData("text");
    if (!/[,;\n]/.test(text)) return;
    e.preventDefault();
    add(draft + text);
  };

  return (
    <div
      // Метка для формы вокруг: Enter здесь добавляет значение, а не сохраняет
      // форму. Одного preventDefault на keydown мало — см. CHIPS_MARKER ниже.
      {...{ [CHIPS_MARKER]: "" }}
      onClick={() => input.current?.focus()}
      className={cn(
        "flex min-h-10 w-full flex-wrap items-center gap-1.5 rounded-xl border-0 bg-[var(--muted)] px-2 py-1.5 text-sm focus-within:outline-none focus-within:ring-2 focus-within:ring-primary/30",
        disabled && "pointer-events-none opacity-60",
      )}
    >
      {value.map((v) => (
        <span
          key={v}
          className="flex max-w-full items-center gap-1 rounded-lg bg-[var(--card)] py-0.5 pl-2.5 pr-1 text-[13px]"
        >
          <span className="truncate">{v}</span>
          <button
            type="button"
            tabIndex={-1}
            aria-label={`${t("Убрать")} ${v}`}
            onMouseDown={(e) => {
              e.preventDefault();
              onChange(value.filter((x) => x !== v));
            }}
            className="flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--muted-foreground)] transition-colors hover:text-[var(--destructive)] pointer-coarse:h-6 pointer-coarse:w-6"
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      ))}

      <input
        ref={input}
        id={id}
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        // Уход из поля с недописанным значением — самая частая потеря: человек
        // набрал и нажал «Сохранить», минуя Enter.
        onBlur={() => draft.trim() && add(draft)}
        // С чипами подсказка меняется на приглашение: пример значения рядом с
        // настоящими значениями читается как ещё одно, уже добавленное. Пустой
        // строки здесь тоже мало — набранные чипы переносят поле на вторую
        // строку, и без текста она выглядит просто пустой полосой с плюсом.
        placeholder={value.length === 0 ? placeholder : t("добавить ещё")}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
        className="h-6 min-w-[8rem] flex-1 bg-transparent px-1 text-sm placeholder:text-[var(--muted-foreground)]/60 focus:outline-none"
      />

      <button
        type="button"
        onClick={commit}
        disabled={disabled || !draft.trim()}
        aria-label={addLabel}
        title={addLabel}
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-[var(--muted-foreground)] transition-colors hover:bg-[var(--card)] hover:text-[var(--foreground)] disabled:opacity-35 disabled:hover:bg-transparent"
      >
        <Plus className="h-4 w-4" />
      </button>
    </div>
  );
}
