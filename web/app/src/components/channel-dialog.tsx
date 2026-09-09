import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, type Channel } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// Форма канала рисуется по описанию полей, которое пришло с сервера, а не по
// своему списку: иначе новое поле пришлось бы заводить в двух местах, и рано
// или поздно они разошлись бы.
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
          ? "Сохранено — подхватится на следующей рассылке"
          : "Сохранено — применится при следующем запуске сервиса",
      );
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : "не получилось"),
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
          save.mutate();
        }}
      >
        <div className="flex items-center justify-between rounded-xl bg-[var(--muted)]/60 px-4 py-3">
          <div className="text-sm">
            Канал включён
            <div className="text-xs text-[var(--muted-foreground)]">
              {channel.in && channel.out
                ? "приносит созвоны и уносит follow-up"
                : channel.in
                  ? "приносит созвоны"
                  : "уносит follow-up"}
            </div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} />
        </div>

        {channel.fields.map((f) => (
          <div key={f.key}>
            {f.kind === "switch" ? (
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
                <label className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]">
                  {f.label}
                </label>
                <input
                  value={values[f.key] ?? ""}
                  onChange={(e) => set(f.key, e.target.value)}
                  placeholder={f.placeholder}
                  inputMode={f.kind === "number" ? "numeric" : undefined}
                  autoComplete="off"
                  spellCheck={false}
                  className="h-10 w-full rounded-xl border-0 bg-[var(--muted)] px-3 text-sm placeholder:text-[var(--muted-foreground)]/60 focus:outline-none focus:ring-2 focus:ring-primary/30"
                />
                {f.hint && (
                  <p className="mt-1 text-xs leading-relaxed text-[var(--muted-foreground)]">
                    {f.hint}
                  </p>
                )}
              </>
            )}
          </div>
        ))}

        {channel.secrets && channel.secrets.length > 0 && (
          <div className="rounded-xl border border-[var(--border)] p-4">
            <div className="text-sm">Что нужно в окружении</div>
            <p className="mb-3 text-xs text-[var(--muted-foreground)]">
              Токены задаются переменными окружения и в базу не попадают: класть их туда, где
              лежат расшифровки всех разговоров, не стоит.
            </p>
            <div className="space-y-2">
              {channel.secrets.map((s) => (
                <div key={s.env} className="flex items-center justify-between gap-3 text-sm">
                  <span>
                    {s.what} <code className="text-xs text-[var(--muted-foreground)]">{s.env}</code>
                  </span>
                  <Badge variant={s.set ? "success" : "warning"}>{s.set ? "задан" : "пусто"}</Badge>
                </div>
              ))}
            </div>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button type="submit" variant="primary" isLoading={save.isPending}>
            Сохранить
          </Button>
        </DialogFooter>
      </form>
    </Dialog>
  );
}
