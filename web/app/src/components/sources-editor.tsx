import { useState } from "react";
import { FolderSearch, Plus, X } from "lucide-react";
import type { Source, SourceKind } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { SelectMenu } from "@/components/ui/select-menu";
import { FolderPicker } from "@/components/folder-picker";

// Источники, из которых собирается справка о проекте.
//
// Раньше это была одна textarea, куда писали «вид значение» построчно — и
// «непонятный вид магия» человек узнавал только после сохранения. Строка с
// выпадашкой ошибиться в виде не даёт вовсе, а виды названы так, как их зовут
// вслух: GitHub, а не repo.
const KINDS: { value: SourceKind; label: string; placeholder: string; hint: string }[] = [
  {
    value: "repo",
    label: "GitHub-репозиторий",
    placeholder: "git@github.com:org/payments",
    hint: "Склонируем на один коммит и прочитаем README, состав и темы последних коммитов.",
  },
  {
    value: "path",
    label: "Локальный каталог",
    placeholder: "~/work/payments",
    hint: "Каталог на этой же машине — годится, когда репозиторий уже склонирован.",
  },
  {
    value: "url",
    label: "Сайт",
    placeholder: "https://pay.example.com",
    hint: "Одна страница: описание продукта обычно и есть его словарь.",
  },
  {
    value: "text",
    label: "Просто текст",
    placeholder: "Приём денег, подписки, вебхуки провайдеров",
    hint: "Когда объяснить проще словами, чем ссылкой.",
  },
];

const kindOf = (k: string) => KINDS.find((x) => x.value === k) ?? KINDS[3];

export function SourcesEditor({
  value,
  onChange,
}: {
  value: Source[];
  onChange: (next: Source[]) => void;
}) {
  // Какой из источников сейчас выбирают мышью. Индекс, а не булево: строк
  // может быть несколько, и открытое окно относится к одной из них.
  const [browsing, setBrowsing] = useState<number | null>(null);

  const patch = (i: number, next: Partial<Source>) =>
    onChange(value.map((s, idx) => (idx === i ? { ...s, ...next } : s)));

  return (
    <div className="space-y-2">
      {value.map((s, i) => (
        <div key={i} className="flex items-start gap-2">
          <SelectMenu
            label="Вид источника"
            value={s.kind}
            onChange={(v) => patch(i, { kind: v as SourceKind })}
            options={KINDS.map((k) => ({ value: k.value, label: k.label }))}
            triggerClassName="h-10 w-full max-w-none"
            className="w-52 shrink-0"
          />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <input
                value={s.value}
                onChange={(e) => patch(i, { value: e.target.value })}
                placeholder={kindOf(s.kind).placeholder}
                autoComplete="off"
                spellCheck={false}
                className="h-10 min-w-0 flex-1 rounded-xl border-0 bg-[var(--muted)] px-3 text-sm placeholder:text-[var(--muted-foreground)]/60 focus:outline-none focus:ring-2 focus:ring-primary/30"
              />
              {/* Путь набирают не по памяти, а узнают, увидев. Поле рядом
                  остаётся: обзор ходит по машине, где стоит steno, а панель
                  могли открыть с другого компьютера. */}
              {s.kind === "path" && (
                <Button
                  type="button"
                  variant="outline"
                  className="h-10 shrink-0 gap-2 px-3"
                  onClick={() => setBrowsing(i)}
                >
                  <FolderSearch className="h-4 w-4" />
                  Выбрать
                </Button>
              )}
            </div>
            <p className="mt-1 text-xs text-[var(--muted-foreground)]">{kindOf(s.kind).hint}</p>
          </div>
          <button
            type="button"
            aria-label="Убрать источник"
            onClick={() => onChange(value.filter((_, idx) => idx !== i))}
            className="mt-2 shrink-0 rounded-md p-1 text-[var(--muted-foreground)] transition-colors hover:text-[var(--destructive)]"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      ))}

      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => onChange([...value, { kind: "repo", value: "" }])}
      >
        <Plus className="h-4 w-4" />
        добавить источник
      </Button>

      <FolderPicker
        open={browsing !== null}
        onClose={() => setBrowsing(null)}
        startAt={browsing !== null ? value[browsing]?.value : ""}
        onPick={(p) => {
          if (browsing !== null) patch(browsing, { value: p });
          setBrowsing(null);
        }}
      />
    </div>
  );
}
