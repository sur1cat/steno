import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, type Channel } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ChipsInput, submittedFromChips } from "@/components/ui/chips-input";
import { GoogleConnect } from "@/components/google-connect";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { t } from "@/lib/i18n";

// Форма канала рисуется по описанию полей, которое пришло с сервера, а не по
// своему списку: иначе новое поле пришлось бы заводить в двух местах, и рано
// или поздно они разошлись бы.
//
// Имён переменных окружения здесь нет и не будет. Токены задаёт разработчик в
// `steno setup`; человеку, который открыл панель посмотреть, куда уходит
// follow-up, слово TELEGRAM_BOT_TOKEN не говорит ничего, кроме того, что он
// попал не туда.

/** Строка через запятую с сервера — в список значений и обратно. */
function splitList(raw: string | undefined): string[] {
  return (raw ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function ChannelDialog({
  open,
  onClose,
  channel,
}: {
  open: boolean;
  onClose: () => void;
  channel: Channel | null;
}) {
  const qc = useQueryClient();
  const [enabled, setEnabled] = useState(false);
  const [values, setValues] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open || !channel) return;
    setEnabled(channel.enabled);
    setValues({ ...channel.values });
  }, [open, channel]);

  const save = useMutation({
    mutationFn: () => api.saveChannel(channel!.key, enabled, values),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings"] });
      toast.success(
        channel!.live
          ? t("Сохранено — подхватится на следующей рассылке")
          : t("Сохранено — применится при следующем запуске сервиса"),
      );
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : t("не получилось")),
  });

  if (!channel) return null;
  const set = (k: string, v: string) => setValues((prev) => ({ ...prev, [k]: v }));

  return (
    <Dialog open={open} onClose={onClose} className="max-w-xl">
      <DialogHeader>
        <DialogTitle>{channel.name}</DialogTitle>
        <DialogDescription>{channel.about}</DialogDescription>
      </DialogHeader>

      <form
        className="space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          // Enter в наборе значений уже добавил значение — сохранять по нему
          // рано: модалка закрылась бы посреди набора списка.
          if (submittedFromChips()) return;
          save.mutate();
        }}
      >
        <div className="flex items-center justify-between rounded-xl bg-[var(--muted)]/60 px-4 py-3">
          <div className="text-sm">
            {t("Канал включён")}
            <div className="text-xs text-[var(--muted-foreground)]">
              {channel.in && channel.out
                ? t("приносит созвоны и уносит follow-up")
                : channel.in
                  ? t("приносит созвоны")
                  : t("уносит follow-up")}
            </div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} />
        </div>

        {channel.fields.map((f) => (
          <div key={f.key}>
            {/* «google» — не поле, а кнопка: значения оно не хранит, и в
                values его ключа нет. Согласие одно на все каналы Google,
                поэтому состояние компонент спрашивает сам, а не берёт из
                канала. */}
            {f.kind === "google" ? (
              <>
                <div className="mb-1.5 text-sm font-light text-[var(--muted-foreground)]">
                  {f.label}
                </div>
                <GoogleConnect hint={f.hint} />
              </>
            ) : f.kind === "switch" ? (
              <div className="flex items-start justify-between gap-4">
                <div className="text-sm">
                  {f.label}
                  {f.hint && (
                    <div className="text-xs text-[var(--muted-foreground)]">{f.hint}</div>
                  )}
                </div>
                <Switch
                  checked={values[f.key] === "1"}
                  onCheckedChange={(v) => set(f.key, v ? "1" : "")}
                />
              </div>
            ) : (
              <>
                <label
                  htmlFor={`ch-${f.key}`}
                  className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]"
                >
                  {f.label}
                </label>
                {/* Список набирается по одному значению, а не строкой через
                    запятую: см. components/ui/chips-input.tsx. Наружу уходит
                    та же строка — проволочный формат менять незачем. */}
                {f.kind === "list" ? (
                  <ChipsInput
                    id={`ch-${f.key}`}
                    value={splitList(values[f.key])}
                    onChange={(next) => set(f.key, next.join(", "))}
                    placeholder={f.placeholder}
                    addLabel={`${t("Добавить:")} ${f.label.toLowerCase()}`}
                  />
                ) : (
                  <input
                    id={`ch-${f.key}`}
                    value={values[f.key] ?? ""}
                    onChange={(e) => set(f.key, e.target.value)}
                    placeholder={f.placeholder}
                    inputMode={f.kind === "number" ? "numeric" : undefined}
                    autoComplete="off"
                    spellCheck={false}
                    className="h-10 w-full rounded-xl border-0 bg-[var(--muted)] px-3 text-sm placeholder:text-[var(--muted-foreground)]/60 focus:outline-none focus:ring-2 focus:ring-primary/30"
                  />
                )}
                {f.hint && (
                  <p className="mt-1 text-xs leading-relaxed text-[var(--muted-foreground)]">
                    {f.hint}
                  </p>
                )}
              </>
            )}
          </div>
        ))}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t("Отмена")}
          </Button>
          <Button type="submit" variant="primary" isLoading={save.isPending}>
            {t("Сохранить")}
          </Button>
        </DialogFooter>
      </form>
    </Dialog>
  );
}
