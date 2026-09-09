import { Link, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api, type TaskRow } from "@/lib/api";
import { dateRu, dueRu, overdue, plural } from "@/lib/fmt";
import { cn } from "@/lib/utils";
import { SelectMenu } from "@/components/ui/select-menu";
import { Empty, Failed, GroupHead, Loading, PageHead } from "@/components/layout";

const UNASSIGNED = "не назначен";

// «Не назначен» первым — это то, что вообще ни на ком не висит, и именно оно
// требует решения. Дальше по алфавиту: тот же порядок, что у фильтра сверху,
// иначе человека приходится искать на странице дважды.
function lessOwner(a: string, b: string) {
  if ((a === UNASSIGNED) !== (b === UNASSIGNED)) return a === UNASSIGNED ? -1 : 1;
  return a.localeCompare(b, "ru");
}

export function TasksPage() {
  const [params, setParams] = useSearchParams();
  const owner = params.get("owner") ?? "";
  const q = useQuery({ queryKey: ["tasks", owner], queryFn: () => api.tasks(owner) });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const tasks = q.data.tasks ?? [];
  const owners = [...(q.data.owners ?? [])].sort(lessOwner);

  const groups: { owner: string; tasks: TaskRow[] }[] = [];
  const byOwner = new Map<string, (typeof groups)[number]>();
  for (const t of tasks) {
    let g = byOwner.get(t.owner);
    if (!g) {
      g = { owner: t.owner, tasks: [] };
      byOwner.set(t.owner, g);
      groups.push(g);
    }
    g.tasks.push(t);
  }
  groups.sort((a, b) => lessOwner(a.owner, b.owner));

  return (
    <>
      <PageHead
        title="Задачи"
        sub={`${plural(tasks.length, "задача", "задачи", "задач")} со всех созвонов${
          owner ? ` · ${owner}` : ""
        }`}
        actions={
          owners.length > 0 && (
            <SelectMenu
              label="Кто отвечает"
              value={owner}
              onChange={(v) => setParams(v ? { owner: v } : {})}
              options={[
                { value: "", label: "все" },
                ...owners.map((o) => ({ value: o, label: o })),
              ]}
              triggerClassName="w-56 max-w-none"
            />
          )
        }
      />

      {groups.length === 0 ? (
        <Empty>Задач пока нет.</Empty>
      ) : (
        <div className="space-y-7">
          {groups.map((g) => (
            <section key={g.owner}>
              <GroupHead
                title={g.owner}
                count={plural(g.tasks.length, "задача", "задачи", "задач")}
              />
              <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
                {g.tasks.map((t, i) => (
                  <div key={i} className="px-4 py-3.5 text-sm sm:px-5">
                    <div className="max-w-prose leading-relaxed">
                      {t.what}{" "}
                      <span
                        className={cn(
                          "text-[var(--muted-foreground)]",
                          overdue(t.due) && "text-[var(--destructive)]",
                        )}
                      >
                        · {dueRu(t.due)}
                      </span>
                    </div>
                    <div className="mt-1 text-[13px] text-[var(--muted-foreground)]">
                      <Link
                        to={`/m/${t.meetingId}?t=${Math.round(t.at)}`}
                        className="text-primary underline-offset-4 hover:underline"
                      >
                        {t.meetingTitle || "созвон"}
                      </Link>{" "}
                      · {dateRu(t.meetingAt)}
                    </div>
                    {t.quote && (
                      <div className="mt-1.5 max-w-prose border-l-2 border-[var(--border)] pl-3 text-[13px] italic leading-relaxed text-[var(--muted-foreground)]">
                        {t.quote}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
    </>
  );
}
