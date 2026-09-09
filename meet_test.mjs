// Проверка meet.js на синтетическом DOM: настоящий браузер для этого не нужен,
// а сломанный разбор строк субтитров стоит дорого — без имён follow-up резко
// теряет ценность. Запуск: node meet_test.mjs (нужен только node).
import assert from "node:assert";
import { readFileSync } from "node:fs";

// --- минимальный DOM -------------------------------------------------------

// Поддерживаются ровно те формы селекторов, которыми пользуется meet.js: тег,
// #id, .класс, [атрибут], [атрибут="значение"], [атрибут*="подстрока"] и
// перечисление через запятую. Непонятная форма — исключение, а не пустой
// список: молча вернув ноль совпадений, фикстура разрешила бы коду то, чего
// живой DOM не разрешит, и тест прошёл бы на сломанном разборе.
const SEL_PART = /^\s*(?:([a-zA-Z][\w-]*)|#([\w-]+)|\.([\w-]+)|\[([\w-]+)(?:(\*?=)"([^"]*)")?\])+\s*$/;

function parseSel(sel) {
  if (!SEL_PART.test(sel)) throw new Error(`фикстура не умеет селектор: ${sel}`);
  const re = /([a-zA-Z][\w-]*)|#([\w-]+)|\.([\w-]+)|\[([\w-]+)(?:(\*?=)"([^"]*)")?\]/g;
  const preds = [];
  let m;
  // Значения снимаются с match сразу: сам объект re.exec переиспользует, и
  // замыкание над ним читало бы уже следующее (или null) совпадение.
  while ((m = re.exec(sel))) {
    const [, tag, id, cls, attr, op, val] = m;
    if (tag) preds.push((n) => n.tagName === tag.toUpperCase());
    else if (id) preds.push((n) => n.getAttribute("id") === id);
    else if (cls) preds.push((n) =>
      String(n.getAttribute("class") || "").split(/\s+/).includes(cls));
    else if (attr) {
      preds.push((n) => {
        const v = n.getAttribute(attr);
        if (v === null) return false;
        if (op === "=") return v === val;
        if (op === "*=") return v.includes(val);
        return true; // просто наличие атрибута
      });
    }
  }
  if (!preds.length) throw new Error(`пустой селектор: ${sel}`);
  return (n) => preds.every((p) => p(n));
}

function compile(sel) {
  const parts = sel.split(",").map((s) => s.trim()).filter(Boolean);
  if (!parts.length) throw new Error(`пустой селектор: ${sel}`);
  const matchers = parts.map(parseSel);
  return (n) => matchers.some((f) => f(n));
}

class N {
  constructor(tag, attrs = {}, kids = []) {
    this.tagName = tag.toUpperCase();
    this.nodeType = 1;
    this.attrs = attrs;
    this.clicks = 0;
    this.events = [];
    this.childNodes = kids.map((k) => (typeof k === "string" ? new T(k) : k));
    this.children = this.childNodes.filter((k) => k.nodeType === 1);
    for (const k of this.childNodes) k.parent = this;
  }
  get alt() { return this.attrs.alt || ""; }
  get type() { return this.attrs.type; }
  // <select> отдаёт свои <option> отдельным списком — pickCaptionLanguage
  // ходит именно туда.
  get options() { return [...this.walk()].filter((n) => n !== this && n.tagName === "OPTION"); }
  get value() { return this._value !== undefined ? this._value : (this.attrs.value || ""); }
  set value(v) { this._value = v; }
  getAttribute(n) { return this.attrs[n] ?? null; }
  setAttribute(n, v) { this.attrs[n] = v; }
  // Настоящий узел отдаёт список атрибутов — на нём держится общий поиск
  // признака речи (data-is-speaking и подобные). Без этого фикстура разрешила
  // бы коду то, чего живой DOM не разрешит.
  get attributes() {
    return Object.entries(this.attrs).map(([name, value]) => ({ name, value: String(value) }));
  }
  // Скрытый узел отдаёт нулевой прямоугольник — ровно так его отличает
  // visible() в meet.js.
  getBoundingClientRect() {
    return this.attrs.hidden ? { width: 0, height: 0 } : { width: 100, height: 20 };
  }
  get textContent() { return this.childNodes.map((c) => c.textContent).join(""); }
  // left() читает именно innerText — без него проверка ухода из звонка
  // всегда работала по пустой строке и ничего не доказывала.
  get innerText() { return this.textContent; }
  click() { this.clicks++; if (this.onclick) this.onclick(); }
  dispatchEvent(e) { this.events.push(e && e.type); return true; }
  *walk() { yield this; for (const c of this.children) yield* c.walk(); }
  querySelectorAll(sel) {
    const match = compile(sel);
    const out = [];
    for (const n of this.walk()) {
      if (n === this) continue;
      if (match(n)) out.push(n);
    }
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  // Полоски микрофона ищутся по jsname, а говорит человек или нет — видно по
  // прозрачности их ОБЁРТКИ. Без родителя эта проверка не проверялась бы.
  get parentElement() { return this.parent || null; }
}
class T {
  constructor(t) { this.nodeType = 3; this.textContent = t; this.childNodes = []; this.children = []; }
  *walk() {}
}

// Иконка Material: имя иконки лежит текстом внутри элемента. Ровно она и
// приехала с живого созвона говорящим по имени arrow_downward.
const icon = (name, attrs = {}) => new N("i", { "aria-hidden": "true", ...attrs }, [name]);

const captionRegion = new N("div", { role: "region", "aria-label": "Субтитры" }, [
  new N("div", {}, [
    new N("div", {}, [new N("img", { alt: "Участник А" }), new N("div", {}, ["Участник А"])]),
    new N("div", {}, [new N("div", {}, ["давайте начнём с релиза"])]),
  ]),
  new N("div", {}, [
    new N("div", {}, ["Участник Б"]),
    new N("div", {}, [new N("div", {}, ["я закончу "]), new N("div", {}, ["миграцию к пятнице"])]),
  ]),
]);

const body = new N("body", {}, [
  captionRegion,
  new N("div", { "data-participant-id": "p1" }, [new N("div", {}, ["Участник А"])]),
  new N("div", { "data-participant-id": "p2" }, [new N("div", {}, ["Участник Б"])]),
]);

globalThis.window = {
  HTMLInputElement: {
    prototype: Object.defineProperty({}, "value", {
      configurable: true,
      set(v) { this._value = v; },
      get() { return this._value; },
    }),
  },
};
globalThis.Event = class Event {
  constructor(type) { this.type = type; }
};
globalThis.document = {
  body,
  querySelectorAll: (s) => body.querySelectorAll(s),
  querySelector: (s) => body.querySelector(s),
};
// Вычисленный стиль. Meet прячет и показывает подсветку не сменой разметки, а
// прозрачностью и заливкой, — поэтому фикстура умеет отдавать и их. Значения
// берутся из атрибутов узла: разметку это не засоряет, а в живом DOM их
// поставит сам браузер.
globalThis.getComputedStyle = (el) => ({
  visibility: (el && el.attrs && el.attrs["x-visibility"]) || "visible",
  opacity: (el && el.attrs && el.attrs["x-opacity"]) || "1",
  backgroundColor: (el && el.attrs && el.attrs["x-bg"]) || "rgba(0, 0, 0, 0)",
});
const DEFAULTS = JSON.parse(readFileSync("selectors.json", "utf8"));
const SRC = readFileSync("meet.js", "utf8");

function boot(selectors) {
  window.__stenoSelectors = selectors;
  window.__steno = undefined;
  window.__stenoError = undefined;
  eval(SRC);
  return window.__steno;
}

function withBody(body, fn) {
  const prev = document.body;
  document.body = body;
  document.querySelectorAll = (s) => body.querySelectorAll(s);
  document.querySelector = (s) => body.querySelector(s);
  try {
    return fn();
  } finally {
    document.body = prev;
    document.querySelectorAll = (s) => prev.querySelectorAll(s);
    document.querySelector = (s) => prev.querySelector(s);
  }
}

// --- обычный случай ---------------------------------------------------------
const steno = boot(DEFAULTS);
const c = steno.captions();
assert.ok(c.ok, "область субтитров не нашлась по aria-label");
assert.deepStrictEqual(c.lines, [
  // Имя приходит и как alt аватарки, и как подпись — дубль не должен
  // попасть в текст реплики.
  { speaker: "Участник А", text: "давайте начнём с релиза" },
  { speaker: "Участник Б", text: "я закончу миграцию к пятнице" },
]);
assert.deepStrictEqual(steno.participants(), ["Участник А", "Участник Б"]);
assert.strictEqual(steno.inCall(), true);
assert.strictEqual(steno.left(), false);
assert.strictEqual(steno.captionsOn(), true);

// --- интерфейс внутри области субтитров -------------------------------------
// С живого созвона приехало {"speaker":"arrow_downward","text":"Jump to
// bottom"}: кнопка «вниз» из самой области субтитров. Иконки Material рисуются
// лигатурой, поэтому имя иконки лежит текстом внутри кнопки — и разбор принял
// его за участника. Такая строка не просто мусор: при выравнивании она
// забирает время у соседних реплик, и имя от кнопки приезжает на чужой текст
// whisper.
{
  const withChrome = new N("div", { role: "region", "aria-label": "Субтитры" }, [
    // Кнопка обёрнута в div — так её и рисует Meet.
    new N("div", {}, [
      new N("button", { "aria-label": "Jump to bottom" }, [
        icon("arrow_downward"),
        new N("span", {}, ["Jump to bottom"]),
      ]),
    ]),
    // Она же без обёртки, прямо ребёнком области.
    new N("button", { "aria-label": "К последнему сообщению" }, [
      icon("arrow_downward"),
      new N("span", {}, ["К последнему сообщению"]),
    ]),
    // И она же как div с role=button: у Google встречаются обе формы.
    new N("div", { role: "button", "aria-label": "Jump to bottom" }, [
      icon("keyboard_arrow_down"),
      new N("span", {}, ["Jump to bottom"]),
    ]),
    // Иконка без aria-hidden: на одну защиту меньше, лигатуру должен снять
    // сам тег.
    new N("div", {}, [
      new N("button", {}, [new N("i", {}, ["arrow_downward"])]),
    ]),
    // Настоящая реплика — и внутри неё, не в кнопке, две формы иконки.
    // Каждая ловится своим фильтром, поэтому в фикстуре нужны обе:
    // <i> без aria-hidden ловится только по тегу, а <span aria-hidden> —
    // только по атрибуту.
    new N("div", {}, [
      new N("div", {}, [new N("img", { alt: "Rustem Turgeldin" }), new N("div", {}, ["Rustem Turgeldin"])]),
      new N("i", {}, ["volume_up"]),
      new N("span", { "aria-hidden": "true" }, ["closed_caption"]),
      new N("div", {}, ["давайте начнём"]),
    ]),
  ]);
  const st = boot(DEFAULTS);
  const got = withBody(new N("body", {}, [withChrome]), () => st.captions());
  assert.ok(got.ok, "область субтитров с кнопкой внутри перестала находиться");
  assert.deepStrictEqual(
    got.lines,
    [{ speaker: "Rustem Turgeldin", text: "давайте начнём" }],
    "интерфейс Meet попал в реплики");
  for (const l of got.lines) {
    assert.ok(!/arrow_downward|keyboard_arrow_down|volume_up/.test(l.speaker + l.text),
      `лигатура иконки попала в реплику: ${JSON.stringify(l)}`);
    assert.ok(!/Jump to bottom|К последнему сообщению/.test(l.speaker + l.text),
      `подпись кнопки попала в реплику: ${JSON.stringify(l)}`);
  }
}

// Та же беда в плитке участника: иконка состояния микрофона стала бы именем.
{
  const tiles = new N("body", {}, [
    new N("div", { "data-participant-id": "p1" }, [
      icon("mic_off"),
      new N("div", {}, ["Rustem Turgeldin"]),
    ]),
  ]);
  const st = boot(DEFAULTS);
  assert.deepStrictEqual(
    withBody(tiles, () => st.participants()), ["Rustem Turgeldin"],
    "иконка стала именем участника");
}

// --- кто говорит прямо сейчас -----------------------------------------------
// Имена в расшифровке берутся из субтитров, но субтитры — это распознавание
// речи, и оно подводит целиком: на живом созвоне Meet слушал русскую речь
// английским языком и выдал 31 букву за 232 секунды. Подсветка говорящего от
// распознавания не зависит, поэтому её и снимаем — но устойчивого признака у
// Meet нет, и здесь проверяются все три способа, которыми скрипт его ищет.

// tile — плитка участника. speaking задаёт, чем именно она помечена говорящей.
const tile = (name, extra = {}, kids = []) =>
  new N("div", { "data-participant-id": "id-" + name, ...extra }, [
    icon("mic"),
    new N("div", {}, [name]),
    ...kids,
  ]);

// Способ 1: подпись для скринридера.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    tile("Участник А", {}, [new N("div", { "aria-label": "Участник А говорит" }, [])]),
    tile("Участник Б"),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "подсветка по aria-label не считалась");
}

