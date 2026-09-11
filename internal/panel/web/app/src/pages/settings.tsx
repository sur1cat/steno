import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { api, type AgentState, type Channel, type SettingsProject } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Failed, Loading, PageHead } from "@/components/layout";
import { ProjectDialog } from "@/components/project-dialog";
import { ChannelDialog } from "@/components/channel-dialog";
import { GOOGLE_STATUS_KEY } from "@/components/google-connect";
import { t } from "@/lib/i18n";

// Настройки разложены по разделам, а не идут одной простынёй: раньше всё шло
// подряд одним свитком, и до нужного места приходилось проматывать остальные.
// Адрес (?tab=) помнит, где человек был: обновление страницы не выбрасывает
// наверх.
//
// Раздела «Секреты» здесь нет. Он показывал имена переменных окружения и то,
// пусты они или нет, — то есть спрашивал у человека из продаж про то, чего он
// не задаёт и задать не может. Токены живут в `steno setup`, у разработчика.

type TabKey = "projects" | "channels" | "brain" | "agent";

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
        ? { ok: true, text: t("Google подключён.") }
        : { ok: false, text: why || t("Подключить Google не получилось.") },
    );
    qc.invalidateQueries({ queryKey: GOOGLE_STATUS_KEY });
    qc.invalidateQueries({ queryKey: ["settings"] });
    setParams({ tab: "channels" }, { replace: true });
  }, [params, setParams, qc]);

  if (q.isPending) return <Loading />;
  if (q.isError) return <Failed error={q.error} />;

  const { projects, channels, agent } = q.data;
  // Раздел «Разбор» приезжает тем же списком, что и каналы, — форма у них одна
  // и та же. Но каналом он не является: выключить его нельзя, и стоять он
  // должен отдельно, а не седьмой строкой среди Slack и Telegram.
  const io = channels.filter((c) => c.kind !== "brain");
  const brain = channels.filter((c) => c.kind === "brain");
  const raw = params.get("tab");
  const tab: TabKey = raw === "channels" || raw === "brain" || raw === "agent" ? raw : "projects";

  // Число рядом с разделом — сколько там всего, а не сколько включено.
  // «Каналы 0» при семи выключенных каналах читается как «каналов нет», и это
  // ровно то место, куда человек идёт их включать.
  const tabs: { key: TabKey; label: string; count: number }[] = [
    { key: "projects", label: t("Проекты"), count: projects.length },
    { key: "channels", label: t("Каналы"), count: io.length },
  ];
  // У «Разбора» числа нет: он ровно один, и «Разбор 1» ничего не сообщает.
  const brainTab = brain.length > 0;

  const openProject = (p: SettingsProject | null) => {
    setEditing(p);
    setProjectOpen(true);
  };

  return (
    <>
      <PageHead
        title={t("Настройки")}
        sub={t("Проекты — чтобы steno понимал, о чём речь на созвоне. Каналы — откуда он берёт созвоны и куда потом присылает итог.")}
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
            aria-label={t("Скрыть сообщение")}
            className="shrink-0 rounded-lg p-0.5 opacity-60 transition-opacity hover:opacity-100"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      {/* w-fit: полоса разделов обнимает свои три кнопки. Растянутая во всю
          ширину, она читается как пустая панель с кнопками в углу. */}
      <div className="mb-6 flex w-fit max-w-full flex-wrap items-center gap-1 rounded-xl bg-[var(--muted)] p-1">
        {tabs.map((item) => (
          <button
            key={item.key}
            type="button"
            onClick={() => setParams(item.key === "projects" ? {} : { tab: item.key })}
            aria-current={tab === item.key ? "page" : undefined}
            className={cn(
              "flex items-center gap-2 rounded-lg px-4 py-2 text-sm transition-colors",
              tab === item.key
                ? "bg-[var(--card)] text-[var(--foreground)] shadow-[0_1px_3px_rgba(0,0,0,0.06)]"
                : "text-[var(--muted-foreground)] hover:text-[var(--foreground)]",
            )}
          >
            {item.label}
            <span className="text-xs tabular-nums text-[var(--muted-foreground)]">{item.count}</span>
          </button>
        ))}
        {brainTab && (
          <button
            type="button"
            onClick={() => setParams({ tab: "brain" })}
            aria-current={tab === "brain" ? "page" : undefined}
            className={cn(
              "flex items-center gap-2 rounded-lg px-4 py-2 text-sm transition-colors",
              tab === "brain"
                ? "bg-[var(--card)] text-[var(--foreground)] shadow-[0_1px_3px_rgba(0,0,0,0.06)]"
                : "text-[var(--muted-foreground)] hover:text-[var(--foreground)]",
            )}
          >
            {t("Разбор")}
          </button>
        )}
        {/* У «Агента» числа тоже нет, зато есть состояние: выключатель стоит
            не здесь, и человек должен видеть его положение, не открывая
            раздел. */}
        <button
          type="button"
          onClick={() => setParams({ tab: "agent" })}
          aria-current={tab === "agent" ? "page" : undefined}
          className={cn(
            "flex items-center gap-2 rounded-lg px-4 py-2 text-sm transition-colors",
            tab === "agent"
              ? "bg-[var(--card)] text-[var(--foreground)] shadow-[0_1px_3px_rgba(0,0,0,0.06)]"
              : "text-[var(--muted-foreground)] hover:text-[var(--foreground)]",
          )}
        >
          {t("Агент")}
          <span
            className={cn(
              "h-1.5 w-1.5 rounded-full",
              agent?.enabled ? "bg-emerald-500" : "bg-[var(--muted-foreground)]/40",
            )}
            aria-hidden="true"
          />
        </button>
      </div>

      {tab === "projects" && (
        <section>
          <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
            <p className="max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
              {t("Проект — это то, по чему потом раскладываются задачи и решения с созвонов. Чем\n              лучше steno знает, чем проект занят, тем точнее он понимает, о чём шла речь.")}
            </p>
            <Button variant="outline" size="sm" onClick={() => openProject(null)}>
              <Plus className="h-4 w-4" />
              {t("добавить проект")}
            </Button>
          </div>

          {projects.length === 0 ? (
            <Empty>
              {t("Проектов пока нет.")}
              <br />
              {t("Без них follow-up остаётся плоским списком, из которого через месяц не вытащить,\n              что к чему относилось.")}
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
                      <Badge className="shrink-0">{t("steno в курсе")}</Badge>
                    ) : (
                      <Badge variant="warning" className="shrink-0">
                        {t("ещё не изучен")}
                      </Badge>
                    )}
                  </span>
                  <span className="text-sm leading-relaxed text-[var(--muted-foreground)]">
                    {p.about || t("без описания")}
                  </span>
                  {(p.aliases ?? []).length > 0 && (
                    <span className="text-[13px] text-[var(--muted-foreground)]/80">
                      {t("вслух:")} {(p.aliases ?? []).join(", ")}
                    </span>
                  )}
                  {/* Люди — то, из-за чего задача уезжает не тому, поэтому
                      видно их прямо в списке, а не только внутри карточки. */}
                  {(p.people ?? []).length > 0 && (
                    <span className="text-[13px] text-[var(--muted-foreground)]/80">
                      {t("кто участвует:")} {(p.people ?? []).join(", ")}
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
            {t("Откуда steno узнаёт о созвонах и куда присылает итог. Открой любой, чтобы включить\n            или поменять — что там настраивать, канал расскажет сам.")}
          </p>
          <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
            {io.map((c) => (
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
                      {c.enabled ? t("вкл") : t("выкл")}
                    </Badge>
                    {/* Теми же словами, что и в самой модалке канала: «вход» и
                        «выход» короче, но требуют догадаться, чей это вход. */}
                    <span className="text-[13px] text-[var(--muted-foreground)]/70">
                      {c.in && c.out
                        ? t("приносит созвоны и уносит follow-up")
                        : c.in
                          ? t("приносит созвоны")
                          : t("уносит follow-up")}
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


      {tab === "brain" && (
        <section>
          <p className="mb-3 max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
            {t("Кто читает расшифровку и достаёт из неё задачи, решения и вопросы. Подписка, которая\n            уже есть, ключ провайдера или модель на этой же машине — ключи задаёт `steno setup`,\n            здесь их нет.")}
          </p>
          <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
            {brain.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={() => setChannel(c)}
                className="flex w-full items-start gap-4 px-4 py-3.5 text-left transition-colors hover:bg-[var(--muted)]/50 sm:px-5"
              >
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span>{c.name}</span>
                    {/* Что выбрано — прямо в строке: за этим сюда и заходят. */}
                    {c.summary && (
                      <span className="text-[13px] text-[var(--muted-foreground)]/70">
                        {c.summary}
                      </span>
                    )}
                  </span>
                  <span className="mt-1 block max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
                    {c.about}
                  </span>
                </span>
              </button>
            ))}
          </div>
        </section>
      )}

      {tab === "agent" && agent && <AgentTab agent={agent} />}

      <ProjectDialog
        open={projectOpen}
        onClose={() => setProjectOpen(false)}
        project={editing}
      />
      <ChannelDialog open={channel !== null} onClose={() => setChannel(null)} channel={channel} />
    </>
  );
}

// Раздел «Агент» — состояние, а не форма. Выключатель исполнения нарочно не
// здесь: пароль панели общий на команду, а право писать файлы на машине с
// сервисом даёт тот, кто за ней сидит. Панель говорит, что включено, кто
// исполняет и где это меняется, — и этого достаточно, чтобы не искать.
function AgentTab({ agent }: { agent: AgentState }) {
  const cmd = (s: string) => <code className="whitespace-nowrap rounded bg-[var(--muted)] px-1.5 py-0.5 text-[13px]">{s}</code>;
  const rows: { label: string; value: React.ReactNode; note?: React.ReactNode }[] = [
    {
      label: t("Исполнение"),
      value: agent.enabled ? (
        <Badge variant="success">{t("включено")}</Badge>
      ) : (
        <Badge>{t("выключено")}</Badge>
      ),
      note: agent.enabled ? (
        t("steno может завести рабочую копию, ветку и коммит на машине, где стоит сервис. Push не делает никогда.")
      ) : (
        <>
          {t("ТЗ собираются, ветки не заводятся. Включается там, где стоит сервис:")} {cmd("steno agent on")}{" "}
          {t("в терминале или переключатель в строке меню.")}
        </>
      ),
    },
    {
      label: t("ТЗ после разбора"),
      value: agent.autoSpec ? (
        <Badge variant="success">{t("собираются сами")}</Badge>
      ) : (
        <Badge>{t("по запросу")}</Badge>
      ),
      note: agent.autoSpec ? (
        t("Каждая задача с проектом получает ТЗ сразу после созвона — чтение репозитория и один запрос к модели на задачу.")
      ) : (
        <>
          {t("ТЗ пишется по кнопке у задачи в проекте. Само после каждого созвона —")} {cmd("steno agent auto on")}.
        </>
      ),
    },
    {
      label: t("Исполнитель"),
      value: agent.executor ? (
        <span className="inline-flex flex-wrap items-center gap-2">
          <span>{agent.executor}</span>
          {agent.probed && (
            <Badge variant={agent.ready ? "success" : "warning"}>
              {agent.ready ? t("готов") : t("не готов")}
            </Badge>
          )}
        </span>
      ) : (
        <Badge variant="warning">{t("не выбран")}</Badge>
      ),
      note: agent.why || t("Тот же, кем платят за разбор: подписка Claude Code или Codex. Меняется в разделе «Разбор» или в agent.provider."),
    },
    {
      label: t("Ветки"),
      value: <code className="rounded bg-[var(--muted)] px-1.5 py-0.5 text-[13px]">{agent.branchPrefix}…</code>,
      note: t("Всё, что начинается с этой приставки, сделано машиной — это видно в списке веток без пояснений."),
    },
  ];
  return (
    <section>
      <p className="mb-3 max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">
        {t("Задача с созвона может стать ТЗ по репозиторию проекта, а ТЗ — веткой: агент работает в отдельной рабочей копии и возвращает ветку, которую надо посмотреть глазами.")}
      </p>
      <div className="divide-y divide-[var(--border)] overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--card)]">
        {rows.map((r) => (
          <div key={r.label} className="flex flex-col gap-1 px-4 py-3.5 sm:px-5">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="min-w-36">{r.label}</span>
              <span className="text-sm">{r.value}</span>
            </div>
            {r.note && (
              <span className="max-w-prose text-sm leading-relaxed text-[var(--muted-foreground)]">{r.note}</span>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}
