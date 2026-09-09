import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { plural } from "@/lib/fmt";
import { Badge } from "@/components/ui/badge";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";

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
        <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
          {rows.map((p) => (
            <Link
              key={p.name}
              to={`/p/${encodeURIComponent(p.name)}`}
              className="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-4 transition-colors hover:bg-[var(--muted)]/50"
            >
              <div className="min-w-0 flex-1">
                <div className="truncate">{p.name}</div>
                <div className="mt-0.5 text-sm text-[var(--muted-foreground)]">
                  {[
                    p.tasks
                      ? plural(p.tasks, "открытая задача", "открытых задачи", "открытых задач")
                      : "открытых задач нет",
                    p.questions ? plural(p.questions, "вопрос", "вопроса", "вопросов") : "",
                    p.decisions ? plural(p.decisions, "решение", "решения", "решений") : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </div>
              </div>
              {p.closed > 0 && <Badge>закрыто {p.closed}</Badge>}
            </Link>
          ))}
        </div>
      )}
    </>
  );
}
