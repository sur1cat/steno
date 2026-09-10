import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, type SettingsProject, type Source } from "@/lib/api";
import { dateRu } from "@/lib/fmt";
import { Button } from "@/components/ui/button";
import { ChipsInput, submittedFromChips } from "@/components/ui/chips-input";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { SourcesEditor } from "@/components/sources-editor";
import { t } from "@/lib/i18n";

// Проект правится в модалке, а не на отдельной странице: заводят их подряд по
// три-четыре, и каждый раз уходить со списка и возвращаться обратно — лишняя
// дорога на пустом месте.
export function ProjectDialog({
  open,
  onClose,
  project,
}: {
  open: boolean;
  onClose: () => void;
  /** null — новый проект. */
  project: SettingsProject | null;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [aliases, setAliases] = useState<string[]>([]);
  const [about, setAbout] = useState("");
  const [sources, setSources] = useState<Source[]>([]);
  const [people, setPeople] = useState<string[]>([]);
  const [vocabulary, setVocabulary] = useState<string[]>([]);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    if (!open) return;
    setName(project?.name ?? "");
    setAliases(project?.aliases ?? []);
    setAbout(project?.about ?? "");
    setSources(project?.sources ?? []);
    setPeople(project?.people ?? []);
    setVocabulary(project?.vocabulary ?? []);
    setConfirmDelete(false);
  }, [open, project]);

  const done = (msg: string) => {
    qc.invalidateQueries({ queryKey: ["settings"] });
    qc.invalidateQueries({ queryKey: ["projects"] });
    toast.success(msg);
  };
  const failed = (e: unknown) => toast.error(e instanceof Error ? e.message : t("не получилось"));

  const save = useMutation({
    mutationFn: () =>
      api.saveProject({
        oldName: project?.name,
        name: name.trim(),
        about: about.trim(),
        aliases,
        // Пустые строки не сохраняем: человек добавил источник и передумал —
        // это не повод получить ошибку «у источника пустое значение».
        sources: sources.filter((s) => s.value.trim() !== ""),
        people,
        vocabulary,
      }),
    onSuccess: () => {
      done(t("Проект сохранён"));
      onClose();
    },
    onError: failed,
  });

  const remove = useMutation({
    mutationFn: () => api.deleteProject(project!.name),
    onSuccess: () => {
      done(t("Проект удалён"));
      onClose();
    },
    onError: failed,
  });

  const rebuild = useMutation({
    mutationFn: () => api.buildContext(project!.name),
    onSuccess: () =>
      toast(t("Собираю справку — это поход в Claude на несколько секунд, обнови страницу позже")),
    onError: failed,
  });

  return (
    <>
      <Dialog open={open} onClose={onClose} className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{project ? project.name : t("Новый проект")}</DialogTitle>
          <DialogDescription>
            {t("Чем подробнее описан проект, тем точнее раскладываются по нему решения и задачи.")}
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            // Enter в «как называют вслух» уже добавил название — сохранять по
            // нему рано: модалка закрылась бы посреди набора списка.
            if (submittedFromChips()) return;
            if (name.trim()) save.mutate();
          }}
        >
          <Input
            label={t("Название")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("Платежи")}
            autoComplete="off"
            required
          />

          <div>
            <label
              htmlFor="project-aliases"
              className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]"
            >
              {t("Как называют вслух")}
            </label>
            {/* Не строка через запятую: набранное имя становится отдельным
                значением, и видно, что именно в списке лежит. */}
            <ChipsInput
              id="project-aliases"
              value={aliases}
              onChange={setAliases}
              placeholder={t("биллинг")}
              addLabel={t("Добавить название")}
            />
            <p className="mt-1.5 text-xs leading-relaxed text-[var(--muted-foreground)]">
              {t("По этому списку «биллинг» превращается в «Платежи» — точным совпадением, а не\n              догадкой. Добавляй по одному: Enter или плюс.")}
            </p>
          </div>

          <div>
            <label className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]">
              {t("О чём проект")}
            </label>
            <textarea
              value={about}
              onChange={(e) => setAbout(e.target.value)}
              rows={3}
              placeholder={t("Приём денег, подписки, вебхуки провайдеров")}
              className="w-full rounded-xl border-0 bg-[var(--muted)] px-4 py-2.5 text-sm placeholder:text-[var(--muted-foreground)]/60 focus:outline-none focus:ring-2 focus:ring-primary/30"
            />
          </div>

          {/* Имена людей и слова команды — то единственное, чего нет ни в
              одном источнике. В git человек подписан логином, в календаре —
              тем, что он однажды вписал в аккаунт, а на созвоне его зовут по
              имени; у половины проектов кода нет вовсе. */}
          <div>
            <label
              htmlFor="project-people"
              className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]"
            >
              {t("Кто участвует")}
            </label>
            <ChipsInput
              id="project-people"
              value={people}
              onChange={setPeople}
              placeholder={t("Орынгали")}
              addLabel={t("Добавить человека")}
            />
            <p className="mt-1.5 text-xs leading-relaxed text-[var(--muted-foreground)]">
              {t("Именами, которыми людей зовут на созвоне, а не подписью в git. По ним задача уходит\n              тому, кого назвали вслух, — и по ним же распознавание не превращает имя в похожее\n              обычное слово.")}
            </p>
          </div>

          <div>
            <label
              htmlFor="project-vocabulary"
              className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]"
            >
              {t("Сервисы и сокращения")}
            </label>
            <ChipsInput
              id="project-vocabulary"
              value={vocabulary}
              onChange={setVocabulary}
              placeholder={t("Сапар")}
              addLabel={t("Добавить слово")}
            />
            <p className="mt-1.5 text-xs leading-relaxed text-[var(--muted-foreground)]">
              {t("Всё, что звучит вслух и на имя не похоже: названия систем, чужие сервисы, сокращения.\n              Отдельно от людей, чтобы «Сапар» не стал исполнителем задачи.")}
            </p>
          </div>

          <div>
            <label className="mb-1.5 block text-sm font-light text-[var(--muted-foreground)]">
              {t("Источники")}
            </label>
            <SourcesEditor value={sources} onChange={setSources} />
            <p className="mt-2 text-xs leading-relaxed text-[var(--muted-foreground)]">
              {t("Из них собирается справка: README, состав, манифесты и темы последних коммитов —\n              там и живёт словарь, которым команда говорит о проекте.")}
            </p>
          </div>

          {project && (
            <div className="rounded-xl bg-[var(--muted)]/60 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="text-sm">
                  {project.primerChars > 0 ? (
                    <>
                      {t("Справка на")} {project.primerChars} {t("символов, собрана")} {dateRu(project.builtAt)}
                      <div className="text-xs text-[var(--muted-foreground)]">
                        {t("Уходит в промпт на каждом созвоне.")}
                      </div>
                    </>
                  ) : (
                    <>
                      {t("Справки нет")}
                      <div className="text-xs text-[var(--muted-foreground)]">
                        {t("Она объясняет модели, какими словами команда говорит об этом проекте.")}
                      </div>
                    </>
                  )}
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  isLoading={rebuild.isPending}
                  onClick={() => rebuild.mutate()}
                >
                  {project.primerChars > 0 ? t("Пересобрать справку") : t("Собрать справку")}
                </Button>
              </div>
            </div>
          )}

          <DialogFooter className="justify-between">
            {project ? (
              <Button
                type="button"
                variant="ghost"
                className="text-[var(--destructive)]"
                onClick={() => setConfirmDelete(true)}
              >
                {t("Удалить проект")}
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-3">
              <Button type="button" variant="outline" onClick={onClose}>
                {t("Отмена")}
              </Button>
              <Button type="submit" variant="primary" isLoading={save.isPending} disabled={!name.trim()}>
                {t("Сохранить")}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </Dialog>

      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => remove.mutate()}
        isPending={remove.isPending}
        title={`${t("Удалить")} “${project?.name}”?`}
        description={t("Уберётся описание проекта. Накопленные задачи и решения останутся — это история, и терять её из-за переименования нельзя.")}
      />
    </>
  );
}
