import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Document number for display: prefer the per-park inventory code
 * (display_number, e.g. "3-00007"), falling back to the legacy global integer
 * `number` for rows created before the park-numbering migration.
 */
export function formatDocNumber(row: { display_number?: string | null; number?: number | null }): string {
  if (row.display_number) return row.display_number;
  return row.number ? String(row.number) : "";
}

// ─── Number formatting (Kazakhstan locale) ───

/** Format byte count: 1.5 MB / 12 KB. */
export function fmtBytes(n: number | null | undefined): string {
  if (n == null || isNaN(n)) return "—";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

/** Format number with space separators: 1 234 567.00 */
/**
 * Coerce a form number-field value to `number | null`. A blank/null/undefined
 * (or non-numeric) input becomes `null` — never 0. Use this for optional number
 * filters: `z.coerce.number()` turns "" into 0, which silently narrows the query
 * instead of dropping the filter. `getValues()` also yields a raw "".
 */
export function toNullableNumber(v: unknown): number | null {
  if (v === "" || v === null || v === undefined) return null;
  const n = Number(v);
  return Number.isNaN(n) ? null : n;
}

export function fmtNumber(n: number | null | undefined, decimals = 0): string {
  if (n === null || n === undefined) return "0";
  return n.toLocaleString("ru-RU", {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  });
}

/** Format money: 1 234 567 ₸ */
export function fmtMoney(n: number | null | undefined, decimals = 0): string {
  return fmtNumber(n, decimals) + " ₸";
}

/**
 * Складское количество: до трёх знаков после запятой, хвостовые нули убраны
 * (1 → «1», 2.5 → «2,5», 0.001 → «0,001»).
 *
 * fmtNumber(n) здесь не годится: он округляет до целого, а для остатка это
 * ложь в опасную сторону — 2,5 л при потребности 3 показались бы как «3»,
 * то есть «хватает», и документ всё равно не провёлся бы.
 */
export function fmtQty(n: number | null | undefined): string {
  if (n === null || n === undefined) return "0";
  return n.toLocaleString("ru-RU", { minimumFractionDigits: 0, maximumFractionDigits: 3 });
}

/**
 * Phone value safe to prefill into an editable input. Park-scoped users see
 * masked phones ("+7 7** *** ** 12") — a masked value must never leak into a
 * form field where it could be saved back verbatim.
 */
export function editablePhone(phone: string | null | undefined): string {
  const p = phone || "";
  return p.includes("*") ? "" : p;
}

/**
 * Share of `count` in `total` as whole percent: 85%. Non-zero shares that
 * round to zero show as "<1%" so a real count never reads as 0%.
 */
export function fmtPercent(count: number, total: number): string {
  if (total <= 0) return "0%";
  const pct = (count / total) * 100;
  if (pct > 0 && pct < 1) return "<1%";
  return `${Math.round(pct)}%`;
}

// ─── Date formatting (Kazakhstan: DD.MM.YYYY, UTC+5/+6) ───

/** ISO date (YYYY-MM-DD) for today — for default values on `<input type="date">`. */
export function todayIso(): string {
  return new Date().toISOString().slice(0, 10);
}

/** ISO date N months from today — used to seed default contract end-dates. */
export function todayPlusMonthsIso(months: number): string {
  const d = new Date();
  d.setMonth(d.getMonth() + months);
  return d.toISOString().slice(0, 10);
}

/** Format date as DD.MM.YYYY */
export function fmtDate(dateStr: string | null | undefined): string {
  if (!dateStr) return "—";
  return new Date(dateStr).toLocaleDateString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
  });
}

/**
 * Whole days from today until the given date: positive = in the future,
 * negative = already past, 0 = today, null when no/invalid date. Compared in
 * UTC day-granularity on both ends — TZ-independent and consistent with the
 * backend's UTC CURRENT_DATE that drives osago_status. Used only for the OSAGO
 * tooltip wording ("истекает через N дней" / "просрочено N дней назад"); the
 * red/amber state itself comes from the server.
 */
export function daysUntil(dateStr: string | null | undefined): number | null {
  if (!dateStr) return null;
  const target = new Date(dateStr);
  if (isNaN(target.getTime())) return null;
  const MS_PER_DAY = 24 * 60 * 60 * 1000;
  const dayUTC = (d: Date) => Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
  return Math.round((dayUTC(target) - dayUTC(new Date())) / MS_PER_DAY);
}

