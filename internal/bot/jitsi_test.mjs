// Проверка jitsi.js на синтетическом DOM. Настоящий браузер для этого не
// нужен и не запускается: фикстуры собраны из того, что рисует Jitsi, —
// экран перед входом, лобби, конференция, страница после исключения.
// Запуск: node jitsi_test.mjs (нужен только node).
import assert from "node:assert";
import { readFileSync } from "node:fs";

// --- минимальный DOM -------------------------------------------------------

// Поддерживаются ровно те формы селекторов, которыми пользуется jitsi.js:
// тег, #id, .класс, [атрибут="значение"], [атрибут*="подстрока"] и
// перечисление через запятую. Больше ему не нужно — и это не случайность:
// сложный селектор в скрипте страницы означает привязку к вёрстке, а не к
// атрибутам, и ломается на первом же релизе.
function matchOne(n, sel) {
  const re = /([a-zA-Z][\w-]*)|#([\w-]+)|\.([\w-]+)|\[([\w-]+)(?:(\*?=)"([^"]*)")?\]/g;
  let m;
  let ok = true;
  let any = false;
  while ((m = re.exec(sel))) {
    any = true;
    if (m[1]) ok = ok && n.tagName === m[1].toUpperCase();
    else if (m[2]) ok = ok && n.getAttribute("id") === m[2];
    else if (m[3]) {
      ok = ok && String(n.getAttribute("class") || "").split(/\s+/).includes(m[3]);
    } else if (m[4]) {
      const v = n.getAttribute(m[4]);
      if (v === null) ok = false;
      else if (m[5] === "=") ok = ok && v === m[6];
      else if (m[5] === "*=") ok = ok && v.includes(m[6]);
    }
  }
  return any && ok;
}

class N {
  constructor(tag, attrs = {}, kids = []) {
    this.tagName = tag.toUpperCase();
    this.nodeType = 1;
    this.attrs = attrs;
    this.clicks = 0;
    this.childNodes = kids.map((k) => (typeof k === "string" ? new T(k) : k));
    this.children = this.childNodes.filter((k) => k.nodeType === 1);
    for (const k of this.childNodes) k.parent = this;
  }
  getAttribute(n) {
    return this.attrs[n] ?? null;
  }
  // Браузер отдаёт id и class ещё и свойствами — фикстура должна вести себя
  // так же, иначе тест разрешит коду то, чего живой DOM не разрешит.
  get id() {
    return this.attrs.id || "";
  }
  get className() {
    return this.attrs.class || "";
  }
  getBoundingClientRect() {
    return this.attrs.hidden ? { width: 0, height: 0 } : { width: 100, height: 20 };
  }
  get textContent() {
    return this.childNodes.map((c) => c.textContent).join("");
  }
  get innerText() {
    return this.textContent;
  }
  get value() {
    return this._value !== undefined ? this._value : this.attrs.value || "";
  }
  set value(v) {
    this._value = v;
  }
  click() {
    this.clicks++;
  }
  dispatchEvent() {
    return true;
  }
  *walk() {
    yield this;
    for (const c of this.children) yield* c.walk();
  }
  querySelectorAll(sel) {
    const parts = sel.split(",").map((s) => s.trim()).filter(Boolean);
    const out = [];
    for (const n of this.walk()) {
      if (n === this) continue;
      if (parts.some((p) => matchOne(n, p))) out.push(n);
    }
    return out;
  }
  querySelector(sel) {
    return this.querySelectorAll(sel)[0] || null;
  }
}

class T {
  constructor(t) {
    this.nodeType = 3;
    this.textContent = t;
    this.childNodes = [];
    this.children = [];
  }
  *walk() {}
}

globalThis.Event = class {
  constructor(type) {
    this.type = type;
  }
};
globalThis.getComputedStyle = () => ({ visibility: "visible" });
globalThis.location = { pathname: "/Planerka" };
globalThis.window = { HTMLInputElement: { prototype: {} } };
Object.defineProperty(window.HTMLInputElement.prototype, "value", {
  configurable: true,
  get() {
    return this._value;
  },
  set(v) {
    this._value = v;
  },
});