// Способ 2: полоски микрофона по jsname. Устойчивого признака речи у Meet нет,
// и это лучшее, что есть: прозрачность обёртки полосок Meet гонит от 0 к 1,
// пока человек говорит. jsname генерируется Closure, но живёт заметно дольше
// имён классов — на том же основании в проекте уже работают
// captionRegionJsnames.
{
  const bars = (opacity) =>
    new N("div", { "x-opacity": opacity }, [new N("div", { jsname: "QgSmzd" }, [])]);
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    tile("Участник А", {}, [bars("1")]),
    // Полоски есть у всех и всегда: молчащего выдаёт прозрачность.
    tile("Участник Б", {}, [bars("0")]),
    tile("Участник В", {}, [bars("0.05")]),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "прозрачность обёртки полосок микрофона прочитана неверно");
}

// Способ 5: цвет заливки. Имя класса Meet меняет чаще, чем цвет подсветки, —
// но и цвет менял (в ноябре 2025), поэтому список цветов живёт в
// selectors.json, а не в коде.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    tile("Участник А", {}, [new N("div", { "x-bg": "rgb(11, 87, 208)" }, [])]),
    tile("Участник Б", {}, [new N("div", { "x-bg": "rgba(0, 0, 0, 0)" }, [])]),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "цвет подсветки не прочитан");
}