/** Format datetime as DD.MM.YYYY HH:mm */
export function fmtDateTime(dateStr: string | null | undefined): string {
  if (!dateStr) return "—";
  return new Date(dateStr).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * Contract event line shown under the rental period: the real datetime of the
 * last lifecycle event. Terminated contracts show their termination time;
 * everything else shows the creation time. (The period itself is date-only.)
 */
export function contractEventLine(
  c: { terminated_at?: string | null; created_at: string },
  t: (key: string) => string,
): string {
  return c.terminated_at
    ? `${t("rental.terminatedAt")}: ${fmtDateTime(c.terminated_at)}`
    : `${t("rental.createdAt")}: ${fmtDateTime(c.created_at)}`;
}

/** Format time-of-day as HH:mm (used in print templates for «время создания»). */
export function fmtTime(dateStr: string | null | undefined): string {
  if (!dateStr) return "—";
  return new Date(dateStr).toLocaleTimeString("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

// Genitive month names for the «дата прописью» print tag (e.g. «29 мая 2026 г.»).
const MONTHS_GENITIVE = [
  "января", "февраля", "марта", "апреля", "мая", "июня",
  "июля", "августа", "сентября", "октября", "ноября", "декабря",
];

/** Format date in words: «29 мая 2026 г.» — for contract print templates. */
export function fmtDateWords(dateStr: string | null | undefined): string {
  if (!dateStr) return "—";
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "—";
  return `${d.getDate()} ${MONTHS_GENITIVE[d.getMonth()]} ${d.getFullYear()} г.`;
}

/** Buyout-contract date header style: «5» мая 2026 года (digit day in
 * guillemets, genitive month, full year). Empty string when missing so the
 * print tag renders blank rather than a dash. */
export function fmtDateContract(dateStr: string | null | undefined): string {
  if (!dateStr) return "";
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "";
  return `«${d.getDate()}» ${MONTHS_GENITIVE[d.getMonth()]} ${d.getFullYear()} года`;
}

// ─── Number → Russian words (for amount-in-words print tags «прописью») ───
const WORDS_ONES = [
  "", "один", "два", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять",
  "десять", "одиннадцать", "двенадцать", "тринадцать", "четырнадцать", "пятнадцать",
  "шестнадцать", "семнадцать", "восемнадцать", "девятнадцать",
];
const WORDS_TENS = [
  "", "", "двадцать", "тридцать", "сорок", "пятьдесят",
  "шестьдесят", "семьдесят", "восемьдесят", "девяносто",
];
const WORDS_HUNDREDS = [
  "", "сто", "двести", "триста", "четыреста", "пятьсот",
  "шестьсот", "семьсот", "восемьсот", "девятьсот",
];
// [one, few, many, feminine] per 1000^i scale: единицы, тысячи, миллионы, миллиарды.
const WORDS_SCALES: [string, string, string, boolean][] = [
  ["", "", "", false],
  ["тысяча", "тысячи", "тысяч", true],
  ["миллион", "миллиона", "миллионов", false],
  ["миллиард", "миллиарда", "миллиардов", false],
];

function pluralRu(n: number, forms: [string, string, string]): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return forms[0];
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return forms[1];
  return forms[2];
}

function tripletToWords(n: number, feminine: boolean): string {
  const out: string[] = [];
  const h = Math.floor(n / 100);
  const rem = n % 100;
  if (h) out.push(WORDS_HUNDREDS[h]);
  const unit = (u: number) =>
    feminine && u === 1 ? "одна" : feminine && u === 2 ? "две" : WORDS_ONES[u];
  if (rem < 20) {
    if (rem) out.push(unit(rem));
  } else {
    out.push(WORDS_TENS[Math.floor(rem / 10)]);
    const u = rem % 10;
    if (u) out.push(unit(u));
  }
  return out.join(" ");
}

/** Integer to Russian words, e.g. 14400000 → "четырнадцать миллионов
 * четыреста тысяч". No currency noun — the template supplies «тенге». Empty
 * string for null/0 so an unset amount prints blank. */
export function numToWordsRu(value: number | null | undefined): string {
  if (value == null || isNaN(value)) return "";
  let n = Math.floor(Math.abs(value));
  if (n === 0) return "";
  const triplets: number[] = [];
  while (n > 0) {
    triplets.push(n % 1000);
    n = Math.floor(n / 1000);
  }
  const parts: string[] = [];
  for (let i = triplets.length - 1; i >= 0; i--) {
    const t = triplets[i];
    if (t === 0) continue;
    const [one, few, many, feminine] = WORDS_SCALES[i];
    parts.push(tripletToWords(t, feminine));
    if (i > 0) parts.push(pluralRu(t, [one, few, many]));
  }
  return parts.join(" ");
}

// ─── Number → Kazakh words (казахский блок договоров: «он алты мың») ───
// Казахские числительные не согласуются ни в роде, ни в числе, поэтому здесь
// нет таблицы падежных форм: разряд подписывается одним и тем же словом.
const WORDS_KK_ONES = [
  "", "бір", "екі", "үш", "төрт", "бес", "алты", "жеті", "сегіз", "тоғыз",
];
const WORDS_KK_TENS = [
  "", "он", "жиырма", "отыз", "қырық", "елу",
  "алпыс", "жетпіс", "сексен", "тоқсан",
];
const WORDS_KK_SCALES = ["", "мың", "миллион", "миллиард"];

function tripletToWordsKk(n: number): string {
  const out: string[] = [];
  const h = Math.floor(n / 100);
  // «жүз», а не «бір жүз» — единица перед сотней не произносится.
  if (h > 1) out.push(WORDS_KK_ONES[h]);
  if (h) out.push("жүз");
  const rem = n % 100;
  const t = Math.floor(rem / 10);
  if (t) out.push(WORDS_KK_TENS[t]);
  const u = rem % 10;
  if (u) out.push(WORDS_KK_ONES[u]);
  return out.join(" ");
}

/** Integer to Kazakh words, e.g. 16000 → «он алты мың». Единица опускается
 * перед «жүз» и «мың» (но не перед «миллион») — так написаны и сами договоры
 * заказчика: «елу мың», «екі жүз мың». Валюты нет: «теңге» даёт шаблон.
 * Пустая строка для null/0, чтобы незаполненная сумма печаталась пустотой. */
export function numToWordsKk(value: number | null | undefined): string {
  if (value == null || isNaN(value)) return "";
  let n = Math.floor(Math.abs(value));
  if (n === 0) return "";
  const triplets: number[] = [];
  while (n > 0) {
    triplets.push(n % 1000);
    n = Math.floor(n / 1000);
  }
  const parts: string[] = [];
  for (let i = triplets.length - 1; i >= 0; i--) {
    const t = triplets[i];
    if (t === 0) continue;
    if (i !== 1 || t !== 1) parts.push(tripletToWordsKk(t));
    if (i > 0) parts.push(WORDS_KK_SCALES[i]);
  }
  return parts.join(" ");
}

// ─── GPS sanity guards ───
// Defense-in-depth: backend already clamps outliers (service/gps_outlier.go +
// CHECK constraint in migration 000047). These helpers prevent stale corrupt
// rows from rendering as "500 км/ч" if the BE guard is ever bypassed.

/** Realistic speed ceiling, mirrors backend MaxRealisticSpeedKmh. */
export const MAX_REALISTIC_SPEED_KMH = 300;
/** Realistic odometer ceiling — anything above this is a Wialon glitch. */
export const MAX_REALISTIC_MILEAGE_KM = 9_999_999;

/** Returns the rounded speed, or null when the value is missing/out-of-range. */
export function safeSpeedKmh(speed: number | null | undefined): number | null {
  if (speed == null || isNaN(speed)) return null;
  if (speed < 0 || speed > MAX_REALISTIC_SPEED_KMH) return null;
  return Math.round(speed);
}

/** Returns the mileage, or null when the value is missing/out-of-range. */
export function safeMileageKm(mileage: number | null | undefined): number | null {
  if (mileage == null || isNaN(mileage)) return null;
  if (mileage < 0 || mileage > MAX_REALISTIC_MILEAGE_KM) return null;
  return mileage;
}

/** Odometer km for print/display: space-grouped, blank when missing, zero
 * (no reading yet), or a Wialon glitch value (guarded via safeMileageKm). */
export function fmtMileage(km: number | null | undefined): string {
  const v = safeMileageKm(km);
  return v ? fmtNumber(v) : "";
}

/** Format a lat/lng coordinate. 5 decimals ≈ 1.1 m precision in КЗ — plenty
 * for fleet display, and standardised across the GPS/geofence pages. */
export function fmtCoord(value: number | null | undefined, decimals = 5): string {
  if (value == null || isNaN(value)) return "—";
  return value.toFixed(decimals);
}

/**
 * Format a duration in seconds as "45 мин" or "1 ч 23 мин". Unit labels are
 * passed in so the caller supplies the localized "ч"/"мин" (keeps utils i18n-free).
 */
export function fmtDuration(seconds: number, hoursLabel: string, minutesLabel: string): string {
  // Round to whole minutes first, then split — so a remainder that rounds up to
  // 60 carries into the hour instead of rendering "60 мин" / "1 ч 60 мин".
  const totalMin = Math.round(Math.max(0, seconds) / 60);
  const h = Math.floor(totalMin / 60);
  const m = totalMin % 60;
  if (h > 0) {
    return m > 0 ? `${h} ${hoursLabel} ${m} ${minutesLabel}` : `${h} ${hoursLabel}`;
  }
  return `${m} ${minutesLabel}`;
}

/** Format short date as DD.MM.YY HH:mm */
export function fmtDateTimeShort(dateStr: string | null | undefined): string {
  if (!dateStr) return "—";
  return new Date(dateStr).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