let currentBody = null;
globalThis.document = {
  get body() {
    return currentBody;
  },
  querySelectorAll: (s) => currentBody.querySelectorAll(s),
  querySelector: (s) => currentBody.querySelector(s),
};

const DEFAULTS = JSON.parse(readFileSync("selectors.json", "utf8"));
const SRC = readFileSync("jitsi.js", "utf8");

function boot(body, selectors = DEFAULTS) {
  currentBody = body;
  window.__stenoSelectors = selectors;
  window.__steno = undefined;
  window.__stenoError = undefined;
  window.APP = undefined;
  window.config = undefined;
  location.pathname = "/Planerka";
  eval(SRC);
  assert.strictEqual(window.__stenoError, undefined, "скрипт упал: " + window.__stenoError);
  return window.__steno;
}

function setBody(body) {
  currentBody = body;
}

// --- фикстуры ---------------------------------------------------------------

// Экран перед входом. Атрибуты — те, на которые опирается собственный
// e2e-набор Jitsi (tests/pageobjects/PreJoinScreen.ts).
// Разметка конференции вокруг него настоящая: Conference.tsx монтирует
// #videoconference_page и рисует экран перед входом внутри него. Без этого
// проверка «это ещё не звонок» ничего бы не доказывала.
const prejoinBody = () =>
  new N("body", {}, [
    new N("div", { id: "videoconference_page" }, [
      new N("div", { "data-testid": "prejoin.screen" }, [
        new N("input", { id: "premeeting-name-input", value: "" }),
        new N("div", { role: "button", "aria-label": "Mute microphone" }, ["mic"]),
        new N("div", { role: "button", "aria-label": "Stop camera" }, ["cam"]),
        new N("button", { "data-testid": "prejoin.joinMeeting" }, ["Join meeting"]),
      ]),
    ]),
  ]);

// Лобби: комнату закрыли, ждём модератора (tests/pageobjects/LobbyScreen.ts).
const lobbyBody = () =>
  new N("body", {}, [
    new N("div", { id: "videoconference_page" }, [
      new N("div", { class: "lobby-screen" }, [
        new N("input", { "data-testid": "lobby.nameField", value: "" }),
        new N("button", { "data-testid": "lobby.knockButton" }, ["Ask to Join"]),
        new N("div", {}, ["You'll join the meeting as soon as someone accepts your request"]),
      ]),
    ]),
  ]);

// Конференция: лента участников. Своя плитка — #localVideoContainer, чужие —
// #participant_<id>; имя лежит в .displayname (Thumbnail.tsx, DisplayName.tsx).
// К своему имени Jitsi дописывает «(me)» — оно не должно уехать в участники.
const inCallBody = ({ captions = null, kicked = false } = {}) => {
  const kids = [
    new N("div", { id: "videoconference_page" }, [
      new N("div", { class: "filmstrip" }, [
        new N("span", { id: "localVideoContainer", class: "videocontainer" }, [
          new N("span", { id: "localDisplayName", class: "displayname" }, [
            "Steno · идёт запись (me)",
          ]),
        ]),
        new N("span", { id: "participant_aaa", class: "videocontainer" }, [
          new N("span", { id: "participant_aaa_name", class: "displayname" }, ["Участник А"]),
        ]),
        new N("span", { id: "participant_bbb", class: "videocontainer" }, [
          new N("span", { id: "participant_bbb_name", class: "displayname" }, ["Участник Б"]),
        ]),
      ]),
      new N("div", { class: "toolbox-content-items" }, [
        new N("div", { role: "button", "aria-label": "Unmute microphone" }, ["mic"]),
        new N("div", { role: "button", "aria-label": "Start camera" }, ["cam"]),
        new N("div", { role: "button", "aria-label": "More actions" }, ["…"]),
      ]),
    ]),
  ];
  if (captions) kids.push(captions);
  if (kicked) {
    return new N("body", {}, [
      new N("div", {}, ["Ouch! Участник А kicked you out of the meeting"]),
    ]);
  }
  return new N("body", {}, kids);
};