// Способ 2: атрибут, говорящий о речи. Проверяются обе формы — флаг и
// уровень громкости, — и обе «молчащие»: data-audio-level="0" стоит у всех
// плиток всегда, и без проверки значения говорящими стали бы все сразу.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    tile("Участник А", { "data-is-speaking": "true" }),
    tile("Участник Б", { "data-is-speaking": "false" }),
    tile("Участник В", { "data-audio-level": "0.8" }),
    tile("Участник Г", { "data-audio-level": "0" }),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()),
    ["Участник А", "Участник В"],
    "признак речи по атрибуту прочитан неверно");
}

// Атрибут про то, КТО это, а не про то, что он говорит, не должен считаться
// речью. data-speaker-id стоит у всех плиток всегда — по нему говорящими
// оказались бы все и всегда, и лента стала бы бессмысленной.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    tile("Участник А", { "data-speaker-id": "abc123" }),
    tile("Участник Б", { "data-speaking-name": "Участник Б" }),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), [],
    "атрибут с идентификатором принят за признак речи");
}

// Способ 3: селектор из selectors.json. Сюда кладут то, что видно на живом
// созвоне: у Meet подсветка рисуется элементом с обфусцированным классом, и
// починка должна быть правкой конфига, а не пересборкой.
{
  const st = boot({ ...DEFAULTS, speakingSelectors: [".speech-bars"] });
  const body = new N("body", {}, [
    tile("Участник А", {}, [new N("div", { class: "speech-bars" }, [])]),
    // Тот же элемент, но скрытый: Meet держит его в разметке всегда и
    // показывает только на время речи.
    tile("Участник Б", {}, [new N("div", { class: "speech-bars", hidden: true }, [])]),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "селектор из конфига не сработал или невидимый элемент сочли речью");
}

// Кривой селектор в конфиге не должен ронять весь опрос: без подсветки бот
// хотя бы пишет звук, а с исключением на каждом такте — выходит из звонка
// «страница не отвечает».
{
  const st = boot({ ...DEFAULTS, speakingSelectors: ["((("] });
  const body = new N("body", {}, [tile("Участник А", { "data-is-speaking": "true" })]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "кривой селектор в конфиге сломал определение говорящего");
}

// Говорят двое подряд — лента должна успевать за сменой.
{
  const st = boot(DEFAULTS);
  const a = tile("Участник А", { "data-is-speaking": "true" });
  const b = tile("Участник Б", { "data-is-speaking": "false" });
  const body = new N("body", {}, [a, b]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"]);
  a.attrs["data-is-speaking"] = "false";
  b.attrs["data-is-speaking"] = "true";
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник Б"],
    "смена говорящего не заметна");
}

// Говорит сам бот. Молчащий бот всё равно участник, и его плитку Meet иногда
// подсвечивает; своё имя в ленте — это своё имя в follow-up.
{
  const st = boot(DEFAULTS);
  st.setName("Steno · идёт запись");
  const body = new N("body", {}, [
    // Своя плитка у Meet помечена data-self-name.
    new N("div", { "data-participant-id": "self", "data-is-speaking": "true" }, [
      new N("div", { "data-self-name": "Steno · идёт запись" }, ["Steno · идёт запись"]),
    ]),
    tile("Участник А", { "data-is-speaking": "true" }),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "бот попал в собственную ленту говорящих");
}

// Тот же бот, но без data-self-name: у залогиненного аккаунта разметка своей
// плитки бывает другой. Остаётся вторая проверка — по имени, которым бот
// представился.
{
  const st = boot(DEFAULTS);
  st.setName("Steno · идёт запись");
  const body = new N("body", {}, [
    tile("Steno · идёт запись", { "data-is-speaking": "true" }),
    tile("Участник А", { "data-is-speaking": "true" }),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "бот попал в ленту, когда data-self-name не оказалось");
}

// И наоборот: бот зашёл залогиненным, экрана с полем имени не было, поэтому
// setName записать себе имя не успел — а плитка подписана аккаунтом, а не
// настроенным именем. Тогда работает только data-self-name, и проверять его
// надо там, где вторая защита помочь не может.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    new N("div", { "data-participant-id": "self", "data-is-speaking": "true" }, [
      new N("div", { "data-self-name": "steno-bot@example.com" }, ["Steno Bot"]),
    ]),
    tile("Участник А", { "data-is-speaking": "true" }),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "залогиненный бот попал в собственную ленту: data-self-name не сработал");
}

