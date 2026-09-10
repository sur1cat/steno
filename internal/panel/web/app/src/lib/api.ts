import { t } from "@/lib/i18n";
// Разговор с сервисом. Один слой на всё приложение: страницы не знают ни про
// заголовки, ни про то, как выглядит отказ, — они получают данные или ошибку
// с текстом, который не стыдно показать.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: init?.body ? { "Content-Type": "application/json" } : undefined,
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let body: unknown = null;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    // Сервер ответил не JSON — это уже поломка, и показывать её надо как есть.
    throw new ApiError(res.status, text.slice(0, 200) || t("непонятный ответ сервера"));
  }
  if (!res.ok) {
    const msg = (body as { error?: string } | null)?.error;
    throw new ApiError(res.status, msg ?? `${t("ошибка")} ${res.status}`);
  }
  return body as T;
}

const get = <T,>(path: string) => request<T>(path);
const post = <T,>(path: string, body?: unknown) =>
  request<T>(path, { method: "POST", body: JSON.stringify(body ?? {}) });
const del = <T,>(path: string) => request<T>(path, { method: "DELETE" });

// --- типы ответов -----------------------------------------------------------

export interface MeetingRow {
  id: string;
  title: string;
  startedAt: number;
  durationSec: number;
  participants: string[] | null;
  status: string;
  leftReason: string;
  tasks: number;
  hasAudio: boolean;
}

export interface MeetingsPage {
  meetings: MeetingRow[];
  total: number;
  page: number;
  perPage: number;
}

export interface Segment {
  start: number;
  end: number;
  speaker: string;
  text: string;
}

export interface ActionItem {
  owner: string;
  what: string;
  due: string;
  quote: string;
  at: number;
  project: string;
}

export interface Decision {
  what: string;
  why: string;
  at: number;
  project: string;
}

export interface OpenQuestion {
  question: string;
  waiting_on: string;
  at: number;
  project: string;
}

export interface Followup {
  title: string;
  tldr: string[] | null;
  decisions: Decision[] | null;
  action_items: ActionItem[] | null;
  open_questions: OpenQuestion[] | null;
  risks: string[] | null;
}

export interface Spend {
  model: string;
  input: number;
  output: number;
  usd: number;
}

/** Цена удаления созвона: что уйдёт вместе с ним и что вернётся в работу.
 *  Считает её Go — одной функцией на панель, `steno ui` и `steno rm`, — и
 *  оттуда же приезжает готовая фраза: иначе в трёх местах три разных числа. */
export interface Removal {
  segments: number;
  tasks: number;
  followup: boolean;
  publications: number;
  events: number;
  indexed: number;
  openedTasks: number;
  openedDecisions: number;
  openedQuestions: number;
  /** Чужие пункты, закрытые на этом созвоне: они откроются заново. */
  reopen: number;
  bytes: number;
  /** Готовая фраза «уйдут: …» — ровно та же, что видит терминал. */
  text: string;
}

export interface MeetingFull {
  id: string;
  title: string;
  startedAt: number;
  durationSec: number;
  participants: string[] | null;
  status: string;
  leftReason: string;
  meetUrl: string;
  followup: Followup | null;
  hasFollowup: boolean;
  segments: Segment[] | null;
  links: Record<string, string> | null;
  hasAudio: boolean;
  spend: Spend;
  removal: Removal;
}

export interface HitPart {
  text: string;
  hit?: boolean;
}

export interface SearchHit {
  meetingId: string;
  title: string;
  startedAt: number;
  kind: string;
  at: number;
  speaker: string;
  parts: HitPart[] | null;
}

export interface TaskRow {
  owner: string;
  what: string;
  due: string;
  quote: string;
  at: number;
  meetingId: string;
  meetingTitle: string;
  meetingAt: number;
}

export interface ProjectRow {
  name: string;
  tasks: number;
  questions: number;
  decisions: number;
  closed: number;
}

export interface ProjectItem {
  id: string;
  kind: "task" | "decision" | "question";
  text: string;
  owner: string;
  due: string;
  status: string;
  quote: string;
  openedAt: number;
  updatedAt: number;
  openedIn: string;
  closedIn: string;
  note: string;
}

/** Папка на машине, где стоит steno. Файлов обзор не отдаёт вовсе. */
export interface BrowseDir {
  name: string;
  path: string;
  /** Внутри лежит .git — то, что человек глазами и ищет. */
  isRepo: boolean;
}