// Субтитры: Captions.tsx рисует контейнер с правилом transcriptionSubtitles,
// внутри по <p> на реплику, а текст уже склеен как «Имя: реплика».
const captionsBox = (lines) =>
  new N("div", { class: "tss-1qq8vmq-transcriptionSubtitles" },
    lines.map((l) => new N("p", {}, [new N("span", {}, [l])])));

// --- экран перед входом ------------------------------------------------------
{
  const body = prejoinBody();
  const steno = boot(body);
  assert.strictEqual(steno.inCall(), false, "на экране перед входом мы ещё не в звонке");

  assert.strictEqual(steno.setName("Steno · идёт запись"), true, "поле имени не нашлось");
  const input = body.querySelector("#premeeting-name-input");
  assert.strictEqual(input.value, "Steno · идёт запись");

  // Микрофон и камера на этом экране включены — подписи «выключить …».
  assert.strictEqual(steno.muteState(), "live");
  assert.strictEqual(steno.muteSelf(), 2, "должны были нажать обе кнопки");
  assert.strictEqual(steno.clickJoin(), true);
  assert.strictEqual(
    body.querySelector('[data-testid="prejoin.joinMeeting"]').clicks, 1,
    "нажали не ту кнопку");
}

// --- лобби -------------------------------------------------------------------
{
  const body = lobbyBody();
  const steno = boot(body);
  // Лобби — это ещё не звонок: если считать его входом, бот начнёт писать
  // тишину и через EmptyFor уйдёт, решив, что остался один.
  assert.strictEqual(steno.inCall(), false, "лобби посчитали звонком");
  assert.strictEqual(steno.setName("Steno"), true, "поле имени в лобби не нашлось");
  assert.strictEqual(steno.clickJoin(), true);
  assert.strictEqual(body.querySelector('[data-testid="lobby.knockButton"]').clicks, 1);

  // Заявку надо подать и тогда, когда лобби появилось уже после входа: без
  // этого бот простоял бы в нём весь admission и ушёл бы с «хост не впустил»,
  // хотя хосту никакой заявки и не показали.
  const fresh = lobbyBody();
  const steno2 = boot(fresh);
  steno2.setName("Steno");
  setBody(fresh);
  assert.strictEqual(steno2.reknock(), true, "не попросились в звонок из лобби");
  assert.strictEqual(fresh.querySelector('[data-testid="lobby.knockButton"]').clicks, 1);
  assert.strictEqual(fresh.querySelector('[data-testid="lobby.nameField"]').value, "Steno",
    "в лобби надо представиться заново — поле там своё");
  // В самом звонке просить уже не у кого.
  setBody(inCallBody());
  assert.strictEqual(steno2.reknock(), false);
}

// --- в звонке ----------------------------------------------------------------
{
  const pre = prejoinBody();
  const steno = boot(pre);
  steno.setName("Steno · идёт запись");
  setBody(inCallBody());

  assert.strictEqual(steno.inCall(), true, "не увидели, что уже в звонке");
  assert.strictEqual(steno.left(), false);

  // Себя возвращаем ровно тем именем, которым представились: в плитке к нему
  // приписано «(me)», и такой строкой бот попал бы в собственный список
  // участников — и в шапку follow-up.
  assert.deepStrictEqual(steno.participants(),
    ["Участник А", "Участник Б", "Steno · идёт запись"]);

  // Микрофон и камера выключены — подписи сменились на «включить …».
  assert.strictEqual(steno.muteState(), "muted");
  assert.strictEqual(steno.muteSelf(), 0, "выключать было нечего");
}

// --- разговор один на один ---------------------------------------------------
{
  // Собеседник один. Если бы себя в списке не было, длина стала бы единицей —
  // и бот ушёл бы с разговора один на один, решив, что остался сам.
  const pre = prejoinBody();
  const steno = boot(pre);
  steno.setName("Steno");
  const body = inCallBody();
  const film = body.querySelector(".filmstrip");
  film.children = film.children.filter((c) => c.attrs.id !== "participant_bbb");
  film.childNodes = film.children;
  setBody(body);
  assert.strictEqual(steno.participants().length, 2,
    "в разговоре один на один бот должен видеть двоих: собеседника и себя");
}

