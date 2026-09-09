import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { api, type Channel, type SettingsProject } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";
import { ProjectDialog } from "@/components/project-dialog";
import { ChannelDialog } from "@/components/channel-dialog";
import { GOOGLE_STATUS_KEY } from "@/components/google-connect";

// Настройки разложены по разделам, а не идут одной простынёй: раньше всё шло
// подряд одним свитком, и до нужного места приходилось проматывать остальные.
// Адрес (?tab=) помнит, где человек был: обновление страницы не выбрасывает
// наверх.
//
// Раздела «Секреты» здесь нет. Он показывал имена переменных окружения и то,
// пусты они или нет, — то есть спрашивал у человека из продаж про то, чего он
// не задаёт и задать не может. Токены живут в `steno setup`, у разработчика.

type TabKey = "projects" | "channels";

export function SettingsPage() {
  const q = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [editing, setEditing] = useState<SettingsProject | null>(null);
  const [projectOpen, setProjectOpen] = useState(false);
  const [channel, setChannel] = useState<Channel | null>(null);
  const [notice, setNotice] = useState<{ ok: boolean; text: string } | null>(null);

  // Сюда возвращается человек после согласия у Google: сервер приводит его на
  // /settings?google=ok или ?google=fail&why=…
  //
  // Параметры сразу убираем из адреса — иначе обновление страницы через час
  // снова объявит о том, что случилось один раз, — и заодно переключаем на
  // «Каналы»: ушёл человек из модалки канала, и возвращать его на «Проекты»
  // значит заставить искать, куда он шёл.
  useEffect(() => {
    const g = params.get("google");
    if (!g) return;
    const why = params.get("why") ?? "";
    setNotice(
      g === "ok"
        ? { ok: true, text: "Google подключён." }
        : { ok: false, text: why || "Подключить Google не получилось." },
    );
    qc.invalidateQueries({ queryKey: GOOGLE_STATUS_KEY });
    qc.invalidateQueries({ queryKey: ["settings"] });
    setParams({ tab: "channels" }, { replace: true });
  }, [params, setParams, qc]);

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const { projects, channels } = q.data;
  const raw = params.get("tab");
  const tab: TabKey = raw === "channels" ? raw : "projects";

  // Число рядом с разделом — сколько там всего, а не сколько включено.
  // «Каналы 0» при семи выключенных каналах читается как «каналов нет», и это
  // ровно то место, куда человек идёт их включать.
  const tabs: { key: TabKey; label: string; count: number }[] = [
    { key: "projects", label: "Проекты", count: projects.length },
    { key: "channels", label: "Каналы", count: channels.length },
  ];

  const openProject = (p: SettingsProject | null) => {
    setEditing(p);
    setProjectOpen(true);
  };

  return (
    <>
      <PageHead
        title="Настройки"
        sub="Проекты — чтобы steno понимал, о чём речь на созвоне. Каналы — откуда он берёт созвоны и куда потом присылает итог."
      />

      {/* Про итог возвращения из Google говорим строкой на странице, а не
          тостом: при отказе здесь лежит объяснение сервера, и уехать оно не
          должно раньше, чем его дочитают. */}
      {notice && (
        <div
          className={cn(
            "mb-5 flex items-start gap-3 rounded-2xl border px-4 py-3 text-sm leading-relaxed",
            notice.ok
              ? "border-primary/30 bg-primary/5 text-[var(--foreground)]"
              : "border-[var(--destructive)]/30 bg-[var(--destructive)]/5 text-[var(--destructive)]",
          )}
        >
          <span className="min-w-0 flex-1">{notice.text}</span>
          <button
            type="button"
            onClick={() => setNotice(null)}
            aria-label="Скрыть сообщение"
            className="shrink-0 rounded-lg p-0.5 opacity-60 transition-opacity hover:opacity-100"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

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
              Проект — это то, по чему потом раскладываются задачи и решения с созвонов. Чем
              лучше steno знает, чем проект занят, тем точнее он понимает, о чём шла речь.
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
                    {/* Сколько именно символов в справке — счётчик для того,
                        кто её собирал. Человеку важно одно: собрана или нет. */}
                    {p.primerChars > 0 ? (
                      <Badge className="shrink-0">steno в курсе</Badge>
                    ) : (
                      <Badge variant="warning" className="shrink-0">
                        ещё не изучен
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
            Откуда steno узнаёт о созвонах и куда присылает итог. Открой любой, чтобы включить
            или поменять — что там настраивать, канал расскажет сам.
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
                    {/* Теми же словами, что и в самой модалке канала: «вход» и
                        «выход» короче, но требуют догадаться, чей это вход. */}
                    <span className="text-[13px] text-[var(--muted-foreground)]/70">
                      {c.in && c.out
                        ? "приносит созвоны и уносит follow-up"
                        : c.in
                          ? "приносит созвоны"
                          : "уносит follow-up"}
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


      <ProjectDialog
        open={projectOpen}
        onClose={() => setProjectOpen(false)}
        project={editing}
      />
      <ChannelDialog open={channel !== null} onClose={() => setChannel(null)} channel={channel} />
    </>
  );
}
