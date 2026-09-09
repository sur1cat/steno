// Проверка meet.js на синтетическом DOM: настоящий браузер для этого не нужен,
// а сломанный разбор строк субтитров стоит дорого — без имён follow-up резко
// теряет ценность. Запуск: node meet_test.mjs (нужен только node).
import assert from "node:assert";
import { readFileSync } from "node:fs";

// --- минимальный DOM -------------------------------------------------------
class N {
  constructor(tag, attrs = {}, kids = []) {
    this.tagName = tag.toUpperCase();
    this.nodeType = 1;
    this.attrs = attrs;
    this.childNodes = kids.map((k) => (typeof k === "string" ? new T(k) : k));
    this.children = this.childNodes.filter((k) => k.nodeType === 1);
    for (const k of this.childNodes) k.parent = this;
  }
  get alt() { return this.attrs.alt || ""; }
  getAttribute(n) { return this.attrs[n] ?? null; }
  getBoundingClientRect() { return { width: 100, height: 20 }; }
  get textContent() { return this.childNodes.map((c) => c.textContent).join(""); }
  // left() читает именно innerText — без него проверка ухода из звонка
  // всегда работала по пустой строке и ничего не доказывала.
  get innerText() { return this.textContent; }
  *walk() { yield this; for (const c of this.children) yield* c.walk(); }
  querySelectorAll(sel) {
    const m = sel.match(/^\[role="region"\]\[aria-label\]$/);
    if (m) return [...this.walk()].filter(
      (n) => n.getAttribute("role") === "region" && n.getAttribute("aria-label") !== null);
    if (sel === "[data-participant-id]")
      return [...this.walk()].filter((n) => n.getAttribute("data-participant-id") !== null);
    return [];
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}
class T {
  constructor(t) { this.nodeType = 3; this.textContent = t; this.childNodes = []; this.children = []; }
  *walk() {}
}

const captionRegion = new N("div", { role: "region", "aria-label": "Субтитры" }, [
  new N("div", {}, [
    new N("div", {}, [new N("img", { alt: "Аня Смирнова" }), new N("div", {}, ["Аня Смирнова"])]),
    new N("div", {}, [new N("div", {}, ["давайте начнём с релиза"])]),
  ]),
  new N("div", {}, [
    new N("div", {}, ["Боря"]),
    new N("div", {}, [new N("div", {}, ["я закончу "]), new N("div", {}, ["миграцию к пятнице"])]),
  ]),
]);

const body = new N("body", {}, [
  captionRegion,
  new N("div", { "data-participant-id": "p1" }, [new N("div", {}, ["Аня Смирнова"])]),
  new N("div", { "data-participant-id": "p2" }, [new N("div", {}, ["Боря"])]),
]);

globalThis.window = { HTMLInputElement: { prototype: {} } };
globalThis.document = {
  body,
  querySelectorAll: (s) => body.querySelectorAll(s),
  querySelector: (s) => body.querySelector(s),
};
globalThis.getComputedStyle = () => ({ visibility: "visible" });
const DEFAULTS = JSON.parse(readFileSync("selectors.json", "utf8"));
const SRC = readFileSync("meet.js", "utf8");

function boot(selectors) {
  window.__stenoSelectors = selectors;
  window.__steno = undefined;
  window.__stenoError = undefined;
  eval(SRC);
  return window.__steno;
}

// --- обычный случай ---------------------------------------------------------
const steno = boot(DEFAULTS);
const c = steno.captions();
assert.ok(c.ok, "область субтитров не нашлась по aria-label");
assert.deepStrictEqual(c.lines, [
  // Имя приходит и как alt аватарки, и как подпись — дубль не должен
  // попасть в текст реплики.
  { speaker: "Аня Смирнова", text: "давайте начнём с релиза" },
  { speaker: "Боря", text: "я закончу миграцию к пятнице" },
]);
assert.deepStrictEqual(steno.participants(), ["Аня Смирнова", "Боря"]);
assert.strictEqual(steno.inCall(), true);
assert.strictEqual(steno.left(), false);
assert.strictEqual(steno.captionsOn(), true);

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

console.log("meet.js: ок");