// Одного человека Meet рисует несколькими плитками сразу — в ленте снизу и на
// главной. Подсветиться может любая из них: если брать первую попавшуюся,
// говорящий на главной плитке пропадёт из ленты целиком.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [
    new N("div", { "data-participant-id": "p1" }, [new N("div", {}, ["Участник А"])]),
    new N("div", { "data-participant-id": "p1", "data-is-speaking": "true" }, [
      new N("div", {}, ["Участник А"]),
    ]),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "подсветка на второй плитке того же участника потеряна");
}

// Никто не говорит — пустой список, а не «все». Это состояние на созвоне
// самое частое, и ошибка здесь размазала бы чужие имена по всей расшифровке.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [tile("Участник А"), tile("Участник Б")]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), []);
}

// Имя в ленте подсветки должно быть тем же, что в списке участников: иначе
// два источника имён не сойдутся ни между собой, ни с субтитрами, и одному
// человеку в follow-up достанутся два разных ярлыка.
//
// В плитке лежит иконка микрофона, и её лигатура — тот самый мусор, который
// однажды приехал с живого созвона говорящим по имени arrow_downward. В имени
// говорящего её быть не должно.
{
  const st = boot(DEFAULTS);
  const body = new N("body", {}, [tile("Rustem Turgeldin", { "data-is-speaking": "true" })]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Rustem Turgeldin"],
    "в имени говорящего мусор из плитки");
  assert.deepStrictEqual(withBody(body, () => st.participants()), ["Rustem Turgeldin"],
    "подсветка и список участников зовут человека по-разному");
}