export interface BrowsePage {
  path: string;
  /** Пусто — выше идти некуда, это домашний каталог. */
  parent: string;
  dirs: BrowseDir[] | null;
}

export type SourceKind = "repo" | "path" | "url" | "text";

export interface Source {
  kind: SourceKind;
  value: string;
}

export interface SettingsProject {
  name: string;
  aliases: string[] | null;
  about: string;
  sources: Source[] | null;
  /** Имена людей — теми, которыми их зовут вслух, а не подписью аккаунта. */
  people: string[] | null;
  /**
   * Остальные слова проекта: сервисы, системы, сокращения. Имена людей сервер
   * из этого списка вычитает — в базе они лежат и здесь тоже (одним плоским
   * словарём их читает whisper), но два поля с одним и тем же содержимым
   * правятся вразнобой.
   */
  vocabulary: string[] | null;
  primerChars: number;
  builtAt: number;
  primer: string;
}

// Секретов панель не знает вовсе — ни значений, ни имён переменных, ни того,
// заданы ли они. Токены задаёт разработчик в `steno setup`; человеку, который
// открыл панель посмотреть, куда уходит follow-up, показывать нечего.
// Лишние поля в ответе сервера здесь просто не описаны и никем не читаются.

export interface ChannelField {
  key: string;
  label: string;
  /**
   * «list» — набор значений; по проводу это строка через запятую.
   * «google» — не поле, а кнопка «Подключить Google»: значения не хранит.
   */
  kind: "text" | "list" | "number" | "duration" | "switch" | "google";
  hint: string;
  placeholder: string;
}

export interface Channel {
  key: string;
  name: string;
  about: string;
  in: boolean;
  out: boolean;
  enabled: boolean;
  values: Record<string, string>;
  fields: ChannelField[];
  summary: string;
  live: boolean;
}

export interface Settings {
  projects: SettingsProject[];
  channels: Channel[];
}

/**
 * Доступ в Google — один на всю панель, а не на канал: календарь, почта бота и
 * документы ходят под одним и тем же согласием. Поэтому поле «google» есть у
 * трёх каналов, а состояние спрашивается одно.
 */
export interface GoogleStatus {
  /** Вход по кнопке заведён вообще. false — кнопку не рисуем, только `why`. */
  ready: boolean;
  connected: boolean;
  /** Почта того, кто подключился. Пусто, пока не подключён. */
  account: string;
  /** Готовый текст для человека. Показывать как есть, ничего не дописывая. */
  why: string;
  /**
   * Какого рода это сообщение: «info» — объясняем, всё в порядке; «warn» —
   * что-то не работает. Приходит всегда.
   *
   * Отдельным полем, потому что по остальным трём не вычисляется — и это
   * проверено, а не предположено: ключ организации уживается с заведённым
   * входом по кнопке (ready:true), а подключённый аккаунт переживает смену
   * client secret и остаётся connected при ready:false. Красить по `ready`
   * или по `connected` — значит поставить тревогу не туда.
   */
  severity: "info" | "warn";
}

export interface ScheduleEntry {
  key: string;
  calendarId: string;
  title: string;
  meetUrl: string;
  startsAt: number;
  endsAt: number;
  attendees: string[] | null;
  skip: string;
  override: "" | "skip" | "attend";
  recorded: string;
  willAttend: boolean;
}

export interface SchedulePage {
  entries: ScheduleEntry[];
  days: number;
  calendarOn: boolean;
}

export interface InviteResult {
  status: "started" | "duplicate" | "no_capacity";
  meetingId?: string;
  message: string;
}

export interface UploadResult {
  id: string;
  title: string;
  bytes: number;
}

/**
 * Загрузка записи. Единственная ручка, которую нельзя позвать через fetch: он
 * не умеет докладывать, сколько уже ушло, а файл здесь — гигабайт видео с
 * четырёхчасовой встречи. Без полосы прогресса это выглядит зависшей вкладкой,
 * и человек нажимает «отправить» второй раз.
 */
