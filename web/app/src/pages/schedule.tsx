import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Video } from "lucide-react";
import { api, type ScheduleEntry } from "@/lib/api";
import { dayRu, timeRu } from "@/lib/fmt";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SelectMenu } from "@/components/ui/select-menu";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";
import { InviteDialog } from "@/components/invite-dialog";

const DAYS = [
  { value: "1", label: "сегодня" },
  { value: "3", label: "три дня" },
  { value: "7", label: "неделя" },
  { value: "14", label: "две недели" },
];

const dayKey = (unix: number) => new Date(unix * 1000).toDateString();

export function SchedulePage() {
  const [days, setDays] = useState("7");
  const [inviting, setInviting] = useState(false);
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["schedule", days], queryFn: () => api.schedule(Number(days)) });

  const decide = useMutation({
    mutationFn: (v: { key: string; decision: "" | "skip" | "attend" }) =>
      api.scheduleOverride(v.key, v.decision),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["schedule"] }),
    onError: (e: unknown) => toast.error(e instanceof Error ? e.message : "не получилось"),
  });

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const entries = q.data.entries ?? [];
  const groups: { day: number; items: ScheduleEntry[] }[] = [];
  const byDay = new Map<string, (typeof groups)[number]>();
  for (const e of entries) {
    const k = dayKey(e.startsAt);
    let g = byDay.get(k);
    if (!g) {
      g = { day: e.startsAt, items: [] };
      byDay.set(k, g);
      groups.push(g);
    }
    g.items.push(e);
  }

  return (
    <>
      <PageHead
        title="Расписание"
        sub="Куда бот пойдёт, а куда нет и почему. Передумать можно заранее — выгонять его из уже идущего звонка на глазах у всех не придётся."
        actions={
          <>
            <SelectMenu
              label="На сколько дней"
              value={days}
              onChange={setDays}
              options={DAYS}
              triggerClassName="w-40 max-w-none"
            />
            <Button variant="outline" onClick={() => setInviting(true)}>
              <Video className="h-4 w-4" />
              Позвать бота
            </Button>
          </>
        }
      />

      {!q.data.calendarOn && (
        <div className="mb-5 rounded-2xl border border-[var(--border)] bg-[var(--muted)]/50 px-5 py-4 text-sm text-[var(--muted-foreground)]">
          Календарь выключен, и расписанию неоткуда взяться. Включить его можно в{" "}
          <Link to="/settings" className="text-primary underline-offset-4 hover:underline">
            настройках
          </Link>
          .
        </div>
      )}

      {entries.length === 0 ? (
        <Empty>
          Впереди ничего не запланировано.
          <br />
          Расписание собирается из календарей команды раз в пятнадцать минут.
        </Empty>
      ) : (
        <div className="space-y-6">
          {groups.map((g) => (
            <section key={g.day}>
              <h2 className="mb-2 text-sm uppercase tracking-wide text-[var(--muted-foreground)]">
                {dayRu(g.day)}
              </h2>
              <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
                {g.items.map((e) => (
                  <div key={e.key} className="flex flex-wrap items-start gap-x-4 gap-y-2 px-5 py-4">
                    <div className="w-14 shrink-0 pt-0.5 font-mono text-sm tabular-nums text-[var(--muted-foreground)]">
                      {timeRu(e.startsAt)}
                    </div>

                    <div className="min-w-0 flex-1">
                      <div className="truncate">{e.title || "Без названия"}</div>
                      <div className="mt-0.5 truncate text-sm text-[var(--muted-foreground)]">
                        {(e.attendees ?? []).join(", ") || "участники не указаны"}
                      </div>
                      {/* Причина названа словами: молчаливое «бот не пришёл»
                          разбирать невозможно, а названная причина чинится за
                          минуту. */}
                      {e.skip && (
                        <div className="mt-1 text-sm text-[var(--muted-foreground)]">
                          {e.override === "attend"
                            ? `${e.skip} — но идти велено вручную`
                            : e.skip}
                        </div>
                      )}
                      {!e.skip && e.override === "skip" && (
                        <div className="mt-1 text-sm text-[var(--muted-foreground)]">
                          снято вручную
                        </div>
                      )}
                    </div>

                    <div className="flex shrink-0 items-center gap-2">
                      {e.recorded ? (
                        <Link to={`/m/${e.recorded}`}>
                          <Badge variant="success">записан</Badge>
                        </Link>
                      ) : e.willAttend ? (
                        <Badge variant="success">иду</Badge>
                      ) : (
                        <Badge>не иду</Badge>
                      )}

                      {!e.recorded &&
                        (e.willAttend ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={decide.isPending}
                            onClick={() => decide.mutate({ key: e.key, decision: "skip" })}
                          >
                            не ходить
                          </Button>
                        ) : (
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={decide.isPending || !e.meetUrl}
                            title={e.meetUrl ? undefined : "без ссылки на Meet идти некуда"}
                            onClick={() => decide.mutate({ key: e.key, decision: "attend" })}
                          >
                            пойти
                          </Button>
                        ))}

                      {e.override !== "" && !e.recorded && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-[var(--muted-foreground)]"
                          disabled={decide.isPending}
                          onClick={() => decide.mutate({ key: e.key, decision: "" })}
                        >
                          как в календаре
                        </Button>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}

      <InviteDialog open={inviting} onClose={() => setInviting(false)} />
    </>
  );
}