// --- субтитры ----------------------------------------------------------------
{
  const steno = boot(inCallBody({
    captions: captionsBox([
      "Участник А: давайте начнём с релиза",
      "Участник Б: я закончу миграцию к пятнице",
      "просто реплика без имени",
    ]),
  }));
  const c = steno.captions();
  assert.ok(c.ok, "область субтитров не нашлась");
  assert.deepStrictEqual(c.lines, [
    { speaker: "Участник А", text: "давайте начнём с релиза" },
    { speaker: "Участник Б", text: "я закончу миграцию к пятнице" },
    // Двоеточия нет — значит имени нет. Отдать первое слово за имя было бы
    // хуже пустого имени: в follow-up появился бы несуществующий участник.
    { speaker: "", text: "просто реплика без имени" },
  ]);
  assert.strictEqual(steno.captionsOn(), true);
}

// --- субтитров нет вовсе -----------------------------------------------------
{
  const steno = boot(inCallBody());
  assert.deepStrictEqual(steno.captions(), { ok: false, lines: [] });
  assert.strictEqual(steno.captionsOn(), false);

  // Публичный meet.jit.si честно пишет в config.js, что субтитров не будет.
  // Это не поломка вёрстки, и жать там нечего.
  assert.strictEqual(steno.captionsUnavailable(), false, "без конфига не наговариваем");
  window.config = { transcription: { enabled: false, disableClosedCaptions: true } };
  assert.strictEqual(steno.captionsUnavailable(), true);
  window.config = { transcription: { enabled: true, disableClosedCaptions: false } };
  assert.strictEqual(steno.captionsUnavailable(), false);
  // Кнопку убрали из панели — включать нечем.
  window.config = { toolbarButtons: ["microphone", "camera", "hangup"] };
  assert.strictEqual(steno.captionsUnavailable(), true);
}

// --- включение субтитров -----------------------------------------------------
{
  // Кнопки субтитров на виду нет, есть только «Ещё»: сначала открываем меню.
  const body = inCallBody();
  const steno = boot(body);
  assert.strictEqual(steno.toggleCaptions(), "menu");
  assert.strictEqual(
    body.querySelectorAll('[aria-label="More actions"]')[0].clicks, 1);
  // Защёлка не должна взводиться от открытия меню: иначе бот решит, что
  // субтитры включены, и больше к ним не вернётся.
  assert.strictEqual(steno.captionsOn(), false, "открытие меню — ещё не включение");

  // Меню открылось, кнопка появилась.
  const cc = new N("div", { role: "button", "aria-label": "Subtitles" }, ["Subtitles"]);
  body.children.push(cc);
  body.childNodes.push(cc);
  assert.strictEqual(steno.toggleCaptions(), "clicked");
  assert.strictEqual(cc.clicks, 1);
  // Область субтитров между репликами не рисуется. Без защёлки бот увидел бы
  // пустоту и нажал бы кнопку второй раз — то есть выключил бы субтитры.
  assert.strictEqual(steno.captionsOn(), true, "защёлка не взвелась");
}

// --- нас вывели из звонка ----------------------------------------------------
{
  const steno = boot(inCallBody({ kicked: true }));
  assert.strictEqual(steno.inCall(), false);
  assert.strictEqual(steno.left(), true, "не узнали текст об исключении из звонка");
}

// --- страница закрытия -------------------------------------------------------
{
  const steno = boot(new N("body", {}, [new N("div", {}, ["Bye"])]));
  location.pathname = "/close3.html";
  assert.strictEqual(steno.left(), true, "не узнали страницу закрытия");
}

