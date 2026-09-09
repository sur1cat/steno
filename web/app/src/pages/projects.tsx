import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight } from "lucide-react";
import { api, type ProjectRow } from "@/lib/api";
import { pluralWord } from "@/lib/fmt";
import { cn } from "@/lib/utils";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";

// Проекты — сеткой карточек, а не полосами во всю ширину.
//
// В проекте всего четыре числа, и растянутая на 1440 пикселей строка ради них
// оставляла посреди экрана пустое поле, а взгляду задавала маршрут «название
// слева — плашка справа» на каждой строке. В карточке те же числа стоят рядом
// и читаются одним взглядом, а восемь проектов видны сразу, без прокрутки.

const STATS: { key: keyof ProjectRow; label: [string, string, string] }[] = [
  { key: "tasks", label: ["задача", "задачи", "задач"] },
  { key: "questions", label: ["вопрос", "вопроса", "вопросов"] },
  { key: "decisions", label: ["решение", "решения", "решений"] },
];

export function ProjectsPage() {
  const q = useQuery({ queryKey: ["projects"], queryFn: api.projects });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const rows = q.data.projects ?? [];

  return (
    <>
      <PageHead
        title="Проекты"
        sub="Что накопилось за все созвоны — не по встречам, а по делу"
      />

      {rows.length === 0 ? (
        <Empty>
          Пока пусто.
          <br />
          Проекты появятся, когда пройдёт первый созвон с follow-up.
        </Empty>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {rows.map((p) => (
            <Link
              key={p.name}
              to={`/p/${encodeURIComponent(p.name)}`}
              className="group flex flex-col gap-4 rounded-2xl border border-[var(--border)] bg-[var(--card)] p-5 transition-colors hover:border-primary/40 hover:bg-[var(--muted)]/30"
            >
              <div className="flex items-start justify-between gap-2">
                <span className="min-w-0 break-words text-[15px] leading-snug">{p.name}</span>
                <ArrowUpRight className="h-4 w-4 shrink-0 text-[var(--muted-foreground)] opacity-0 transition-opacity group-hover:opacity-100" />
              </div>

              {/* Числа идут сразу под названием во всех карточках, а закрытое
                  уезжает вниз: иначе карточка с закрытыми задачами поднимает
                  свою строку чисел, и в ряду они стоят на разной высоте. */}
              <div className="flex flex-wrap gap-x-5 gap-y-2">
                {STATS.map(({ key, label }) => {
                  const n = p[key] as number;
                  return (
                    <div key={key}>
                      {/* Ноль не прячем: «открытых задач нет» — это тоже
                          состояние проекта, и оно важнее пустого места. */}
                      <div
                        className={cn(
                          "text-xl tabular-nums leading-none",
                          n === 0 && "text-[var(--muted-foreground)]/40",
                        )}
                      >
                        {n}
                      </div>
                      <div className="mt-1 text-[11px] uppercase tracking-wide text-[var(--muted-foreground)]">
                        {pluralWord(n, ...label)}
                      </div>
                    </div>
                  );
                })}
              </div>

              {p.closed > 0 && (
                <div className="mt-auto border-t border-[var(--border)] pt-3 text-[13px] text-[var(--muted-foreground)]">
                  закрыто {p.closed}
                </div>
              )}
            </Link>
          ))}
        </div>
      )}
    </>
  );
}
