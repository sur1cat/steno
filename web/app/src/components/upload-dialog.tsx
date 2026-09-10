import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { FileAudio, Upload, X } from "lucide-react";
import { ApiError, api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { t } from "@/lib/i18n";

// Загрузка записи созвона, на котором бота не было.
//
// Бот приходит не на всё: созвон мог пройти в Zoom, разговор — по телефону,
// встреча могла случиться до того, как steno поставили. Запись при этом обычно
// есть, и без этого окна она остаётся мёртвым файлом, хотя весь остальной
// конвейер к ней применим целиком.

const ACCEPT = "audio/*,video/*,.m4a,.mp3,.wav,.ogg,.opus,.mp4,.mov,.mkv,.webm";

function mb(bytes: number): string {
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} ${t("ГБ")}`;
  if (bytes >= 1 << 20) return `${Math.round(bytes / (1 << 20))} ${t("МБ")}`;
  return `${Math.max(1, Math.round(bytes / 1024))} ${t("КБ")}`;
}

/** Имя файла без расширения — то же начальное название даёт и сервер. */
function titleFrom(name: string): string {
  const dot = name.lastIndexOf(".");
  return (dot > 0 ? name.slice(0, dot) : name).trim();
}

export function UploadDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [title, setTitle] = useState("");
  const [over, setOver] = useState(false);
  const [sent, setSent] = useState(0);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const abort = useRef<AbortController | null>(null);
  const qc = useQueryClient();
  const nav = useNavigate();

  useEffect(() => {
    if (!open) return;
    setFile(null);
    setTitle("");
    setOver(false);
    setSent(0);
    setBusy(false);
    setFailed("");
  }, [open]);

  const take = (f: File | null | undefined) => {
    if (!f) return;
    setFile(f);
    setFailed("");
    // Название подставляется, но остаётся правимым: «rec_20260904_1130.m4a»
    // не название, а «Разговор с подрядчиком» — название.
    setTitle((prev) => (prev.trim() ? prev : titleFrom(f.name)));
  };

  const send = async () => {
    if (!file) return;
    setBusy(true);
    setFailed("");
    setSent(0);
    abort.current = new AbortController();
    try {
      const res = await api.upload(file, title, {
        signal: abort.current.signal,
        onProgress: (n) => setSent(n),
      });
      // 202, а не «готово»: сервер только принял файл. Дальше идут
      // перекодирование и расшифровка, и созвон уже виден в списке — со
      // статусом, который меняется сам.
      toast.success(`“${res.title}” ${t("принят, расшифровка пошла")}`);
      qc.invalidateQueries({ queryKey: ["meetings"] });
      onClose();
      nav("/");
    } catch (e) {
      // Отмену узнаём по статусу, а не по тексту: текст переводится, и
      // сравнение с русской строкой перестало бы срабатывать на английском.
      if (e instanceof ApiError && e.status === 0) setFailed("");
      else setFailed(e instanceof Error ? e.message : t("не получилось"));
    } finally {
      setBusy(false);
      abort.current = null;
    }
  };

  const percent = file && file.size > 0 ? Math.min(100, Math.round((sent / file.size) * 100)) : 0;

  return (
    <Dialog
      open={open}
      onClose={busy ? () => {} : onClose}
      className="max-w-xl"
    >
      <DialogHeader>
        <DialogTitle>{t("Загрузить запись")}</DialogTitle>
        <DialogDescription>
          {t("Созвон прошёл в Zoom, разговор был по телефону, встреча случилась до того, как\n          поставили steno, — а запись осталась. Расшифровка, follow-up и разметка по проектам\n          отработают на ней как обычно. Имён говорящих не будет: они приходят из субтитров Meet,\n          а в чужом файле их нет.")}
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-4">
        {!file ? (
          <button
            type="button"
            onClick={() => picker.current?.click()}
            onDragOver={(e) => {
              e.preventDefault();
              setOver(true);
            }}
            onDragLeave={() => setOver(false)}
            onDrop={(e) => {
              e.preventDefault();
              setOver(false);
              take(e.dataTransfer.files?.[0]);
            }}
            className={cn(
              "flex w-full flex-col items-center gap-2 rounded-2xl border-2 border-dashed px-6 py-10 text-center transition-colors",
              over
                ? "border-primary bg-primary/5"
                : "border-[var(--border)] hover:border-primary/60 hover:bg-[var(--muted)]/40",
            )}
          >
            <Upload className="h-6 w-6 text-[var(--muted-foreground)]" />
            <span className="text-sm">
              {t("Перетащи файл сюда или")} <span className="text-primary">{t("выбери на диске")}</span>
            </span>
            <span className="text-[13px] text-[var(--muted-foreground)]">
              {t("Звук или видео — mp4, m4a, mp3, wav. Картинку выбросим, останется звук.")}
            </span>
          </button>
        ) : (
          <div className="flex items-center gap-3 rounded-2xl border border-[var(--border)] bg-[var(--muted)]/40 px-4 py-3">
            <FileAudio className="h-5 w-5 shrink-0 text-[var(--muted-foreground)]" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm">{file.name}</div>
              <div className="text-[13px] text-[var(--muted-foreground)]">{mb(file.size)}</div>
            </div>
            {!busy && (
              <button
                type="button"
                onClick={() => setFile(null)}
                aria-label={t("Убрать файл")}
                className="shrink-0 rounded-lg p-1 text-[var(--muted-foreground)] transition-colors hover:bg-[var(--muted)] hover:text-[var(--foreground)]"
              >
                <X className="h-4 w-4" />
              </button>
            )}
          </div>
        )}

        <input
          ref={picker}
          type="file"
          accept={ACCEPT}
          className="hidden"
          onChange={(e) => {
            take(e.target.files?.[0]);
            e.target.value = "";
          }}
        />

        <Input
          label={t("Название")}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={t("Разговор с подрядчиком")}
          disabled={busy}
          autoComplete="off"
        />

        {/* Файл может быть на гигабайт: без полосы это выглядит зависшей
            вкладкой, и человек жмёт «Загрузить» второй раз. */}
        {busy && (
          <div>
            <div className="mb-1.5 flex items-baseline justify-between text-[13px] text-[var(--muted-foreground)]">
              <span>
                {percent < 100
                  ? t("Отправляю файл…")
                  : t("Файл ушёл, сервер принимает — расшифровка начнётся сама")}
              </span>
              <span className="tabular-nums">
                {file ? `${mb(sent)} ${t("из")} ${mb(file.size)}` : ""}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-[var(--muted)]">
              <div
                className={cn(
                  "h-full rounded-full bg-primary transition-[width] duration-200",
                  percent >= 100 && "animate-pulse",
                )}
                style={{ width: `${Math.max(2, percent)}%` }}
              />
            </div>
          </div>
        )}

        {/* Отказ в режиме субтитров объясняет причину и говорит, что поменять.
            Тост на это не годится: он уезжает раньше, чем дочитан. */}
        {failed && (
          <div className="rounded-2xl border border-[var(--destructive)]/30 bg-[var(--destructive)]/5 px-4 py-3 text-sm leading-relaxed text-[var(--destructive)]">
            {failed}
          </div>
        )}
      </div>

      <DialogFooter>
        {busy ? (
          <Button type="button" variant="outline" onClick={() => abort.current?.abort()}>
            {t("Прервать")}
          </Button>
        ) : (
          <Button type="button" variant="outline" onClick={onClose}>
            {t("Отмена")}
          </Button>
        )}
        <Button
          type="button"
          variant="primary"
          onClick={send}
          isLoading={busy}
          disabled={!file || busy}
        >
          {t("Загрузить")}
        </Button>
      </DialogFooter>
    </Dialog>
  );
}