// --- APP.conference главнее подписей -----------------------------------------
{
  // Подписи кнопок зависят от языка интерфейса, а вопрос «молчит ли наш
  // микрофон» слишком дорог, чтобы решать его переводом. Если приложение
  // отвечает само — верим ему.
  const body = inCallBody(); // подписи говорят «выключены»
  const steno = boot(body);
  let mutedAudio = false;
  let mutedVideo = false;
  window.APP = {
    conference: {
      isLocalAudioMuted: () => mutedAudio,
      isLocalVideoMuted: () => mutedVideo,
      muteAudio: () => { mutedAudio = true; },
      muteVideo: () => { mutedVideo = true; },
      isJoined: () => true,
      listMembers: () => [{ getDisplayName: () => "Участник А" }],
    },
  };
  assert.strictEqual(steno.muteState(), "live",
    "поверили подписи кнопок вместо самого приложения");
  assert.strictEqual(steno.muteSelf(), 2);
  assert.strictEqual(steno.muteState(), "muted");
}

// --- кто говорит прямо сейчас ------------------------------------------------
// На публичном meet.jit.si субтитров нет вовсе, и доминантный говорящий —
// единственный источник имён: whisper слышит речь, но не знает, кто говорит.
//
// Спрашиваем сначала стор приложения, и это не вкусовщина. Класс на плитке
// отсутствует в трёх законных случаях: при
// interfaceConfig.DISABLE_DOMINANT_SPEAKER_INDICATOR, во время доставки
// перевода и когда плитки просто нет — лента участников виртуализируется.

// Стор Jitsi: срез features/base/participants. remote — это Map, а не объект;
// фикстура повторяет это буквально, иначе тест разрешил бы коду индексацию по
// ключу, которой живой стор не разрешит.
const store = (dominant, { local = { id: "me", name: "Steno" }, remote = [] } = {}) => ({
  getState: () => ({
    "features/base/participants": {
      dominantSpeaker: dominant,
      local,
      remote: new Map(remote.map((p) => [p.id, p])),
    },
  }),
});

// Говорит один.
{
  const steno = boot(inCallBody());
  steno.setName("Steno · идёт запись");
  window.APP = {
    store: store("aaa", { remote: [{ id: "aaa", name: "Участник А" }] }),
    conference: { getMyUserId: () => "me" },
  };
  assert.deepStrictEqual(steno.speaking(), ["Участник А"]);
}

// Говорят двое подряд: лента должна ехать за сменой доминантного.
{
  const steno = boot(inCallBody());
  const remote = [{ id: "aaa", name: "Участник А" }, { id: "bbb", name: "Участник Б" }];
  window.APP = { store: store("aaa", { remote }), conference: { getMyUserId: () => "me" } };
  assert.deepStrictEqual(steno.speaking(), ["Участник А"]);
  window.APP = { store: store("bbb", { remote }), conference: { getMyUserId: () => "me" } };
  assert.deepStrictEqual(steno.speaking(), ["Участник Б"], "смена говорящего не заметна");
}

// Никто не говорит. Это самое частое состояние созвона, и ошибка здесь
// размазала бы одно имя по всей расшифровке.
{
  const steno = boot(inCallBody());
  window.APP = { store: store(undefined), conference: { getMyUserId: () => "me" } };
  assert.deepStrictEqual(steno.speaking(), []);
}

// Говорит сам бот. Он сидит молча, но участником быть не перестаёт, и его имя
// в ленте — это его имя в follow-up. Себя узнаём двумя способами, и проверять
// их надо по отдельности: иначе одна оставшаяся защита прикрывает поломку
// другой, и обе выглядят рабочими.

// В обоих случаях конференция готова назвать имя бота. Так и надо проверять:
// то, что lib-jitsi-meet сегодня не отдаёт локального участника по id, —
// подробность реализации, а не договор. Начнёт отдавать — и между ботом и его
// собственным именем в follow-up останется только эта проверка.
const selfNamer = { getDisplayName: () => "Steno · идёт запись" };

