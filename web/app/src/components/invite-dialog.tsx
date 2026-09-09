import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, type InviteResult } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// Позвать бота на созвон, которого нет в расписании. Неожиданные созвоны —
// это как раз то, чего в календаре не было: собрались в две минуты, никто не
// завёл встречу, и без этой кнопки запись пришлось бы просить в Telegram.
export function InviteDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const qc = useQueryClient();

  useEffect(() => {
    if (open) {
      setUrl("");
      setTitle("");
    }
  }, [open]);

  const invite = useMutation({
    mutationFn: () => api.invite(url.trim(), title.trim()),
    onSuccess: (res: InviteResult) => {
      // Три исхода различаются словами: «уже иду» и «не хватило слотов» —
      // разные новости, и от второй человеку надо что-то сделать.
      if (res.status === "started") toast.success(res.message);
      else if (res.status === "duplicate") toast(res.message);
      else toast.warning(res.message);
      qc.invalidateQueries({ queryKey: ["meetings"] });
      onClose();
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : "не получилось"),
  });

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogHeader>
        <DialogTitle>Позвать бота</DialogTitle>
        <DialogDescription>
          Бот зайдёт в звонок и начнёт запись. Участники увидят его в списке под
          именем из настроек — незаметной записи здесь нет и не будет.
        </DialogDescription>
      </DialogHeader>

      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (url.trim()) invite.mutate();
        }}
      >
        <Input
          label="Ссылка на Google Meet"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://meet.google.com/abc-defg-hij"
          autoFocus
          autoComplete="off"
          spellCheck={false}
        />
        <Input
          label="Название (необязательно)"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Разбор инцидента"
          autoComplete="off"
        />
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button type="submit" variant="primary" isLoading={invite.isPending} disabled={!url.trim()}>
            Позвать
          </Button>
        </DialogFooter>
      </form>
    </Dialog>
  );
}