// --- панель участников ------------------------------------------------------
// Полоски микрофона Meet рисует в строках этой панели, а плитки в сетке
// виртуализируются: говорящего может не быть в сетке вовсе. Панель локальная,
// другие участники её не видят.

// Панель закрыта — надо нажать кнопку, а не что попало.
{
  const st = boot(DEFAULTS);
  const btn = new N("div", { role: "button", "aria-label": "Показать всех" }, [icon("people")]);
  const other = new N("div", { role: "button", "aria-label": "Чат с участниками" }, []);
  const body = new N("body", {}, [other, btn]);
  assert.strictEqual(withBody(body, () => st.openPeoplePanel()), "clicked");
  assert.strictEqual(btn.clicks, 1, "нажали не ту кнопку");
  assert.strictEqual(other.clicks, 0, "нажали лишнюю кнопку");
}

// Панель уже открыта — трогать нечего. Второе нажатие закрыло бы её обратно,
// и подсветка пропала бы ровно там, где мы её и добивались.
{
  const st = boot(DEFAULTS);
  const btn = new N("div", { role: "button", "aria-label": "Показать всех" }, []);
  const panel = new N("div", { "aria-label": "Участники" }, [
    new N("div", { role: "listitem", "data-participant-id": "p1" }, [
      new N("div", {}, ["Участник А"]),
    ]),
  ]);
  const body = new N("body", {}, [btn, panel]);
  assert.strictEqual(withBody(body, () => st.openPeoplePanel()), "open");
  assert.strictEqual(btn.clicks, 0, "нажали кнопку при открытой панели — то есть закрыли её");
}