// Только по id конференции: срез local в сторе бывает пустым, пока участник
// не доехал.
{
  const steno = boot(inCallBody());
  steno.setName("Steno · идёт запись");
  window.APP = {
    // null, а не undefined: undefined в деструктуризации подменяется
    // значением по умолчанию, и срез local остался бы на месте — проверка
    // «узнаём ли себя по id конференции» тогда ничего бы не проверяла.
    store: store("me", { local: null }),
    conference: {
      getMyUserId: () => "me",
      getParticipantById: (id) => (id === "me" ? selfNamer : null),
    },
  };
  assert.deepStrictEqual(steno.speaking(), [],
    "бот попал в собственную ленту говорящих: не спросили свой id у конференции");
}

// Только по своему участнику в сторе: APP.conference доезжает позже стора, и
// getMyUserId на первых опросах ещё не отвечает.
{
  const steno = boot(inCallBody());
  steno.setName("Steno · идёт запись");
  window.APP = {
    store: store("me", { local: { id: "me", name: "Steno · идёт запись" } }),
    conference: { getParticipantById: (id) => (id === "me" ? selfNamer : null) },
  };
  assert.deepStrictEqual(steno.speaking(), [],
    "бот попал в собственную ленту говорящих: не узнали себя по срезу local");
}

// Стор говорит «никто не говорит», а класс на плитке ещё висит: подсветка
// гаснет не мгновенно. Верить надо стору — иначе имя замолчавшего человека
// заедет на следующего.
{
  const body = inCallBody();
  const steno = boot(body);
  body.querySelector("#participant_aaa").attrs.class = "videocontainer dominant-speaker";
  window.APP = { store: store(undefined), conference: { getMyUserId: () => "me" } };
  assert.deepStrictEqual(steno.speaking(), [],
    "поверили залипшему классу вместо стора");
}

// Имени в сторе нет — спрашиваем конференцию. Пустое имя в сторе бывает, пока
// участник не представился, а имя нам нужно любое настоящее.
{
  const steno = boot(inCallBody());
  window.APP = {
    store: store("aaa", { remote: [{ id: "aaa" }] }),
    conference: {
      getMyUserId: () => "me",
      getParticipantById: (id) => (id === "aaa" ? { getDisplayName: () => "Участник А" } : null),
    },
  };
  assert.deepStrictEqual(steno.speaking(), ["Участник А"],
    "не спросили имя у конференции, когда в сторе его нет");
}

// Стора нет вовсе — остаётся класс dominant-speaker на плитке. Литерал из
// Thumbnail.tsx, на который опирается собственный e2e-набор Jitsi.
{
  const body = inCallBody();
  const steno = boot(body);
  steno.setName("Steno · идёт запись");
  const film = body.querySelector(".filmstrip");
  film.querySelector("#participant_aaa").attrs.class = "videocontainer dominant-speaker";
  assert.deepStrictEqual(steno.speaking(), ["Участник А"],
    "не нашли говорящего по классу плитки");
}

// Своя плитка тоже получает этот класс — и её надо пропустить: она
// #localVideoContainer, а не #participant_<id>.
{
  const body = inCallBody();
  const steno = boot(body);
  steno.setName("Steno · идёт запись");
  body.querySelector("#localVideoContainer").attrs.class = "videocontainer dominant-speaker";
  assert.deepStrictEqual(steno.speaking(), [],
    "бот попал в ленту через свою плитку");
}

// Одного участника Jitsi рисует дважды — в основной ленте и в stage. Оба узла
// получают класс, а человек в ленте должен быть один.
{
  const body = inCallBody();
  const steno = boot(body);
  const film = body.querySelector(".filmstrip");
  film.querySelector("#participant_aaa").attrs.class = "videocontainer dominant-speaker";
  const stage = new N("span", { id: "participant_aaa_stage", class: "videocontainer dominant-speaker" }, [
    new N("span", { class: "displayname" }, ["Участник А"]),
  ]);
  film.children.push(stage);
  film.childNodes.push(stage);
  assert.deepStrictEqual(steno.speaking(), ["Участник А"],
    "один говорящий попал в ленту дважды");
}

