import { useSearchParams, Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Upload } from "lucide-react";
import { api, type MeetingRow } from "@/lib/api";
import {
  dayHeadRu,
  dayKey,
  durRu,
  plural,
  statusAlarm,
  statusPending,
  statusRu,
  timeRu,
} from "@/lib/fmt";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Failed, GroupHead, Loading, PageHead } from "@/components/layout";

// Список созвонов разбит по дням.
//
// До этого он был колонкой одинаковых полос во всю ширину экрана: слева
// название, справа, в девятистах пикселях от него, одинокая плашка, между ними
// пустота. Дата повторялась в каждой строке, отличая строки друг от друга
// последними двумя цифрами — и глазу было не за что зацепиться.
//
// Теперь день назван один раз заголовком группы, в строке остаётся время, а
// плашка стоит сразу за названием — там, где её читают вместе с ним, а не
// после путешествия через полэкрана.

function group(meetings: MeetingRow[]): { key: string; at: number; items: MeetingRow[] }[] {
  const out: { key: string; at: number; items: MeetingRow[] }[] = [];
  const byDay = new Map<string, (typeof out)[number]>();
  for (const m of meetings) {
    const k = dayKey(m.startedAt);
    let g = byDay.get(k);
    if (!g) {
      g = { key: k, at: m.startedAt, items: [] };
      byDay.set(k, g);
      out.push(g);
    }
    g.items.push(m);
  }
  return out;
}

export function MeetingsPage() {
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Number(params.get("p") ?? 1) || 1);
  const q = useQuery({
    queryKey: ["meetings", page],
    queryFn: () => api.meetings(page),
    // Загруженная запись и идущая запись доходят сами. Человек, только что
    // отдавший файл, смотрит именно на эту строку и ждёт, что «разбираю файл»
    // сменится само, — а не что он догадается нажать F5.
    refetchInterval: (query) =>
      (query.state.data?.meetings ?? []).some((m) => statusPending(m.status)) ? 5000 : false,
  });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const { meetings, total, perPage } = q.data;
  const hasNext = page * perPage < total;
  const go = (p: number) => setParams(p > 1 ? { p: String(p) } : {});
  const days = group(meetings);

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
          Бот появится здесь, как только сходит на первый. А если созвон уже прошёл без него —
          запись можно загрузить файлом.
        </Empty>
      ) : (
        <div className="space-y-7">
          {days.map((d) => (
            <section key={d.key}>
              <GroupHead
                title={dayHeadRu(d.at)}
                count={plural(d.items.length, "созвон", "созвона", "созвонов")}
              />
              <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
                {d.items.map((m) => (
                  <MeetingLine key={m.id} m={m} />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}

      {(page > 1 || hasNext) && (
        <div className="mt-6 flex items-center justify-between">
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

function MeetingLine({ m }: { m: MeetingRow }) {
  const people = (m.participants ?? []).join(", ");
  return (
    <Link
      to={`/m/${m.id}`}
      className="flex items-start gap-4 px-4 py-3.5 transition-colors hover:bg-[var(--muted)]/50 sm:px-5"
    >
      <span className="w-11 shrink-0 pt-0.5 font-mono text-sm tabular-nums text-[var(--muted-foreground)]">
        {timeRu(m.startedAt)}
      </span>

      <span className="min-w-0 flex-1">
        {/* Плашки идут следом за названием, а не улетают к правому краю: там
            они читались отдельно от того, к чему относятся, а между ними и
            текстом оставалось полэкрана пустоты. */}
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="truncate">{m.title || "Без названия"}</span>
          {statusAlarm(m.status) && (
            <Badge
              variant={
                m.status === "failed" || m.status === "publish_failed" ? "destructive" : "warning"
              }
              className={cn("shrink-0", statusPending(m.status) && "animate-pulse")}
            >
              {statusRu(m.status)}
            </Badge>
          )}
          {m.tasks > 0 && (
            <span className="shrink-0 text-xs tabular-nums text-[var(--muted-foreground)]">
              {plural(m.tasks, "задача", "задачи", "задач")}
            </span>
          )}
        </span>
        <span className="mt-0.5 block truncate text-sm text-[var(--muted-foreground)]">
          {[durRu(m.durationSec), people].filter(Boolean).join(" · ") ||
            "участники не записаны"}
        </span>
      </span>
    </Link>
  );
}
