import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Copy, Play, RefreshCw } from "lucide-react";
import { api, type SpecFull } from "@/lib/api";
import { dateRu, dueRu } from "@/lib/fmt";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { specState } from "@/components/spec-mark";
import { Failed, Loading, PageHead } from "@/components/layout";
import { t } from "@/lib/i18n";

// Страница ТЗ.
//
// Порядок разделов — порядок чтения, тот же, что у steno spec show: что и
// зачем, где в коде, что делать, чем проверить, и только в конце — на чём это
// держится и чего не хватает. Дыры внизу не потому, что они менее важны, а
// потому, что до них надо дочитать: список вопросов в начале читается как
// отписка, в конце — как то, с чем человек сейчас пойдёт к автору задачи.
//
// Но одно стоит выше всего: можно ли по этому работать. Если нельзя — причины
// в первой же рамке, а кнопка запуска этого не скрывает и не смягчает.

export function SpecPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const [confirmRun, setConfirmRun] = useState(false);
  const q = useQuery({
    queryKey: ["spec", id],
    queryFn: () => api.spec(id),
    // Пока агент работает, журнал живёт: сервис дописывает его в базу каждые
    // несколько секунд, и страница ходит за ним с той же частотой.
    refetchInterval: (query) => (query.state.data?.spec.status === "running" ? 3000 : false),
  });

  const run = useMutation({
    mutationFn: () => api.runSpec(id),
    onSuccess: () => {
      setConfirmRun(false);
      toast.success(t("Начал: рабочая копия, ветка, агент. Ход работы — ниже."));
      qc.invalidateQueries({ queryKey: ["spec", id] });
    },
    onError: (e: unknown) => {
      setConfirmRun(false);
      toast.error(e instanceof Error ? e.message : t("не получилось"));
    },
  });
  const rebuild = useMutation({
    mutationFn: (itemId: string) => api.buildSpec(itemId),
    onSuccess: () => toast.success(t("Собираю ТЗ заново — новое появится у задачи в проекте, это старое останется.")),
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : t("не получилось")),
  });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const d = q.data;
  const sp = d.spec;
  const b = d.body;
  const st = specState(sp);
  const gate = sp.blocked ?? [];
  const canRun =
    sp.status !== "rejected" && sp.status !== "running" && gate.length === 0 && d.agent.enabled;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(d.markdown);
      toast.success(t("ТЗ скопировано как markdown."));
    } catch {
      toast.error(t("не получилось"));
    }
  };

  return (
    <>
      <PageHead
        title={sp.title || d.item?.text || sp.id}
        sub={
          <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
            <Link to={`/p/${encodeURIComponent(sp.project)}`} className="text-primary underline-offset-4 hover:underline">
              {sp.project}
            </Link>
            <span aria-hidden="true">·</span>
            <span>{dateRu(new Date(sp.at).getTime() / 1000)}</span>
            {sp.usd > 0 && (
              <>
                <span aria-hidden="true">·</span>
                <span>
                  ${sp.usd.toFixed(3)}
                  {sp.model && ` · ${sp.model}`}
                </span>
              </>
            )}
            <Badge variant={st.tone} title={st.hint}>
              {st.label}
            </Badge>
            <span className="font-mono text-xs text-[var(--muted-foreground)]/60">{sp.id}</span>
          </span>
        }
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={copy} title={t("скопировать ТЗ как markdown — отдать человеку или другому агенту")}>
              <Copy className="mr-1.5 h-4 w-4" />
              {t("скопировать")}
            </Button>
            {d.item?.status === "open" && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => rebuild.mutate(sp.item_id)}
                isLoading={rebuild.isPending}
                title={t("собрать ТЗ заново — после того, как на вопросы ответили")}
              >
                <RefreshCw className="mr-1.5 h-4 w-4" />
                {t("собрать заново")}
              </Button>
            )}
            {sp.status !== "rejected" && (
              <Button
                variant="primary"
                size="sm"
                disabled={!canRun}
                onClick={() => setConfirmRun(true)}
                title={
                  !d.agent.enabled
                    ? t("исполнение выключено на машине с сервисом — включается там: steno agent on")
                    : gate.length > 0
                      ? gate.join("\n")
                      : t("рабочая копия, своя ветка, коммит — никогда push")
                }
              >
                <Play className="mr-1.5 h-4 w-4" />
                {sp.status === "running"
                  ? t("работает…")
                  : sp.status === "done" || sp.status === "failed"
                    ? t("запустить ещё раз")
                    : t("запустить агента")}
              </Button>
            )}
          </>
        }
      />

      {/* Сначала — можно ли по этому работать. Человек, открывший ТЗ, должен
          узнать про его негодность в первой же рамке, а не дочитав до конца. */}
      {sp.status === "rejected" ? (
        <Notice tone="muted">
          <b>{t("Задача не взята:")}</b> {sp.reject}
        </Notice>
      ) : gate.length > 0 ? (
        <Notice tone="warn">
          <b>{t("По этому ТЗ нельзя запускать агента.")}</b>
          <ul className="mt-1.5 list-disc space-y-1 pl-5">
            {gate.map((r, i) => (
              <li key={i}>{r}</li>
            ))}
          </ul>
        </Notice>
      ) : !d.agent.enabled ? (
        <Notice tone="muted">
          {t("Исполнение выключено. Это осознанная настройка: по ней steno получает право писать файлы и выполнять команды на машине, где стоит сервис. Включается там же —")}{" "}
          <code className="whitespace-nowrap rounded bg-[var(--muted)] px-1.5 py-0.5">steno agent on</code>
          {t(", или переключателем в строке меню.")}
        </Notice>
      ) : d.agent.probed && !d.agent.ready ? (
        <Notice tone="warn">{d.agent.why}</Notice>
      ) : null}

      {(sp.status === "running" || sp.status === "done" || sp.status === "failed") && (
        <RunPanel d={d} />
      )}

      <div className="space-y-6">
        {d.item && (
          <Section title={t("Задача с созвона")}>
            <div className="text-sm leading-relaxed">
              {d.item.text}
              <span className="text-[var(--muted-foreground)]">
                {d.item.owner && ` · ${d.item.owner}`} · {dueRu(d.item.due)}
                {d.item.status !== "open" && ` · ${d.item.status === "done" ? t("сделано") : t("снято")}`}
              </span>
              {d.item.openedIn && (
                <>
                  {" "}
                  <Link to={`/m/${d.item.openedIn}`} className="text-primary underline-offset-4 hover:underline">
                    {t("созвон")}
                  </Link>
                </>
              )}
            </div>
            {d.item.quote && (
              <div className="mt-2 border-l-2 border-[var(--border)] pl-3 text-[13px] italic text-[var(--muted-foreground)]">
                {d.item.quote}
              </div>
            )}
          </Section>
        )}

        {sp.status !== "rejected" && (
          <>
            {(b.summary ?? []).length > 0 && (
              <Section title={t("Что сделать и зачем")}>
                <div className="space-y-1.5 text-sm leading-relaxed">
                  {(b.summary ?? []).map((l, i) => (
                    <p key={i}>{l}</p>
                  ))}
                </div>
              </Section>
            )}

            <Lines title={t("Что известно")} lines={b.known} />

            {(b.places ?? []).length > 0 && (
              <Section title={t("Где это в коде")}>
                <div className="space-y-2.5">
                  {(b.places ?? []).map((p, i) => {
                    const found = b.found?.[i] ?? false;
                    return (
                      <div key={i} className="text-sm">
                        <div className="flex flex-wrap items-center gap-2">
                          <code
                            className={cn(
                              "rounded bg-[var(--muted)] px-1.5 py-0.5 text-[13px]",
                              !found && "line-through decoration-[var(--destructive)]/60",
                            )}
                          >
                            {p.path}
                          </code>
                          {/* Выдуманный путь не прячем: это сведение о качестве
                              ТЗ, и человеку оно нужнее, чем аккуратный список. */}
                          {!found && (
                            <Badge variant="destructive">{t("такого пути в репозитории нет")}</Badge>
                          )}
                        </div>
                        {p.why && (
                          <div className="mt-0.5 text-[13px] leading-relaxed text-[var(--muted-foreground)]">
                            {p.why}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              </Section>
            )}

            {(b.steps ?? []).length > 0 && (
              <Section title={t("Что сделать")}>
                <ol className="list-decimal space-y-1.5 pl-5 text-sm leading-relaxed">
                  {(b.steps ?? []).map((l, i) => (
                    <li key={i}>{l}</li>
                  ))}
                </ol>
              </Section>
            )}

            <Lines title={t("Чем проверить")} lines={b.checks} />
            <Lines title={t("Чего в этой задаче делать не надо")} lines={b.not_here} />

            {(b.guesses ?? []).length > 0 && (
              <Section
                title={t("На чём это держится")}
                sub={t("Ответов не было, поэтому ТЗ предполагает вот что. Если предположение неверно — переделывать придётся отсюда.")}
              >
                <div className="space-y-2.5 text-sm leading-relaxed">
                  {(b.guesses ?? []).map((g, i) => (
                    <div key={i}>
                      <div>{g.what}</div>
                      {g.if_wrong && (
                        <div className="text-[13px] text-[var(--muted-foreground)]">
                          {t("Если не так:")} {g.if_wrong}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </Section>
            )}

            {/* Раздел, ради которого всё писалось. Он последний и он же
                единственный обязательный: ТЗ без него не проходит проверку. */}
            <Section title={t("Чего не хватает, чтобы это сделать")} tone={(b.unknowns ?? []).length === 0 ? "warn" : undefined}>
              {(b.unknowns ?? []).length === 0 ? (
                <p className="text-sm leading-relaxed">
                  {t("Ни одного вопроса не названо — это само по себе повод не доверять этому ТЗ.")}
                </p>
              ) : (
                <div className="space-y-3">
                  {(b.unknowns ?? []).map((u, i) => (
                    <div key={i} className="text-sm leading-relaxed">
                      <div className="font-medium">{u.question}</div>
                      {u.why && <div className="text-[13px] text-[var(--muted-foreground)]">{u.why}</div>}
                      {u.ask && (
                        <div className="text-[13px] text-[var(--muted-foreground)]">
                          {t("Спросить:")} <span className="text-[var(--foreground)]">{u.ask}</span>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </Section>
          </>
        )}
      </div>

      <ConfirmDialog
        open={confirmRun}
        onClose={() => setConfirmRun(false)}
        onConfirm={() => run.mutate()}
        isPending={run.isPending}
        destructive={false}
        title={t("Отдать ТЗ агенту?")}
        description={`${d.agent.executor || t("агент")} ${t("получит отдельную рабочую копию репозитория и свою ветку")} ${d.agent.branchPrefix}… ${t("Коммит сделает steno, push не делает никто. Вернётся ветка, которую надо посмотреть глазами.")}`}
        confirmLabel={t("Запустить")}
      />
    </>
  );
}

// RunPanel — ход исполнения: ветка, кто нажал, журнал. Журнал прокручивается
// к концу сам, пока идёт работа: смотреть на него и есть смысл только ради
// последней строки.
function RunPanel({ d }: { d: SpecFull }) {
  const sp = d.spec;
  const pre = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (sp.status === "running" && pre.current) {
      pre.current.scrollTop = pre.current.scrollHeight;
    }
  }, [d.log, sp.status]);

  const tone = sp.status === "failed" ? "warn" : sp.status === "done" ? "ok" : "muted";
  return (
    <Notice tone={tone}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <b>
          {sp.status === "running"
            ? t("Агент работает")
            : sp.status === "done"
              ? t("Агент отработал")
              : t("Агент сорвался")}
        </b>
        {sp.branch && (
          <code className="rounded bg-[var(--muted)] px-1.5 py-0.5 text-[13px]">{sp.branch}</code>
        )}
        {sp.run_by && <span className="text-[13px] text-[var(--muted-foreground)]">{t("запустил:")} {sp.run_by}</span>}
      </div>
      {sp.run_error && <div className="mt-1.5 text-sm">{sp.run_error}</div>}
      {sp.status === "done" && sp.repo && sp.branch && (
        <div className="mt-1.5 text-[13px] text-[var(--muted-foreground)]">
          {t("посмотреть:")}{" "}
          <code className="rounded bg-[var(--muted)] px-1.5 py-0.5">
            git -C {sp.repo} diff ..{sp.branch}
          </code>
        </div>
      )}
      {d.log ? (
        <pre
          ref={pre}
          className="mt-3 max-h-72 overflow-auto rounded-xl bg-[var(--muted)]/60 p-3 font-mono text-xs leading-relaxed text-[var(--foreground)]"
        >
          {d.log}
        </pre>
      ) : (
        sp.status === "running" && (
          <div className="mt-2 text-[13px] text-[var(--muted-foreground)]">{t("заводит рабочую копию…")}</div>
        )
      )}
    </Notice>
  );
}

function Notice({ tone, children }: { tone: "warn" | "muted" | "ok"; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        "mb-6 rounded-2xl border px-5 py-4 text-sm leading-relaxed",
        tone === "warn" && "border-amber-400/40 bg-amber-50 text-amber-900 dark:bg-amber-900/15 dark:text-amber-200",
        tone === "ok" && "border-emerald-400/40 bg-emerald-50 text-emerald-900 dark:bg-emerald-900/15 dark:text-emerald-200",
        tone === "muted" && "border-[var(--border)] bg-[var(--card)] text-[var(--foreground)]",
      )}
    >
      {children}
    </div>
  );
}

function Section({
  title,
  sub,
  tone,
  children,
}: {
  title: string;
  sub?: string;
  tone?: "warn";
  children: React.ReactNode;
}) {
  return (
    <section>
      <h2 className="mb-2 text-sm uppercase tracking-wide text-[var(--muted-foreground)]">{title}</h2>
      {sub && <p className="mb-2 max-w-prose text-[13px] leading-relaxed text-[var(--muted-foreground)]">{sub}</p>}
      <div
        className={cn(
          "rounded-2xl border bg-[var(--card)] p-5",
          tone === "warn" ? "border-amber-400/40" : "border-[var(--border)]",
        )}
      >
        {children}
      </div>
    </section>
  );
}

function Lines({ title, lines }: { title: string; lines: string[] | null }) {
  if (!lines || lines.length === 0) return null;
  return (
    <Section title={title}>
      <ul className="list-disc space-y-1.5 pl-5 text-sm leading-relaxed">
        {lines.map((l, i) => (
          <li key={i}>{l}</li>
        ))}
      </ul>
    </Section>
  );
}
