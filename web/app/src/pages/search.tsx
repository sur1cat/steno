import { Link, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api, type SearchHit } from "@/lib/api";
import { clock, dateRu, plural } from "@/lib/fmt";
import { Badge } from "@/components/ui/badge";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";

/** Подсветка собирается из кусков, а не из готовой разметки: в индексе лежит
 *  сырая расшифровка вместе со всем, что люди наговорили и что попало в
 *  название встречи из Telegram. Отдавать это браузеру как HTML нельзя. */
function Snippet({ hit }: { hit: SearchHit }) {
  return (
    <span>
      {(hit.parts ?? []).map((p, i) =>
        p.hit ? (
          <mark key={i} className="rounded bg-primary/25 px-0.5 text-[var(--foreground)]">
            {p.text}
          </mark>
        ) : (
          <span key={i}>{p.text}</span>
        ),
      )}
    </span>
  );
}

export function SearchPage() {
  const [params] = useSearchParams();
  const q = (params.get("q") ?? "").trim();
  const res = useQuery({ queryKey: ["search", q], queryFn: () => api.search(q), enabled: q !== "" });

  const hits = res.data?.hits ?? [];

  // Двадцать совпадений из одного разговора — это один результат, а не
  // двадцать: иначе один болтливый созвон вытесняет со страницы все остальные.
  const groups: { id: string; title: string; startedAt: number; hits: SearchHit[] }[] = [];
  const byId = new Map<string, (typeof groups)[number]>();
  for (const h of hits) {
    let g = byId.get(h.meetingId);
    if (!g) {
      g = { id: h.meetingId, title: h.title, startedAt: h.startedAt, hits: [] };
      byId.set(h.meetingId, g);
      groups.push(g);
    }
    g.hits.push(h);
  }

  return (
    <>
      <PageHead
        title={q ? `«${q}»` : "Поиск"}
        sub={
          q
            ? `${plural(hits.length, "совпадение", "совпадения", "совпадений")} в ${plural(
                groups.length,
                "созвоне",
                "созвонах",
                "созвонах",
              )}`
            : "Ищет по расшифровкам и по follow-up сразу"
        }
      />

      {res.isError && <Failed error={res.error} />}
      {q !== "" && res.isPending && <Loading />}

      {q !== "" && !res.isPending && groups.length === 0 && (
        <Empty>
          Ничего не нашлось.
          <br />
          Поиск понимает начало слова: «релиз» найдёт и «релиза», и «релизом».
        </Empty>
      )}

      <div className="space-y-5">
        {groups.map((g) => (
          <div key={g.id} className="overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
            <div className="flex items-center justify-between gap-3 border-b border-[var(--border)] px-5 py-3">
              <Link to={`/m/${g.id}`} className="truncate text-primary underline-offset-4 hover:underline">
                {g.title || "Без названия"}
              </Link>
              <Badge>{dateRu(g.startedAt)}</Badge>
            </div>
            <div className="divide-y divide-[var(--border)]">
              {g.hits.map((h, i) => (
                <div key={i} className="flex items-start gap-3 px-5 py-3 text-sm">
                  {h.kind === "расшифровка" ? (
                    <Link
                      to={`/m/${h.meetingId}?t=${Math.round(h.at)}`}
                      className="shrink-0 rounded-md bg-[var(--muted)] px-2 py-0.5 font-mono text-xs tabular-nums text-[var(--muted-foreground)] transition-colors hover:bg-primary/15 hover:text-primary"
                    >
                      {clock(h.at)}
                    </Link>
                  ) : (
                    <Badge className="shrink-0">итог</Badge>
                  )}
                  <div className="min-w-0 flex-1 leading-relaxed">
                    {h.speaker && (
                      <span className="text-[var(--muted-foreground)]">
                        <b>{h.speaker}</b> ·{" "}
                      </span>
                    )}
                    <Snippet hit={h} />
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </>
  );
}
