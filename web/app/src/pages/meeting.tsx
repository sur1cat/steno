import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";
import { api, type Segment } from "@/lib/api";
import { clock, dateRu, dueRu, durRu, overdue, statusAlarm, statusRu, targetRu } from "@/lib/fmt";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";
import { t } from "@/lib/i18n";

/** Ближайшая строка не позже момента. Таймкод из follow-up указывает на момент
 *  разговора, а не на строку расшифровки, и точного совпадения может не быть —
 *  без этого половина таймкодов просто никуда не вела бы. */
function nearestStart(segments: Segment[], sec: number): number | null {
  let best: number | null = null;
  for (const s of segments) {
    const t = Math.round(s.start);
    if (t <= sec && (best === null || t > best)) best = t;
  }
  return best ?? (segments.length > 0 ? Math.round(segments[0].start) : null);
}

export function MeetingPage() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["meeting", id], queryFn: () => api.meeting(id) });
  const [confirmDelete, setConfirmDelete] = useState(false);

  // Кнопка стоит на странице созвона, а не в списке: удаление — не «прибраться
  // в списке», а решение по конкретному созвону, и принимают его, глядя на то,
  // что в нём было. В списке одна и та же строка на глаз отличается от соседней
  // двумя цифрами времени, и промах там стоил бы чужого созвона.
  const remove = useMutation({
    mutationFn: () => api.deleteMeeting(id),
    onSuccess: (res) => {
      setConfirmDelete(false);
      // Страница удалённого созвона осталась бы пустой и на живом адресе:
      // уводим в список, и только потом сбрасываем кэш — иначе useQuery этой
      // же страницы успевает сходить за удалённым и показать ошибку.
      nav("/", { replace: true });
      qc.removeQueries({ queryKey: ["meeting", id] });
      qc.invalidateQueries({ queryKey: ["meetings"] });
      qc.invalidateQueries({ queryKey: ["tasks"] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["project"] });
      toast.success(
        res.removed.reopen > 0
          ? `${t("Созвон удалён.")} ${t("Вернулось в работу:")} ${res.removed.reopen}`
          : t("Созвон удалён."),
      );
    },
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : t("не получилось")),
  });

  const audioRef = useRef<HTMLAudioElement>(null);
  const lineRefs = useRef(new Map<number, HTMLDivElement>());
  const [active, setActive] = useState<number | null>(null);
  // Помним, для какого созвона уже отработали ?t=, а не просто «отработали»:
  // роутер переиспользует эту страницу при переходе с созвона на созвон, и с
  // одним флагом вторая ссылка из поиска никуда бы не перематывала.
  const seededFor = useRef<string | null>(null);

  const segments = q.data?.segments ?? [];

  // Перемотка — главное, ради чего таймкоды кликабельны: искать момент в
  // часовом созвоне ползунком невозможно.
  const seek = useCallback(
    (sec: number, play: boolean) => {
      const a = audioRef.current;
      if (a) {
        a.currentTime = sec;
        if (play) void a.play().catch(() => {});
      }
      const target = nearestStart(segments, sec);
      if (target === null) return;
      setActive(target);
      lineRefs.current.get(target)?.scrollIntoView({ block: "center", behavior: "smooth" });
    },
    [segments],
  );

  // ?t= в адресе: так сюда приводят поиск и страница задач. Не проигрываем
  // сразу — человек пришёл читать, а не слушать.
  useEffect(() => {
    if (seededFor.current === id || segments.length === 0) return;
    seededFor.current = id;
    setActive(null);
    const at = Number(params.get("t"));
    if (Number.isFinite(at) && at > 0) seek(at, false);
  }, [id, params, segments, seek]);

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const m = q.data;
  const f = m.followup;
  const links = Object.entries(m.links ?? {}).filter(([, url]) => url);

  const Timecode = ({ at }: { at: number }) => (
    <button
      type="button"
      onClick={() => seek(Math.round(at), true)}
      className="shrink-0 rounded-md bg-[var(--muted)] px-2 py-0.5 font-mono text-xs tabular-nums text-[var(--muted-foreground)] transition-colors hover:bg-primary/15 hover:text-primary"
    >
      {clock(at)}
    </button>
  );

  return (
    <>
      <PageHead
        title={f?.title || m.title || t("Без названия")}
        sub={
          <span className="inline-flex flex-wrap items-center gap-2">
            <span>
              {[dateRu(m.startedAt), durRu(m.durationSec), (m.participants ?? []).join(", ")]
                .filter(Boolean)
                .join(" · ")}
            </span>
            {statusAlarm(m.status) && <Badge variant="warning">{statusRu(m.status)}</Badge>}
            {m.leftReason && <span>{t("вышел:")} {m.leftReason}</span>}
          </span>
        }
        actions={
          <Button
            variant="ghost"
            size="sm"
            className="text-[var(--muted-foreground)] hover:text-[var(--destructive)]"
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2 className="mr-1.5 h-4 w-4" />
            {t("Удалить созвон")}
          </Button>
        }
      />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        {/* Слева — выжимка: то, ради чего созвон вообще разбирали. */}
        <div className="space-y-6">
          {!m.hasFollowup || !f ? (
            <Empty>
              {t("Follow-up ещё не готов.")}
              <br />
              <code className="rounded bg-[var(--muted)] px-1.5 py-0.5">steno process {m.id}</code>
            </Empty>
          ) : (
            <>
              {f.tldr && f.tldr.length > 0 && (
                <Section title={t("Коротко")}>
                  <ul className="list-disc space-y-1.5 pl-5 text-sm leading-relaxed">
                    {f.tldr.map((line, i) => (
                      <li key={i}>{line}</li>
                    ))}
                  </ul>
                </Section>
              )}

              {f.action_items && f.action_items.length > 0 && (
                <Section title={t("Задачи")}>
                  <div className="space-y-3">
                    {f.action_items.map((a, i) => (
                      <div key={i} className="flex items-start gap-3">
                        <Timecode at={a.at} />
                        <div className="min-w-0 flex-1 text-sm">
                          <div>
                            <span className="text-[var(--foreground)]">{a.owner}</span>
                            <span className="text-[var(--muted-foreground)]"> — </span>
                            {a.what}{" "}
                            <span
                              className={cn(
                                "text-[var(--muted-foreground)]",
                                overdue(a.due) && "text-[var(--destructive)]",
                              )}
                            >
                              · {dueRu(a.due)}
                            </span>
                          </div>
                          {a.quote && <Quote>{a.quote}</Quote>}
                        </div>
                      </div>
                    ))}
                  </div>
                </Section>
              )}

              {f.decisions && f.decisions.length > 0 && (
                <Section title={t("Решения")}>
                  <div className="space-y-3">
                    {f.decisions.map((d, i) => (
                      <div key={i} className="flex items-start gap-3">
                        <Timecode at={d.at} />
                        <div className="min-w-0 flex-1 text-sm">
                          <b>{d.what}</b>
                          {d.why && <span className="text-[var(--muted-foreground)]"> — {d.why}</span>}
                        </div>
                      </div>
                    ))}
                  </div>
                </Section>
              )}

              {f.open_questions && f.open_questions.length > 0 && (
                <Section title={t("Открытые вопросы")}>
                  <div className="space-y-3">
                    {f.open_questions.map((qq, i) => (
                      <div key={i} className="flex items-start gap-3">
                        <Timecode at={qq.at} />
                        <div className="min-w-0 flex-1 text-sm">
                          {qq.question}{" "}
                          <span className="text-[var(--muted-foreground)]">
                            {t("· ждём:")} {qq.waiting_on || t("не определено")}
                          </span>
                        </div>
                      </div>
                    ))}
                  </div>
                </Section>
              )}

              {f.risks && f.risks.length > 0 && (
                <Section title={t("Риски")}>
                  <ul className="list-disc space-y-1.5 pl-5 text-sm leading-relaxed">
                    {f.risks.map((r, i) => (
                      <li key={i}>{r}</li>
                    ))}
                  </ul>
                </Section>
              )}
            </>
          )}

          {m.spend?.input > 0 && (
            <p className="text-sm text-[var(--muted-foreground)]">
              {t("follow-up: вход")} {m.spend.input} {t("токенов, выход")} {m.spend.output}
              {m.spend.usd > 0 && ` · $${m.spend.usd.toFixed(3)}`}
            </p>
          )}

          {links.length > 0 && (
            <Section title={t("Опубликовано")}>
              <div className="flex flex-wrap gap-3 text-sm">
                {links.map(([target, url]) => (
                  <a
                    key={target}
                    href={url}
                    target="_blank"
                    rel="noopener"
                    className="text-primary underline-offset-4 hover:underline"
                  >
                    {targetRu(target)}
                  </a>
                ))}
              </div>
            </Section>
          )}
        </div>

        {/* Справа — запись и расшифровка: то, чем выжимку проверяют. */}
        <div className="space-y-4">
          {m.hasAudio && (
            <div className="rounded-2xl border border-[var(--border)] bg-[var(--card)] p-3">
              <audio ref={audioRef} controls preload="metadata" src={`/audio/${m.id}`} className="w-full" />
            </div>
          )}

          <h2 className="text-sm uppercase tracking-wide text-[var(--muted-foreground)]">
            {t("Расшифровка")}
          </h2>

          {segments.length === 0 ? (
            <Empty>{t("Расшифровки нет.")}</Empty>
          ) : (
            <div className="max-h-[calc(100vh-16rem)] space-y-0.5 overflow-y-auto rounded-2xl border border-[var(--border)] bg-[var(--card)] p-3">
              {segments.map((s, i) => {
                const key = Math.round(s.start);
                return (
                  <div
                    key={i}
                    ref={(el) => {
                      // Снятую строку убираем из карты: без этого при переходе
                      // с созвона на созвон в ней копятся узлы прошлых страниц.
                      if (el) lineRefs.current.set(key, el);
                      else lineRefs.current.delete(key);
                    }}
                    className={cn(
                      "flex gap-3 rounded-lg px-2 py-1.5 transition-colors",
                      active === key && "bg-primary/10",
                    )}
                  >
                    <button
                      type="button"
                      onClick={() => seek(key, true)}
                      className="h-fit shrink-0 font-mono text-xs tabular-nums text-[var(--muted-foreground)] transition-colors hover:text-primary"
                    >
                      {clock(s.start)}
                    </button>
                    <div className="min-w-0 flex-1 text-sm leading-relaxed">
                      {s.speaker && (
                        <div className="text-xs text-[var(--muted-foreground)]">{s.speaker}</div>
                      )}
                      <p>{s.text}</p>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {/* Цену считает Go и присылает готовой фразой: те же цифры и те же слова,
          что в `steno ui` и `steno rm`. Согласие — отдельной кнопкой, а не
          Enter'ом: отменить удаление нечем. */}
      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => remove.mutate()}
        isPending={remove.isPending}
        title={`${t("Удалить")} “${f?.title || m.title || t("Без названия")}”?`}
        description={`${t("Это навсегда:")} ${m.removal?.text ?? ""}.`}
        confirmLabel={t("Удалить созвон")}
      />
    </>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="mb-2 text-sm uppercase tracking-wide text-[var(--muted-foreground)]">
        {title}
      </h2>
      <div className="rounded-2xl border border-[var(--border)] bg-[var(--card)] p-5">{children}</div>
    </section>
  );
}

function Quote({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-1 border-l-2 border-[var(--border)] pl-3 text-[13px] italic text-[var(--muted-foreground)]">
      {children}
    </div>
  );
}