export function uploadRecording(
  file: File,
  title: string,
  opts: { onProgress?: (sent: number, total: number) => void; signal?: AbortSignal } = {},
): Promise<UploadResult> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("file", file, file.name);
    if (title.trim()) form.append("title", title.trim());

    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/upload");
    xhr.withCredentials = true;
    xhr.responseType = "text";

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) opts.onProgress?.(e.loaded, e.total);
    };
    xhr.onerror = () => reject(new ApiError(0, t("связь оборвалась, файл не дошёл")));
    xhr.onabort = () => reject(new ApiError(0, t("отменено")));
    xhr.onload = () => {
      let body: unknown = null;
      try {
        body = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        reject(new ApiError(xhr.status, t("непонятный ответ сервера")));
        return;
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        // Отказ в режиме субтитров объясняет причину словами — её и показываем,
        // а не «ошибка 400».
        const msg = (body as { error?: string } | null)?.error;
        reject(new ApiError(xhr.status, msg ?? `${t("ошибка")} ${xhr.status}`));
        return;
      }
      resolve(body as UploadResult);
    };

    opts.signal?.addEventListener("abort", () => xhr.abort(), { once: true });
    xhr.send(form);
  });
}

// --- ручки ------------------------------------------------------------------

export const api = {
  session: () => get<{ authenticated: boolean }>("/api/session"),
  login: (password: string) => post<{ authenticated: boolean }>("/api/login", { password }),
  logout: () => post<{ authenticated: boolean }>("/api/logout"),

  meetings: (page: number) => get<MeetingsPage>(`/api/meetings?page=${page}`),
  meeting: (id: string) => get<MeetingFull>(`/api/meetings/${encodeURIComponent(id)}`),
  deleteMeeting: (id: string) =>
    del<{ ok: boolean; removed: Removal }>(`/api/meetings/${encodeURIComponent(id)}`),
  search: (q: string) => get<{ q: string; hits: SearchHit[] | null }>(`/api/search?q=${encodeURIComponent(q)}`),
  tasks: (owner: string) =>
    get<{ tasks: TaskRow[] | null; owners: string[] | null; owner: string }>(
      `/api/tasks?owner=${encodeURIComponent(owner)}`,
    ),

  projects: () => get<{ projects: ProjectRow[] }>("/api/projects"),
  project: (name: string) =>
    get<{ name: string; items: ProjectItem[] }>(`/api/projects/${encodeURIComponent(name)}`),
  saveProject: (body: {
    oldName?: string;
    name: string;
    about: string;
    aliases: string[];
    sources: Source[];
    people: string[];
    vocabulary: string[];
  }) => post<{ name: string }>("/api/projects", body),
  deleteProject: (name: string) => del<{ ok: boolean }>(`/api/projects/${encodeURIComponent(name)}`),
  buildContext: (name: string) =>
    post<{ status: string }>(`/api/projects/${encodeURIComponent(name)}/context`),

  settings: () => get<Settings>("/api/settings"),

  // Обзор папок — на машине, где стоит сам steno, а не там, где открыт браузер.
  // Пустой path — домашний каталог.
  browse: (path: string) => get<BrowsePage>(`/api/browse?path=${encodeURIComponent(path)}`),

  googleStatus: () => get<GoogleStatus>("/api/google/status"),
  // Возвращает адрес согласия Google. Уводить туда надо эту же вкладку:
  // вернётся человек по адресу, который Google знает, — в новой вкладке он
  // окажется в чужой копии панели, а исходная так и останется неподключённой.
  googleConnect: () => post<{ url: string }>("/api/google/connect"),
  googleDisconnect: () => post<{ ok: boolean }>("/api/google/disconnect"),

  saveChannel: (key: string, enabled: boolean, values: Record<string, string>) =>
    post<{ ok: boolean }>(`/api/channels/${encodeURIComponent(key)}`, { enabled, values }),

  closeItem: (id: string) => post<{ ok: boolean }>(`/api/items/${encodeURIComponent(id)}/close`),
  reopenItem: (id: string) => post<{ ok: boolean }>(`/api/items/${encodeURIComponent(id)}/reopen`),

  schedule: (days: number) => get<SchedulePage>(`/api/schedule?days=${days}`),
  scheduleOverride: (key: string, decision: "" | "skip" | "attend") =>
    post<{ ok: boolean }>(`/api/schedule/${encodeURIComponent(key)}/override`, { decision }),
  invite: (url: string, title: string) => post<InviteResult>("/api/invite", { url, title }),
  upload: uploadRecording,
};
