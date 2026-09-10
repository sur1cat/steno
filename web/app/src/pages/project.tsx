import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, type ProjectItem } from "@/lib/api";
import { dateRu, dueRu, overdue } from "@/lib/fmt";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Empty, Failed, GroupHead, Loading, PageHead } from "@/components/layout";
import { t } from "@/lib/i18n";

export function ProjectPage() {
  const { name = "" } = useParams();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["project", name], queryFn: () => api.project(name) });

  // Задачу можно закрыть и вернуть руками. Без этого закрыть её можно было
  // только упомянув на созвоне: сделал тихо — висит вечно. А закрытую по
  // коммиту ошибочно вернуть было нечем вовсе.
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["project", name] });
    qc.invalidateQueries({ queryKey: ["projects"] });
  };
  const close = useMutation({
    mutationFn: (id: string) => api.closeItem(id),
    onSuccess: refresh,
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : t("не получилось")),
  });
  const reopen = useMutation({
    mutationFn: (id: string) => api.reopenItem(id),
    onSuccess: refresh,
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : t("не получилось")),
  });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const items = q.data.items ?? [];
  const open = (kind: ProjectItem["kind"]) =>
    items.filter((i) => i.status === "open" && i.kind === kind);
  const closed = items.filter((i) => i.status !== "open");

  const tasks = open("task");
  const questions = open("question");
  const decisions = open("decision");

  return (
    <>
      <PageHead
        title={q.data.name}
        sub={t("Живое состояние: то, что сейчас открыто, и то, что уже закрылось")}
      />

      {items.length === 0 && <Empty>{t("По проекту пока ничего не накопилось.")}</Empty>}

      {tasks.length > 0 && (
        <Section title={t("Задачи")} count={tasks.length}>
          {tasks.map((it) => (
            <Row key={it.id}>
              <div>
                {it.text}
                {it.owner && <span className="text-[var(--muted-foreground)]"> · {it.owner}</span>}
                <span
                  className={cn(
                    "text-[var(--muted-foreground)]",
                    overdue(it.due) && "text-[var(--destructive)]",
                  )}
                >
                  {" "}
                  · {dueRu(it.due)}
                </span>
              </div>
              <Meta item={it}>
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0 text-[13px] text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
                  onClick={() => close.mutate(it.id)}
                >
                  {t("закрыть")}
                </Button>
              </Meta>
              {it.quote && <Quote>{it.quote}</Quote>}
            </Row>
          ))}
        </Section>
      )}

      {questions.length > 0 && (
        <Section title={t("Открытые вопросы")} count={questions.length}>
          {questions.map((it) => (
            <Row key={it.id}>
              <div>
                {it.text}
                {it.owner && (
                  <span className="text-[var(--muted-foreground)]"> {t("· ждём:")} {it.owner}</span>
                )}
              </div>
              <Meta item={it}>
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0 text-[13px] text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
                  onClick={() => close.mutate(it.id)}
                >
                  {t("закрыть")}
                </Button>
              </Meta>
            </Row>
          ))}
        </Section>
      )}

      {decisions.length > 0 && (
        <Section title={t("Решения")} count={decisions.length}>
          {decisions.map((it) => (
            <Row key={it.id}>
              <div>
                <b>{it.text}</b>
                {it.quote && <span className="text-[var(--muted-foreground)]"> — {it.quote}</span>}
              </div>
              <Meta item={it} />
            </Row>
          ))}
        </Section>
      )}

      {closed.length > 0 && (
        <Section title={t("Закрыто")} count={closed.length}>
          {closed.map((it) => (
            <Row key={it.id}>
              <div className="opacity-65">
                {it.text}
                <span className="text-[var(--muted-foreground)]">
                  {" "}
                  · {it.status === "done" ? t("сделано") : t("снято")}
                </span>
              </div>
              {it.note && <div className="text-[13px] text-[var(--muted-foreground)]">{it.note}</div>}
              <div className="mt-1 flex flex-wrap items-center gap-2 text-[13px] text-[var(--muted-foreground)]">
                <span>{dateRu(it.updatedAt)}</span>
                {it.closedIn && (
                  <Link to={`/m/${it.closedIn}`} className="text-primary underline-offset-4 hover:underline">
                    {t("созвон")}
                  </Link>
                )}
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0 text-[13px] text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
                  onClick={() => reopen.mutate(it.id)}
                >
                  {t("вернуть в работу")}
                </Button>
              </div>
            </Row>
          ))}
        </Section>
      )}
    </>
  );
}

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <section className="mb-7">
      <GroupHead title={title} count={count} />
      <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
        {children}
      </div>
    </section>
  );
}

// Идентификатор больше не висит одиноким столбиком у правого края: на широком
// экране он оказывался в полуметре от строки, к которой относится, и добавлял
// в список ещё одну вертикаль ни о чём. Его место — в служебной строке снизу,
// вместе с датой и ссылкой на созвон.
function Row({ children }: { children: React.ReactNode }) {
  return <div className="max-w-prose space-y-1 px-4 py-3.5 text-sm leading-relaxed sm:px-5">{children}</div>;
}

function Meta({ item, children }: { item: ProjectItem; children?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-[var(--muted-foreground)]">
      <span>{t("с")} {dateRu(item.openedAt)}</span>
      {item.openedIn && (
        <>
          <span aria-hidden="true">·</span>
          <Link
            to={`/m/${item.openedIn}`}
            className="text-primary underline-offset-4 hover:underline"
          >
            {t("созвон")}
          </Link>
        </>
      )}
      <span className="font-mono text-xs text-[var(--muted-foreground)]/60">{item.id}</span>
      {children}
    </div>
  );
}

function Quote({ children }: { children: React.ReactNode }) {
  return (
    <div className="border-l-2 border-[var(--border)] pl-3 text-[13px] italic text-[var(--muted-foreground)]">
      {children}
    </div>
  );
}
