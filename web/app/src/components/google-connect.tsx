import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Check, TriangleAlert } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
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

  const { ready, connected, account, why, severity } = q.data;

  // Цвет и кнопки — две независимые вещи, и выводить одно из другого нельзя.
  //
  // Цвет решает severity, и только он. Ни `ready`, ни `connected`, ни тем
  // более слова внутри `why` для этого не годятся: ключ организации уживается
  // с заведённым входом по кнопке, а подключённый аккаунт переживает смену
  // client secret и остаётся connected при ready:false. Обе «очевидные»
  // зависимости бэкенд проверил и опроверг конкретными сценариями.
  //
  // Кнопки решает то, чем человек в этом состоянии может распорядиться:
  // согласие спрашивается входом по кнопке, поэтому «Подключить» живёт при
  // ready; а снять уже лежащий токен можно и без него, поэтому «Отключить»
  // живёт при connected. Из этого следует состояние без единой кнопки —
  // тревога есть, нажать нечего (вход не настроен), — и оно правильное:
  // выдуманная кнопка привела бы прямиком в ошибку.
  const warn = severity === "warn";
  const needsRedo = connected && warn;
  const alarm = "text-amber-600 dark:text-amber-400";

  return (
    <>
      <div className="rounded-xl border border-[var(--border)] px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          {/* Своей строки состояния при ready=false и без доступа нет: там
              нечего сообщать сверх того, что уже сказал сервер, а «подключить
              нечем» — выдумка панели поверх его слов. */}
          {(connected || ready) && (
            <div className="flex min-w-0 items-center gap-2 text-sm">
              {connected &&
                (warn ? (
                  <TriangleAlert className={cn("h-4 w-4 shrink-0", alarm)} />
                ) : (
                  <Check className="h-4 w-4 shrink-0 text-primary" />
                ))}
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

          <div className="flex flex-wrap items-center gap-2">
            {/* «Подключить» — только при заведённом входе по кнопке: без него
                она ведёт прямиком в ошибку. */}
            {ready && (!connected || needsRedo) && (
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
            {/* «Отключить» — при подключённом, независимо от ready: убрать
                лежащий токен можно и тогда, когда переспросить согласие уже
                нечем, и это единственное, что человеку остаётся. */}
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
        </div>

        {/* Текст сервера показываем как есть — цвет берём из severity, а не из
            слов внутри. */}
        {why && (
          <p
            className={cn(
              "mt-2 text-xs leading-relaxed",
              warn ? alarm : "text-[var(--muted-foreground)]",
            )}
          >
            {why}
          </p>
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