// Кнопки нет — надо честно сказать, а не сделать вид, что открыли.
{
  const st = boot(DEFAULTS);
  assert.strictEqual(withBody(new N("body", {}, []), () => st.openPeoplePanel()), "");
}

// Строка панели участников помечена тем же data-participant-id, что и плитка,
// — значит подсветку в ней speaking() найдёт без единой отдельной ветки.
{
  const st = boot(DEFAULTS);
  const row = (name, opacity) =>
    new N("div", { role: "listitem", "data-participant-id": "id-" + name }, [
      new N("div", {}, [name]),
      new N("div", { "x-opacity": opacity }, [new N("div", { jsname: "QgSmzd" }, [])]),
    ]);
  const body = new N("body", {}, [
    new N("div", { "aria-label": "Участники" }, [row("Участник А", "1"), row("Участник Б", "0")]),
  ]);
  assert.deepStrictEqual(withBody(body, () => st.speaking()), ["Участник А"],
    "подсветку в строке панели участников не увидели");
}

// --- метасимвол в selectors.json --------------------------------------------
// Реальные подписи Meet содержат скобки («Субтитры (бета)»). Раньше такая
// строка роняла весь скрипт: window.__steno не определялся вовсе, и бот
// умирал через 90 секунд с сообщением про кнопку входа.
const withParen = boot({ ...DEFAULTS, captionRegionLabels: ["Субтитры (бета"] });
assert.ok(withParen, `скрипт упал на метасимволе: ${window.__stenoError}`);
assert.strictEqual(window.__stenoError, undefined);
assert.strictEqual(withParen.captionsOn(), false, "мусорная подпись не должна ничего находить");

// --- очищенный список -------------------------------------------------------
// Пустой массив в JS истинный, а new RegExp("") совпадает со всем. Проверять
// это надо там, где регулярка действительно спрашивается: пока на странице
// есть плитки участников, left() выходит раньше и ничего не доказывает.
// Поэтому здесь состояние «звонок закончился»: плиток нет, текст обычный.
const afterCall = new N("body", {}, [
  new N("div", {}, ["Спасибо, что были с нами"]),
  new N("div", {}, [new N("div", {}, ["Вернуться на главный"])]),
]);

for (const [name, sel] of [
  ["пустые списки", { captionRegionLabels: [], joinButtonTexts: [], nameInputLabels: [], leftMeetingTexts: [] }],
  ["список из пустой строки", { leftMeetingTexts: [""] }],
]) {
  const st = boot(sel);
  assert.ok(st, `скрипт упал на списке «${name}»`);
  withBody(afterCall, () => {
    assert.strictEqual(
      st.left(), false,
      `«${name}»: очищенный leftMeetingTexts не должен означать «мы вышли из звонка»`);
    assert.strictEqual(
      st.clickJoin(), false,
      `«${name}»: очищенный joinButtonTexts не должен делать подходящей любую кнопку`);
  });
}

// А настоящая фраза ухода по-прежнему распознаётся.
{
  const st = boot(DEFAULTS);
  const left = new N("body", {}, [new N("div", {}, ["Вы вышли из встречи"])]);
  withBody(left, () => {
    assert.strictEqual(st.left(), true, "уход из звонка перестал распознаваться");
  });
}

