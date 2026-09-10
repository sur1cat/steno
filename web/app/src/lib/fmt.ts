import { isRU, locale, t } from "@/lib/i18n";
// Как человек читает дату, длительность и число. Раньше это жило функциями
// шаблонов в panel_view.go; теперь страницы рисует браузер, и правила переехали
// сюда — но остались теми же, чтобы текст в панели не разъехался с текстом,
// который сервис шлёт в Telegram и Slack.

// Месяц и день просим у браузера, а не верстаем массивом: по-русски это
// «8 сентября», по-английски «September 8», и порядок слов тут — часть языка,
// а не оформления.
function monthDay(d: Date) {
  return d.toLocaleDateString(locale, { day: "numeric", month: "long" });
}

function monthDayYear(d: Date) {
  return d.toLocaleDateString(locale, { day: "numeric", month: "long", year: "numeric" });
}

function weekdayOf(d: Date) {
  return d.toLocaleDateString(locale, { weekday: "long" });
}

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
  if (sameDay(d, now)) return `${t("сегодня")}, ${hhmm(d)}`;
  if (sameDay(d, yesterday)) return `${t("вчера")}, ${hhmm(d)}`;
  if (d.getFullYear() === now.getFullYear()) {
    return `${monthDay(d)}, ${hhmm(d)}`;
  }
  return `${monthDayYear(d)}, ${hhmm(d)}`;
}

/** «сегодня» / «завтра» / «11 сентября, четверг» — заголовок дня в расписании. */
export function dayRu(unix: number): string {
  const d = new Date(unix * 1000);
  const now = new Date();
  const tomorrow = new Date(now.getTime() + 24 * 3600 * 1000);
  const weekday = weekdayOf(d);
  if (sameDay(d, now)) return `${t("сегодня")}, ${weekday}`;
  if (sameDay(d, tomorrow)) return `${t("завтра")}, ${weekday}`;
  return `${monthDay(d)}, ${weekday}`;
}

/** «сегодня, вторник» / «вчера» / «6 сентября, суббота» — заголовок дня в
 *  списке того, что уже прошло. Отдельно от dayRu: там впереди «завтра»,
 *  которого в архиве быть не может, и там же нет года — а созвон
 *  позапрошлогодний от сегодняшнего должен отличаться на глаз. */
export function dayHeadRu(unix: number): string {
  const d = new Date(unix * 1000);
  const now = new Date();
  const yesterday = new Date(now.getTime() - 24 * 3600 * 1000);
  const weekday = weekdayOf(d);
  if (sameDay(d, now)) return `${t("сегодня")}, ${weekday}`;
  if (sameDay(d, yesterday)) return `${t("вчера")}, ${weekday}`;
  if (d.getFullYear() === now.getFullYear()) {
    return `${monthDay(d)}, ${weekday}`;
  }
  return monthDayYear(d);
}

/** Разбивка по дням: ключ группы. Календарный день, не сутки от «сейчас». */
export function dayKey(unix: number): string {
  return new Date(unix * 1000).toDateString();
}

export function timeRu(unix: number): string {
  return hhmm(new Date(unix * 1000));
}

export function durRu(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return "";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return h > 0 ? `${h}${t("ч")} ${m}${t("мин")}` : `${m}${t("мин")}`;
}

/** Слово, согласованное с числом: «задача», «задачи», «задач». Отдельно от
 *  plural — карточке проекта нужно крупное число само по себе, а подпись под
 *  ним отдельной строкой. */
export function pluralWord(n: number, one: string, few: string, many: string): string {
  // По-английски форм две, и «few» здесь работает обычным множественным:
  // t("задачи") — это "tasks". Отдельной таблицы для этого не нужно.
  if (!isRU) return n === 1 ? one : few;
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

/** «1 задача», «2 задачи», «5 задач». Без согласования интерфейс выглядит
 *  машинным переводом, а этот текст видно на каждой странице. */
export function plural(n: number, one: string, few: string, many: string): string {
  return `${n} ${pluralWord(n, one, few, many)}`;
}

/** Таймкод в записи: 00:12:34. */
export function clock(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(Math.floor(s / 3600))}:${p(Math.floor((s % 3600) / 60))}:${p(s % 60)}`;
}

export function dueRu(due: string): string {
  if (!due.trim()) return t("срок не назван");
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(due.trim());
  if (!m) return due;
  return monthDay(new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
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
    uploading: t("разбираю файл"),
    recording: t("идёт запись"),
    recorded: t("записан"),
    transcribed: t("расшифрован"),
    summarized: t("есть follow-up"),
    published: t("разослан"),
    publish_failed: t("не разослан"),
    failed: t("сорвался"),
  };
  return map[s] ?? s;
}

/** Незаконченное или сорвавшееся подсвечиваем: обрезанная запись снаружи
 *  выглядит как нормальная, и это надо видеть. */
export function statusAlarm(s: string): boolean {
  return s === "failed" || s === "publish_failed" || s === "recording" || s === "uploading";
}

/** Состояния, которые кончатся сами. Список созвонов, пока такие есть,
 *  перечитывается сам: загрузили запись — и человек ждёт не обновления
 *  страницы, а того, что «разбираю файл» сменится на «расшифрован». */
export function statusPending(s: string): boolean {
  return s === "uploading" || s === "recording";
}

/** Ключи публикаций хранятся идентификаторами, показывать их человеку в таком
 *  виде незачем. */
export function targetRu(t: string): string {
  if (t.startsWith("google_docs")) return "Google Docs";
  if (t.startsWith("slack")) return "Slack";
  if (t.startsWith("telegram")) return "Telegram";
  return t;
}
