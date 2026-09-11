import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { SpecRow } from "@/lib/api";
import { plural } from "@/lib/fmt";
import { t } from "@/lib/i18n";

// Отметка о ТЗ у задачи: есть ли оно, что с ним и что можно нажать.
//
// Одна на список задач в проекте и на шапку самого ТЗ, чтобы слово о
// состоянии было одно и то же в обоих местах. Состояний шесть, и у каждого
// свой цвет не для красоты: «нельзя запускать» и «идёт работа» — это оба
// «жёлтый», но по разным причинам, и подпись это объясняет.

export type SpecTone = "default" | "success" | "warning" | "destructive";

/** Словами и цветом: что с ТЗ. */
export function specState(sp: SpecRow): { label: string; tone: SpecTone; hint?: string } {
  switch (sp.status) {
    case "rejected":
      return { label: t("не про код"), tone: "default", hint: sp.reject };
    case "running":
      return { label: t("агент работает"), tone: "warning", hint: sp.branch };
    case "done":
      return { label: t("есть ветка"), tone: "success", hint: sp.branch };
    case "failed":
      return { label: t("агент сорвался"), tone: "destructive", hint: sp.run_error };
  }
  if (sp.blocked && sp.blocked.length > 0) {
    return { label: t("ТЗ: нельзя запускать"), tone: "warning", hint: sp.blocked.join("\n") };
  }
  return {
    label: `${t("ТЗ")} · ${plural(sp.unknowns, t("вопрос"), t("вопроса"), t("вопросов"))}`,
    tone: "success",
  };
}

export function SpecMark({
  spec,
  building,
  failed,
  onBuild,
  busy,
  readOnly,
}: {
  spec?: SpecRow;
  building: boolean;
  failed?: string;
  onBuild: () => void;
  busy?: boolean;
  /** Только показать: у закрытой задачи новое ТЗ не собирают. */
  readOnly?: boolean;
}) {
  const again = (
    <Button
      variant="link"
      size="sm"
      className="h-auto p-0 text-[13px] text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
      onClick={onBuild}
      disabled={busy}
    >
      {t("собрать заново")}
    </Button>
  );

  if (building) {
    return (
      <span className="inline-flex items-center gap-2">
        <Badge variant="warning" className="shrink-0">
          {t("собираю ТЗ…")}
        </Badge>
      </span>
    );
  }
  if (failed) {
    return (
      <span className="inline-flex items-center gap-2">
        <Badge variant="destructive" className="shrink-0" title={failed}>
          {t("ТЗ не собралось")}
        </Badge>
        {again}
      </span>
    );
  }
  if (!spec) {
    return (
      <Button
        variant="link"
        size="sm"
        className="h-auto p-0 text-[13px] text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
        onClick={onBuild}
        disabled={busy}
        title={t("написать ТЗ по репозиторию проекта — чтение кода и один запрос к модели")}
      >
        {t("ТЗ")}
      </Button>
    );
  }
  const st = specState(spec);
  return (
    <span className="inline-flex items-center gap-2">
      <Link to={`/s/${spec.id}`} title={st.hint} className="shrink-0">
        <Badge variant={st.tone} className="cursor-pointer hover:opacity-80">
          {st.label}
        </Badge>
      </Link>
      {!readOnly && (spec.status === "rejected" || spec.status === "failed") && again}
    </span>
  );
}