// --- путь к языку субтитров -------------------------------------------------
// Отдельной кнопки «настройки субтитров» в нынешнем Meet нет: с 2024 года язык
// живёт в общих настройках — ⋮ → «Настройки» → вкладка «Субтитры». Старый
// селектор искал кнопку, которой не существует, — отсюда честное «не нашёл
// настройки субтитров» в логе живого созвона.
function settingsFixture({ withMore = true, withCaptionsTab = true } = {}) {
  const state = { menu: false, dialog: false, tab: "Аудио" };
  const root = new N("body", {}, []);
  const rebuild = () => {
    const kids = [];
    if (withMore) {
      const more = new N("button", { "aria-label": "Ещё" }, [icon("more_vert")]);
      more.onclick = () => { state.menu = true; rebuild(); };
      kids.push(more);
    }
    if (state.menu && !state.dialog) {
      const item = new N("div", { role: "menuitem" }, ["Настройки"]);
      item.onclick = () => { state.dialog = true; state.menu = false; rebuild(); };
      kids.push(new N("div", { role: "menu" }, [item]));
    }
    if (state.dialog) {
      const names = withCaptionsTab
        ? ["Аудио", "Видео", "Общие", "Субтитры", "Реакции"]
        : ["Аудио", "Видео", "Общие"];
      const tabs = names.map((name) => {
        const t = new N("div",
          { role: "tab", "aria-selected": String(state.tab === name) }, [name]);
        t.onclick = () => { state.tab = name; rebuild(); };
        return t;
      });
      kids.push(new N("div", { role: "dialog" }, [new N("div", { role: "tablist" }, tabs)]));
    }
    root.childNodes = kids;
    root.children = kids.filter((k) => k.nodeType === 1);
    for (const k of kids) k.parent = root;
  };
  rebuild();
  return { root, state };
}

function walkToCaptions(st, root) {
  let last = null;
  for (let i = 0; i < 6; i++) {
    last = st.captionSettingsStep();
    if (last.done) break;
  }
  return last;
}

{
  const st = boot(DEFAULTS);
  const { root, state } = settingsFixture();
  const r = withBody(root, () => walkToCaptions(st, root));
  assert.ok(r.done, `не дошли до вкладки субтитров: ${JSON.stringify(r)}`);
  assert.strictEqual(state.tab, "Субтитры", "выбрали не ту вкладку");
}

// Диалог открылся, а вкладки «Субтитры» в нём нет — надо сказать, какие есть.
// Иначе следующий переезд Meet чинится только заходом в живой звонок руками.
{
  const st = boot(DEFAULTS);
  const { root } = settingsFixture({ withCaptionsTab: false });
  const r = withBody(root, () => walkToCaptions(st, root));
  assert.ok(!r.done, "доложили об успехе без вкладки субтитров");
  assert.strictEqual(r.state, "dialog-without-captions-tab");
  assert.ok(r.tabs.includes("Аудио"), `не показали, какие вкладки есть: ${JSON.stringify(r)}`);
}

// Кнопки ⋮ нет вовсе — в отчёте должны быть подписи кнопок, которые есть.
{
  const st = boot(DEFAULTS);
  const { root } = settingsFixture({ withMore: false });
  const r = withBody(root, () => walkToCaptions(st, root));
  assert.ok(!r.done, "доложили об успехе без кнопки «Ещё»");
  assert.strictEqual(r.state, "no-more-options-button");
}

// --- выбор языка ------------------------------------------------------------
// Какой разметкой Meet рисует список языков, снаружи не проверить, поэтому
// поддержаны все формы, которыми список вариантов вообще бывает.
{
  const st = boot(DEFAULTS);
  const sel = new N("select", {}, [
    new N("option", { value: "en" }, ["English"]),
    new N("option", { value: "ru" }, ["Русский"]),
  ]);
  const r = withBody(new N("body", {}, [sel]), () =>
    st.pickCaptionLanguage(["Русский", "Russian"]));
  assert.ok(r.ok, `не нашли язык в <select>: ${JSON.stringify(r)}`);
  assert.strictEqual(r.picked, "Русский");
  assert.strictEqual(sel.value, "ru", "значение <select> не переставлено");
  assert.ok(sel.events.includes("change"), "событие change не отправлено");
}

