import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { api, type Channel, type SettingsProject } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";
import { ProjectDialog } from "@/components/project-dialog";
import { ChannelDialog } from "@/components/channel-dialog";

export function SettingsPage() {
  const q = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const [editing, setEditing] = useState<SettingsProject | null>(null);
  const [projectOpen, setProjectOpen] = useState(false);
  const [channel, setChannel] = useState<Channel | null>(null);

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const { projects, channels, secrets } = q.data;
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

      <section className="mb-8">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-sm uppercase tracking-wide text-[var(--muted-foreground)]">
            Проекты
          </h2>
          <Button variant="outline" size="sm" onClick={() => openProject(null)}>
            <Plus className="h-4 w-4" />
            добавить проект
          </Button>
        </div>

        {projects.length === 0 ? (
          <Empty>
            Проектов пока нет.
            <br />
            Без них follow-up остаётся плоским списком, из которого через месяц не вытащить, что
            к чему относилось.
          </Empty>
        ) : (
          <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
            {projects.map((p) => (
              <button
                key={p.name}
                type="button"
                onClick={() => openProject(p)}
                className="flex w-full flex-wrap items-center gap-x-4 gap-y-1 px-5 py-4 text-left transition-colors hover:bg-[var(--muted)]/50"
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate">{p.name}</div>
                  <div className="mt-0.5 truncate text-sm text-[var(--muted-foreground)]">
                    {[
                      p.about || "без описания",
                      (p.aliases ?? []).length > 0 ? `вслух: ${(p.aliases ?? []).join(", ")}` : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </div>
                </div>
                {p.primerChars > 0 ? (
                  <Badge>справка {p.primerChars} символов</Badge>
                ) : (
                  <Badge variant="warning">справки нет</Badge>
                )}
              </button>
            ))}
          </div>
        )}
      </section>

      <section className="mb-8">
        <h2 className="mb-2 text-sm uppercase tracking-wide text-[var(--muted-foreground)]">
          Каналы
        </h2>
        <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
          {channels.map((c) => (
            <button
              key={c.key}
              type="button"
              onClick={() => setChannel(c)}
              className="flex w-full flex-wrap items-center gap-x-4 gap-y-1 px-5 py-4 text-left transition-colors hover:bg-[var(--muted)]/50"
            >
              <div className="min-w-0 flex-1">
                <div>
                  {c.name}
                  <span className="ml-2 text-sm text-[var(--muted-foreground)]">
                    {c.in && c.out ? "вход и выход" : c.in ? "вход" : "выход"}
                  </span>
                </div>
                <div className="mt-0.5 truncate text-sm text-[var(--muted-foreground)]">
                  {c.summary || c.about}
                </div>
              </div>
              <Badge variant={c.enabled ? "success" : "default"}>
                {c.enabled ? "вкл" : "выкл"}
              </Badge>
            </button>
          ))}
        </div>
        <p className="mt-2 text-sm text-[var(--muted-foreground)]">
          Настройки лежат в базе и главнее <code>steno.json</code>: файл — начальное значение, а
          дальше канал правится здесь. Адресаты перечитываются перед каждой рассылкой, источники
          слушают сеть с самого старта — их переключение подхватится при следующем запуске
          сервиса.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-sm uppercase tracking-wide text-[var(--muted-foreground)]">
          Секреты
        </h2>
        <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
          {secrets.map((s) => (
            <div key={s.env} className="flex items-center justify-between gap-4 px-5 py-3">
              <div className="min-w-0 text-sm">
                {s.what}{" "}
                <code className="text-xs text-[var(--muted-foreground)]">{s.env}</code>
              </div>
              <Badge variant={s.set ? "success" : "warning"}>{s.set ? "задан" : "пусто"}</Badge>
            </div>
          ))}
        </div>
        <p className="mt-2 text-sm text-[var(--muted-foreground)]">
          Панель видит только имя переменной и то, пуста она или нет. Значений здесь нет и не
          будет.
        </p>
      </section>

      <ProjectDialog
        open={projectOpen}
        onClose={() => setProjectOpen(false)}
        project={editing}
      />
      <ChannelDialog open={channel !== null} onClose={() => setChannel(null)} channel={channel} />
    </>
  );
}