// Подсветки нет ни на одной плитке — пусто, а не «все».
{
  const steno = boot(inCallBody());
  assert.deepStrictEqual(steno.speaking(), []);
}

// --- мусор в selectors.json --------------------------------------------------
{
  // Скобка в подписи — обычное дело. Раньше такая строка роняла весь скрипт:
  // window.__steno не определялся, и бот умирал через 90 секунд.
  const sel = { ...DEFAULTS, jitsi: { ...DEFAULTS.jitsi, captionButtonLabels: ["Субтитры (бета"] } };
  const steno = boot(inCallBody(), sel);
  assert.ok(steno, "скрипт упал на метасимволе: " + window.__stenoError);
  assert.strictEqual(steno.toggleCaptions(), "menu", "мусорная подпись нашла кнопку субтитров");
}

// --- очищенный список --------------------------------------------------------
{
  // Пустой массив в JS истинный, а new RegExp("") совпадает со всем подряд.
  // Проверяем там, где регулярка действительно спрашивается: на странице без
  // конференции, где решается «нас вывели или нет».
  for (const junk of [[], ["", "   "]]) {
    const sel = { ...DEFAULTS, jitsi: { ...DEFAULTS.jitsi, leftMeetingTexts: junk } };
    const steno = boot(new N("body", {}, [new N("div", {}, ["Обычный текст страницы"])]), sel);
    assert.strictEqual(steno.left(), false,
      `список ${JSON.stringify(junk)} совпал со всем подряд — бот вышел бы на первом опросе`);
  }
}

// --- договор между Go и страницей --------------------------------------------
// pollJS живёт в meet_bot.go и разбирается в структуру pollState. Имена полей
// заданы в двух местах сразу и могут разъехаться молча: бот получил бы нули и
// вышел бы из звонка «потому что нас там нет». Здесь скрипт проверяется на
// живой фикстуре, а разбор его вывода в Go — в TestPollJSContract.
const POLL_JS = (() => {
  const go = readFileSync("meet_bot.go", "utf8");
  const m = go.match(/const pollJS = `([\s\S]*?)`/);
  assert.ok(m, "не нашёл pollJS в meet_bot.go");
  return m[1];
})();

function pollOnFixture() {
  const body = inCallBody({ captions: captionsBox(["Участник А: привет"]) });
  const steno = boot(body);
  steno.setName("Steno · идёт запись");
  window.config = { transcription: { enabled: false } };
  // Подсветка говорящего — второй источник имён, и до Go он должен доезжать
  // так же, как реплики: имя поля задано дважды, в pollJS и в теге pollState.
  body.querySelector("#participant_aaa").attrs.class = "videocontainer dominant-speaker";
  return eval(POLL_JS);
}

{
  const st = pollOnFixture();
  assert.strictEqual(st.inCall, true);
  assert.strictEqual(st.left, false);
  assert.strictEqual(st.captionsOn, true);
  assert.strictEqual(st.captionsUnavailable, true);
  assert.strictEqual(st.muteState, "muted");
  assert.deepStrictEqual(st.lines, [{ speaker: "Участник А", text: "привет" }]);
  assert.deepStrictEqual(st.speaking, ["Участник А"]);
  assert.ok(st.participants.includes("Участник А"));
}

// Тот же опрос на странице без window.__steno: скрипт мог не выполниться, и
// падать на этом бот не должен — он и так это заметит по inCall.
{
  currentBody = new N("body", {}, []);
  window.__steno = undefined;
  const st = eval(POLL_JS);
  assert.strictEqual(st.inCall, false);
  assert.deepStrictEqual(st.lines, []);
  assert.deepStrictEqual(st.speaking, [], "без скрипта страницы лента должна быть пустой");
}

if (process.argv.includes("--print-poll")) {
  // Режим для Go-теста: печатаем только результат опроса, чтобы его разобрали
  // в ту самую структуру, ради которой всё это писалось.
  console.log(JSON.stringify(pollOnFixture()));
} else {
  console.log("jitsi.js: все проверки прошли");
}
