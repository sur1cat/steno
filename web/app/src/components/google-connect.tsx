import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Check } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

// Доступ в Google. Человек нажимает кнопку, соглашается у Google и
// возвращается — вместо того, чтобы заводить служебный ключ и вписывать путь к
// файлу, чего он всё равно сделать не может.
//
// Согласие одно на все три канала: календарь, почта бота и документы ходят под
// ним же. Поле стоит в каждом из трёх, а состояние спрашивается одно — поэтому
// и ключ запроса общий: подключился из календаря — почта увидит это сразу, без
// перезагрузки страницы.

export const GOOGLE_STATUS_KEY = ["google-status"];

export function GoogleConnect({ hint }: { hint?: string }) {
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const q = useQuery({ queryKey: GOOGLE_STATUS_KEY, queryFn: api.googleStatus });

  const connect = useMutation({
    mutationFn: () => api.googleConnect(),
    // Уводим эту же вкладку: Google вернёт человека на адрес панели, и
    // вернуться он должен туда, откуда ушёл.
    onSuccess: (r) => {
      window.location.href = r.url;
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : "не получилось"),
  });

  const disconnect = useMutation({
    mutationFn: () => api.googleDisconnect(),
    onSuccess: () => {
      setConfirming(false);
      qc.invalidateQueries({ queryKey: GOOGLE_STATUS_KEY });
      qc.invalidateQueries({ queryKey: ["settings"] });
      toast.success("Доступ в Google отключён");
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : "не получилось"),
  });

  if (q.isPending) {
    return <div className="text-sm text-[var(--muted-foreground)]">Проверяю доступ…</div>;
  }
  if (q.isError) {
    return (
      <div className="text-sm text-[var(--destructive)]">
        {q.error instanceof Error ? q.error.message : "не удалось узнать про доступ"}
      </div>
    );
  }

  const { ready, connected, account, why } = q.data;

  // `why` — готовая фраза сервера, и по ней самой не видно, тревога это или
  // наоборот. «Доступ выдан не весь… подключи ещё раз» и «доступ уже выдан
  // ключом организации — подключать ничего не нужно» приходят одним и тем же
  // полем, и красить их по словам внутри значило бы гадать: одна опечатка на
  // стороне сервера — и спокойное состояние выглядит поломкой.
  //
  // Поэтому текст показывается ровным, а тревогу выражает действие: доступ
  // получен, но неполный, и переспросить согласие можно кнопкой — тогда рядом
  // появляется «Подключить ещё раз». Там, где нажимать нечего (доступ пришёл
  // ключом организации), не появляется ничего лишнего.
  //
  // ВРЕМЕННО, И ВОТ ПОЧЕМУ. Строка ниже опирается на невысказанный уговор:
  // что «доступ выдан не весь» приходит с ready=true (кнопка есть, согласие
  // можно переспросить), а «выдан ключом организации» — с ready=false
  // (нажимать нечего). На сегодняшних пяти состояниях это так, но в ответе
  // сервера этого не написано, и шестое состояние сломает раскладку молча.
  // Бэкенд заводит для этого severity: "info" | "warn" — как приедет, сюда
  // приходит `why_severity === "warn"`, а этот вывод из ready удаляется.
  const needsRedo = connected && ready && why !== "";

  return (
    <>
      <div className="rounded-xl border border-[var(--border)] px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          {/* Своей строки состояния при ready=false и без доступа нет: там
              нечего сообщать сверх того, что уже сказал сервер, а «подключить
              нечем» — выдумка панели поверх его слов. */}
          {(connected || ready) && (
            <div className="flex min-w-0 items-center gap-2 text-sm">
              {connected && !needsRedo && (
                <Check className="h-4 w-4 shrink-0 text-primary" />
              )}
              <span className="min-w-0">
                {connected ? (
                  <>
                    Подключён
                    {account && (
                      <span className="text-[var(--muted-foreground)]"> · {account}</span>
                    )}
                  </>
                ) : (
                  "Не подключён"
                )}
              </span>
            </div>
          )}

          {/* ready=false — кнопки нет вовсе: нажимать её было бы некуда, а
              объяснение придёт в `why` строкой ниже. */}
          {ready && (
            <div className="flex flex-wrap items-center gap-2">
              {(!connected || needsRedo) && (
                <Button
                  type="button"
                  variant="primary"
                  size="sm"
                  isLoading={connect.isPending}
                  onClick={() => connect.mutate()}
                >
                  {needsRedo ? "Подключить ещё раз" : "Подключить Google"}
                </Button>
              )}
              {connected && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setConfirming(true)}
                >
                  Отключить
                </Button>
              )}
            </div>
          )}
        </div>

        {/* Текст сервера показываем как есть: он уже написан для человека, и
            дописывать к нему нечего — в том числе цветом. */}
        {why && (
          <p className="mt-2 text-xs leading-relaxed text-[var(--muted-foreground)]">{why}</p>
        )}
        {!why && hint && (
          <p className="mt-2 text-xs leading-relaxed text-[var(--muted-foreground)]">{hint}</p>
        )}
      </div>

      {/* Отключение снимает доступ у всех трёх каналов разом, а нажимают его в
          одном. Сказать об этом надо до, а не после. */}
      <ConfirmDialog
        open={confirming}
        onClose={() => setConfirming(false)}
        onConfirm={() => disconnect.mutate()}
        title="Отключить доступ в Google?"
        description="Доступ один на всё: без него перестанут работать и календарь, и почта бота, и документы. Подключить обратно можно этой же кнопкой."
        confirmLabel="Отключить"
        isPending={disconnect.isPending}
      />
    </>
  );
}
