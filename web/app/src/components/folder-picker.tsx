import { useEffect, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ArrowUp, Folder, FolderGit2 } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// Выбор папки мышью вместо вписывания пути руками.
//
// Путь к каталогу — это то, что человек не помнит и не должен помнить: он
// узнаёт свою папку, увидев её, а вписать её без единой опечатки не может.
// Поэтому обзор, а не поле.
//
// Файлов здесь нет вовсе — их не присылает сервер: выбирают каталог, и
// показывать рядом с ним тысячу файлов значит прятать цель за шумом.

export function FolderPicker({
  open,
  onClose,
  onPick,
  startAt,
}: {
  open: boolean;
  onClose: () => void;
  onPick: (path: string) => void;
  /** Уже выбранный путь: открываемся там, а не в домашнем каталоге. */
  startAt?: string;
}) {
  const [path, setPath] = useState("");

  useEffect(() => {
    if (open) setPath(startAt?.trim() ?? "");
  }, [open, startAt]);

  const q = useQuery({
    queryKey: ["browse", path],
    queryFn: () => api.browse(path),
    enabled: open,
    // Прошлый список остаётся на экране, пока грузится следующий: без этого
    // каждый шаг вглубь моргает пустотой, и попасть по строке на третьем
    // уровне становится делом удачи.
    placeholderData: keepPreviousData,
    retry: false,
  });

  // Путь, которого нет (вписали руками и ошиблись), не должен запирать окно:
  // сервер объяснит, что не так, а вернуться можно в домашний каталог.
  const failed = q.isError ? (q.error instanceof Error ? q.error.message : "не открылось") : "";
  const here = q.data?.path ?? path;
  const dirs = q.data?.dirs ?? [];

  return (
    <Dialog open={open} onClose={onClose} className="max-w-lg">
      <DialogHeader>
        <DialogTitle>Выберите папку</DialogTitle>
        <DialogDescription>
          Это папки на том компьютере, где работает steno. Если панель открыта с другого —
          здесь будет чужой диск, и путь придётся вписать руками.
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-3">
        <div className="flex items-center gap-2">
          {/* Пустой parent — это домашний каталог, выше не пускают. Стрелку в
              этом случае не рисуем вовсе: кнопка, которая никуда не ведёт,
              хуже её отсутствия. */}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-9 w-9 shrink-0 p-0"
            aria-label="На папку выше"
            disabled={!q.data?.parent}
            onClick={() => setPath(q.data?.parent ?? "")}
          >
            <ArrowUp className="h-4 w-4" />
          </Button>
          <div className="min-w-0 flex-1 truncate rounded-xl bg-[var(--muted)] px-3 py-2 text-sm">
            {here || "домашний каталог"}
          </div>
        </div>

        <div className="h-64 overflow-y-auto rounded-xl border border-[var(--border)]">
          {failed ? (
            <div className="px-4 py-6 text-center text-sm leading-relaxed text-[var(--destructive)]">
              {failed}
              <div className="mt-3">
                <Button type="button" variant="outline" size="sm" onClick={() => setPath("")}>
                  В домашний каталог
                </Button>
              </div>
            </div>
          ) : q.isPending ? (
            <div className="px-4 py-6 text-center text-sm text-[var(--muted-foreground)]">
              Смотрю…
            </div>
          ) : dirs.length === 0 ? (
            <div className="px-4 py-6 text-center text-sm leading-relaxed text-[var(--muted-foreground)]">
              Внутри нет вложенных папок.
              <br />
              Если нужна эта — выбирай её кнопкой ниже.
            </div>
          ) : (
            <div className="divide-y divide-[var(--border)]">
              {dirs.map((d) => (
                <button
                  key={d.path}
                  type="button"
                  onClick={() => setPath(d.path)}
                  // Двойной клик по репозиторию — это «беру его», а не «зайди
                  // внутрь»: внутри лежит исходный код, и заходить туда незачем.
                  onDoubleClick={() => d.isRepo && onPick(d.path)}
                  className="flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm transition-colors hover:bg-[var(--muted)]/60"
                >
                  {/* Репозиторий видно сразу: человек ищет глазами именно его,
                      и дать увидеть цель дешевле, чем дать зайти и вернуться. */}
                  {d.isRepo ? (
                    <FolderGit2 className="h-4 w-4 shrink-0 text-primary" />
                  ) : (
                    <Folder className="h-4 w-4 shrink-0 text-[var(--muted-foreground)]" />
                  )}
                  <span className={cn("min-w-0 flex-1 truncate", d.isRepo && "text-primary")}>
                    {d.name}
                  </span>
                  {d.isRepo && (
                    <span className="shrink-0 text-[11px] uppercase tracking-wide text-primary/70">
                      репозиторий
                    </span>
                  )}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Отмена
        </Button>
        <Button
          type="button"
          variant="primary"
          disabled={!here || Boolean(failed)}
          onClick={() => onPick(here)}
        >
          Выбрать эту папку
        </Button>
      </DialogFooter>
    </Dialog>
  );
}
