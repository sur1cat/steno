import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { api, type Channel, type SettingsProject } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";
import { ProjectDialog } from "@/components/project-dialog";
import { ChannelDialog } from "@/components/channel-dialog";

// Настройки разложены по разделам, а не идут одной простынёй.
//
// Раньше проекты, каналы и секреты шли подряд одним свитком: чтобы посмотреть,
// задан ли токен Slack, приходилось проматывать все проекты и все каналы, а
// найдя — прокручивать обратно. Разделы держат страницу короткой, а адрес
// (?tab=) помнит, где человек был: обновление страницы не выбрасывает наверх.

type TabKey = "projects" | "channels" | "secrets";

export function SettingsPage() {
  const q = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const [params, setParams] = useSearchParams();
  const [editing, setEditing] = useState<SettingsProject | null>(null);
  const [projectOpen, setProjectOpen] = useState(false);
  const [channel, setChannel] = useState<Channel | null>(null);

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const { projects, channels, secrets } = q.data;
  const raw = params.get("tab");
  const tab: TabKey =
    raw === "channels" || raw === "secrets" || raw === "projects" ? raw : "projects";

  // Число рядом с разделом — сколько там всего, а не сколько включено.
  // «Каналы 0» при семи выключенных каналах читается как «каналов нет», и это
  // ровно то место, куда человек идёт их включать.
  const tabs: { key: TabKey; label: string; count: number }[] = [
    { key: "projects", label: "Проекты", count: projects.length },
    { key: "channels", label: "Каналы", count: channels.length },
    { key: "secrets", label: "Секреты", count: secrets.length },
  ];

  const openProject = (p: SettingsProject | null) => {
    setEditing(p);
    setProjectOpen(true);
  };

  return (
    <>
      <PageHead
        title="Настройки"
        sub="Проекты и каналы правятся здесь. Секреты — только переменными окружения: класть токены в ту же базу, где лежат расшифровки всех разговоров, не стоит."
      />

      {/* w-fit: полоса разделов обнимает свои три кнопки. Растянутая во всю
          ширину, она читается как пустая панель с кнопками в углу. */}
      <div className="mb-6 flex w-fit max-w-full flex-wrap items-center gap-1 rounded-xl bg-[var(--muted)] p-1">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setParams(t.key === "projects" ? {} : { tab: t.key })}
            aria-current={tab === t.key ? "page" : undefined}
            className={cn(
              "flex items-center gap-2 rounded-lg px-4 py-2 text-sm transition-colors",
              tab === t.key
                ? "bg-[var(--card)] text-[var(--foreground)] shadow-[0_1px_3px_rgba(0,0,0,0.06)]"
                : "text-[var(--muted-foreground)] hover:text-[var(--foreground)]",
            )}
          >
            {t.label}
            <span className="text-xs tabular-nums text-[var(--muted-foreground)]">{t.count}</span>
          </button>
        ))}
      </div>

      {tab === "projects" && (
        <section>
          <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
            <p className="max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
              Проект — это то, к чему привязываются задачи и решения с созвонов. Справка по нему
              собирается из репозиториев и заметок и уходит в модель вместе с расшифровкой.
            </p>
            <Button variant="outline" size="sm" onClick={() => openProject(null)}>
              <Plus className="h-4 w-4" />
              добавить проект
            </Button>
          </div>

          {projects.length === 0 ? (
            <Empty>
              Проектов пока нет.
              <br />
              Без них follow-up остаётся плоским списком, из которого через месяц не вытащить,
              что к чему относилось.
            </Empty>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2">
              {projects.map((p) => (
                <button
                  key={p.name}
                  type="button"
                  onClick={() => openProject(p)}
                  className="flex flex-col gap-2 rounded-2xl border border-[var(--border)] bg-[var(--card)] p-4 text-left transition-colors hover:border-primary/40 hover:bg-[var(--muted)]/30"
                >
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="min-w-0 break-words">{p.name}</span>
                    {p.primerChars > 0 ? (
                      <Badge className="shrink-0">справка {p.primerChars} симв.</Badge>
                    ) : (
                      <Badge variant="warning" className="shrink-0">
                        справки нет
                      </Badge>
                    )}
                  </span>
                  <span className="text-sm leading-relaxed text-[var(--muted-foreground)]">
                    {p.about || "без описания"}
                  </span>
                  {(p.aliases ?? []).length > 0 && (
                    <span className="text-[13px] text-[var(--muted-foreground)]/80">
                      вслух: {(p.aliases ?? []).join(", ")}
                    </span>
                  )}
                </button>
              ))}
            </div>
          )}
        </section>
      )}

      {tab === "channels" && (
        <section>
          <p className="mb-3 max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
            Настройки лежат в базе и главнее <code>steno.json</code>: файл — начальное значение,
            а дальше канал правится здесь. Адресаты перечитываются перед каждой рассылкой,
            источники слушают сеть с самого старта — их переключение подхватится при следующем
            запуске сервиса.
          </p>
          <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
            {channels.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={() => setChannel(c)}
                className="flex w-full items-start gap-4 px-4 py-3.5 text-left transition-colors hover:bg-[var(--muted)]/50 sm:px-5"
              >
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span>{c.name}</span>
                    <Badge variant={c.enabled ? "success" : "default"} className="shrink-0">
                      {c.enabled ? "вкл" : "выкл"}
                    </Badge>
                    <span className="text-xs uppercase tracking-wide text-[var(--muted-foreground)]/70">
                      {c.in && c.out ? "вход и выход" : c.in ? "вход" : "выход"}
                    </span>
                  </span>
                  <span className="mt-1 block max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
                    {c.summary || c.about}
                  </span>
                </span>
              </button>
            ))}
          </div>
        </section>
      )}

      {tab === "secrets" && (
        <section>
          <p className="mb-3 max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
            Панель видит только имя переменной и то, пуста она или нет. Значений здесь нет и не
            будет.
          </p>
          <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
            {secrets.map((s) => (
              <div
                key={s.env}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-sm sm:px-5"
              >
                <Badge variant={s.set ? "success" : "warning"} className="shrink-0 order-first">
                  {s.set ? "задан" : "пусто"}
                </Badge>
                <span className="min-w-0">{s.what}</span>
                <code className="ml-auto shrink-0 rounded-md bg-[var(--muted)] px-2 py-0.5 text-xs text-[var(--muted-foreground)]">
                  {s.env}
                </code>
              </div>
            ))}
          </div>
        </section>
      )}

      <ProjectDialog
        open={projectOpen}
        onClose={() => setProjectOpen(false)}
        project={editing}
      />
      <ChannelDialog open={channel !== null} onClose={() => setChannel(null)} channel={channel} />
    </>
  );
}