{
  // Список ARIA, закрытый: выпадашку надо сначала открыть.
  const st = boot(DEFAULTS);
  const opts = [
    new N("div", { role: "option" }, ["English"]),
    new N("div", { role: "option" }, ["Русский"]),
  ];
  const list = new N("div", { role: "listbox", hidden: true }, opts);
  const combo = new N("div", { role: "combobox", "aria-label": "Язык встречи" }, ["English"]);
  const root = new N("body", {}, [combo, list]);
  combo.onclick = () => { delete list.attrs.hidden; };
  const r = withBody(root, () => st.pickCaptionLanguage(["Русский", "Russian"]));
  assert.ok(r.ok, `не нашли язык в списке ARIA: ${JSON.stringify(r)}`);
  assert.strictEqual(r.picked, "Русский");
  assert.strictEqual(opts[1].clicks, 1, "по варианту не нажали");
}

{
  // Выбор спрятан за обычной кнопкой, подписанной языком: у Google так бывает.
  const st = boot(DEFAULTS);
  const opt = new N("div", { role: "menuitemradio", hidden: true }, ["Русский"]);
  const btn = new N("button", { "aria-label": "Язык встречи: English" }, ["English"]);
  const root = new N("body", {}, [btn, opt]);
  btn.onclick = () => { delete opt.attrs.hidden; };
  const r = withBody(root, () => st.pickCaptionLanguage(["Русский"]));
  assert.ok(r.ok, `не нашли язык за кнопкой: ${JSON.stringify(r)}`);
  assert.strictEqual(opt.clicks, 1);
}

{
  // Языка в списке нет — надо вернуть то, что видно, а не молчать: чинить
  // придётся по этому же логу.
  const st = boot(DEFAULTS);
  const root = new N("body", {}, [
    new N("div", { role: "listbox" }, [
      new N("div", { role: "option" }, ["English"]),
      new N("div", { role: "option" }, ["Deutsch"]),
    ]),
  ]);
  const r = withBody(root, () => st.pickCaptionLanguage(["Русский"]));
  assert.ok(!r.ok, "доложили об успехе, не найдя языка");
  assert.ok(r.options.includes("English"), `не показали варианты: ${JSON.stringify(r)}`);
}

// Каким языком Meet слушает — это надо уметь прочитать даже когда поменять не
// вышло: человек должен знать, что текст субтитров брать нельзя.
{
  const st = boot(DEFAULTS);
  const sel = new N("select", { value: "en" }, [
    new N("option", { value: "en" }, ["English"]),
    new N("option", { value: "ru" }, ["Русский"]),
  ]);
  assert.strictEqual(withBody(new N("body", {}, [sel]), () => st.captionLanguage()), "English");
}

// Meet рисует в плитке предупреждения вроде «Others might still see your full
// video.» — на живом созвоне такое попало в участники и доехало до follow-up
// наравне с человеком. Имя не предложение: знак конца и длина в словах —
// достаточный признак.
{
  const tiles = new N("body", {}, [
    new N("div", { "data-participant-id": "p1" }, [
      new N("div", {}, ["Others might still see your full video."]),
      new N("div", {}, ["Rustem Turgeldin"]),
    ]),
    new N("div", { "data-participant-id": "p2" }, [
      // Короткая подпись: по числу слов она от имени не отличается, и ловит её
      // только точка в конце. Без отдельного случая это правило не проверялось
      // бы вовсе — длинную фразу отсекает счёт слов.
      new N("div", {}, ["Запись остановлена."]),
      new N("span", {}, ["Участник Б"]),
    ]),
    new N("div", { "data-participant-id": "p3" }, [
      // Длинная подпись без точки: её ловит только счёт слов. Иначе это
      // правило доказывалось бы чужой проверкой и могло тихо исчезнуть.
      new N("div", {}, ["Others might still see your full video"]),
      new N("span", {}, ["Участник В"]),
    ]),
  ]);
  const st = boot(DEFAULTS);
  assert.deepStrictEqual(
    withBody(tiles, () => st.participants()),
    ["Rustem Turgeldin", "Участник Б", "Участник В"],
    "подпись интерфейса попала в участники");
}

console.log("meet.js: ок");
