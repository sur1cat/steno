// Как человек читает дату, длительность и число. Раньше это жило функциями
// шаблонов в panel_view.go; теперь страницы рисует браузер, и правила переехали
// сюда — но остались теми же, чтобы текст в панели не разъехался с текстом,
// который сервис шлёт в Telegram и Slack.

const MONTHS = [
  "января", "февраля", "марта", "апреля", "мая", "июня",
  "июля", "августа", "сентября", "октября", "ноября", "декабря",
];

function sameDay(a: Date, b: Date) {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

function hhmm(d: Date) {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** «8 сентября, 11:00». Сегодняшнее и вчерашнее называем словом — в списке
 *  созвонов это самые частые строки, и дата в них ничего не добавляет. */
export function dateRu(unix: number | null | undefined): string {
  if (!unix || unix <= 0) return "—";
  const d = new Date(unix * 1000);
  const now = new Date();
  const yesterday = new Date(now.getTime() - 24 * 3600 * 1000);
  if (sameDay(d, now)) return `сегодня, ${hhmm(d)}`;
  if (sameDay(d, yesterday)) return `вчера, ${hhmm(d)}`;
  if (d.getFullYear() === now.getFullYear()) {
    return `${d.getDate()} ${MONTHS[d.getMonth()]}, ${hhmm(d)}`;
  }
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}, ${hhmm(d)}`;
}

/** «сегодня» / «завтра» / «11 сентября, четверг» — заголовок дня в расписании. */
export function dayRu(unix: number): string {
  const d = new Date(unix * 1000);
  const now = new Date();
  const tomorrow = new Date(now.getTime() + 24 * 3600 * 1000);
  const weekday = d.toLocaleDateString("ru-RU", { weekday: "long" });
  if (sameDay(d, now)) return `сегодня, ${weekday}`;
  if (sameDay(d, tomorrow)) return `завтра, ${weekday}`;
  return `${d.getDate()} ${MONTHS[d.getMonth()]}, ${weekday}`;
}

export function timeRu(unix: number): string {
  return hhmm(new Date(unix * 1000));
}

export function durRu(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return "";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return h > 0 ? `${h} ч ${m} мин` : `${m} мин`;
}

/** «1 задача», «2 задачи», «5 задач». Без согласования интерфейс выглядит
 *  машинным переводом, а этот текст видно на каждой странице. */
export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10;
  const m100 = n % 100;
  let w = many;
  if (m10 === 1 && m100 !== 11) w = one;
  else if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) w = few;
  return `${n} ${w}`;
}

/** Таймкод в записи: 00:12:34. */
export function clock(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(Math.floor(s / 3600))}:${p(Math.floor((s % 3600) / 60))}:${p(s % 60)}`;
}

export function dueRu(due: string): string {
  if (!due.trim()) return "срок не назван";
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(due.trim());
  if (!m) return due;
  return `${Number(m[3])} ${MONTHS[Number(m[2]) - 1]}`;
}

/** Просрочка считается по местной полуночи: задача на 8-е краснеет девятого
 *  утром там, где сидит человек, а не там, где стоит сервер. */
export function overdue(due: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec((due ?? "").trim());
  if (!m) return false;
  const at = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  const now = new Date();
  return at < new Date(now.getFullYear(), now.getMonth(), now.getDate());
}

export function statusRu(s: string): string {
  const map: Record<string, string> = {
    recording: "идёт запись",
    recorded: "записан",
    transcribed: "расшифрован",
    summarized: "есть follow-up",
    published: "разослан",
    publish_failed: "не разослан",
    failed: "сорвался",
  };
  return map[s] ?? s;
}

/** Незаконченное или сорвавшееся подсвечиваем: обрезанная запись снаружи
 *  выглядит как нормальная, и это надо видеть. */
export function statusAlarm(s: string): boolean {
  return s === "failed" || s === "publish_failed" || s === "recording";
}

/** Ключи публикаций хранятся идентификаторами, показывать их человеку в таком
 *  виде незачем. */
export function targetRu(t: string): string {
  if (t.startsWith("google_docs")) return "Google Docs";
  if (t.startsWith("slack")) return "Slack";
  if (t.startsWith("telegram")) return "Telegram";
  return t;
}
