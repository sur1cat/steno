import { useSearchParams, Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { dateRu, durRu, plural, statusAlarm, statusRu } from "@/lib/fmt";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";

export function MeetingsPage() {
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Number(params.get("p") ?? 1) || 1);
  const q = useQuery({ queryKey: ["meetings", page], queryFn: () => api.meetings(page) });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const { meetings, total, perPage } = q.data;
  const hasNext = page * perPage < total;
  const go = (p: number) => setParams(p > 1 ? { p: String(p) } : {});

  return (
    <>
      <PageHead
        title="Созвоны"
        sub={`${plural(total, "созвон", "созвона", "созвонов")} в архиве`}
      />

      {meetings.length === 0 ? (
        <Empty>
          Пока ни одного созвона.
          <br />
          Бот появится здесь, как только сходит на первый.
        </Empty>
      ) : (
        <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
          {meetings.map((m) => (
            <Link
              key={m.id}
              to={`/m/${m.id}`}
              className="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-4 transition-colors hover:bg-[var(--muted)]/50"
            >
              <div className="min-w-0 flex-1">
                <div className="truncate">{m.title || "Без названия"}</div>
                <div className="mt-0.5 truncate text-sm text-[var(--muted-foreground)]">
                  {[
                    dateRu(m.startedAt),
                    durRu(m.durationSec),
                    (m.participants ?? []).join(", "),
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                {m.tasks > 0 && (
                  <Badge>{plural(m.tasks, "задача", "задачи", "задач")}</Badge>
                )}
                {statusAlarm(m.status) && (
                  <Badge variant="warning">{statusRu(m.status)}</Badge>
                )}
              </div>
            </Link>
          ))}
        </div>
      )}

      {(page > 1 || hasNext) && (
        <div className="mt-5 flex items-center justify-between">
          <Button variant="ghost" disabled={page <= 1} onClick={() => go(page - 1)}>
            ← новее
          </Button>
          <Button variant="ghost" disabled={!hasNext} onClick={() => go(page + 1)}>
            старее →
          </Button>
        </div>
      )}
    </>
  );
}
