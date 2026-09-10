// Скрипт страницы Jitsi Meet. Кладёт в window.__steno тот же набор методов,
// что и meet.js: цикл записи в meet_bot.go площадку не различает.
//
// Почему выбраны именно эти селекторы.
//
// Всё, что можно, взято из того, на что опирается собственный набор e2e-тестов
// Jitsi (tests/pageobjects/*.ts в репозитории jitsi-meet). Это самая сильная
// гарантия, доступная снаружи: переименуют — покраснеет их собственный CI,
// значит держат намеренно. Оттуда взяты data-testid="prejoin.screen" и
// "prejoin.joinMeeting", #premeeting-name-input, .lobby-screen с
// data-testid="lobby.knockButton" и "lobby.nameField", #localVideoContainer,
// #participant_<id> и #participant_<id>_name.
//
// Классы .videocontainer и .displayname — обычные, не сгенерированные: их
// ставят Thumbnail.tsx и DisplayName.tsx буквальной строкой, они пережили
// переезд Jitsi на новую систему стилей, и на них же опираются e2e-тесты.
//
// Подписи кнопок (микрофон, камера, «Ещё», субтитры) зависят от языка
// интерфейса, поэтому их списки лежат в selectors.json, а не здесь. Там же они
// и чинятся — без пересборки образа.
//
// Состояние микрофона, факт входа и список участников дополнительно
// спрашиваются у APP.conference. Это внутренний объект приложения
// (conference.js), но он же источник правды для всего интерфейса Jitsi, и для
// вопроса «молчит ли наш микрофон» подпись кнопки — слишком слабое основание.
try {
  const S = (window.__stenoSelectors && window.__stenoSelectors.jitsi) || {};

  // Записи в selectors.json — подстроки, а не регулярки: одна скобка в живой
  // подписи уронила бы весь скрипт, и бот молча ждал бы 90 секунд впустую.
  const esc = (s) => String(s).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  // Пустой массив в JS истинный, поэтому `arr || fallback` его бы не заменил,
  // а new RegExp("") совпадает со всем подряд: очищенный список означал бы
  // «подходит любая кнопка».
  const rx = (arr, fallback) => {
    const clean = (Array.isArray(arr) ? arr : []).filter(
      (s) => typeof s === "string" && s.trim() !== "");
    const list = clean.length ? clean : fallback;
    return new RegExp(list.map(esc).join("|"), "i");
  };

  const LIVE_MIC = rx(S.liveMicLabels, ["mute microphone", "выключить микрофон"]);
  const LIVE_CAM = rx(S.liveCamLabels, ["stop camera", "выключить камеру"]);
  const MUTED_MIC = rx(S.mutedMicLabels, ["unmute microphone", "включить микрофон"]);
  const MUTED_CAM = rx(S.mutedCamLabels, ["start camera", "включить камеру"]);
  const MORE_ACTIONS = rx(S.moreActionsLabels, ["more actions", "другие действия"]);
  const CC_LABEL = rx(S.captionButtonLabels,
    ["subtitles", "closed captions", "субтитры"]);
  const LEFT_TEXT = rx(S.leftMeetingTexts,
    ["kicked you out of the meeting", "you were kicked out",
      "вас удалили из конференции", "thank you for using"]);

  const visible = (el) => {
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden";
  };

  const label = (el) =>
    (el.getAttribute("aria-label") || "") + " " + (el.getAttribute("title") || "");
  const buttons = () => [...document.querySelectorAll('button,[role="button"]')].filter(visible);
  const byLabel = (re) => buttons().find((b) => re.test(label(b)));

  // Внутренний объект приложения. Может не существовать: скрипт выполняется
  // до кода страницы, а на странице закрытия его уже нет.
  const conf = () => {
    const c = window.APP && window.APP.conference;
    return c && typeof c.isLocalAudioMuted === "function" ? c : null;
  };

  const prejoin = () => document.querySelector('[data-testid="prejoin.screen"]');
  const lobby = () =>
    document.querySelector(".lobby-screen") ||
    document.querySelector('[data-testid="lobby.knockButton"]');

  // Область субтитров. Jitsi собирает её в Captions.tsx: контейнер с правилом
  // transcriptionSubtitles, внутри по <p> на реплику. Имя правила попадает в
  // сгенерированный класс, поэтому ищем по вхождению; на старых сборках это
  // был обычный класс transcription-subtitles.
  const captionRegion = () =>
    document.querySelector('[class*="transcriptionSubtitles"]') ||
    document.querySelector(".transcription-subtitles");

  // «Субтитры включены» — не то же самое, что «сейчас видна область»: между
  // репликами Jitsi её не рисует вовсе. Без этой защёлки бот, увидев пустоту,
  // жал бы кнопку снова и снова — то есть выключал бы уже включённые субтитры.
  let ccRequested = false;

  // Имя, которым бот представился. Нужно там, где список участников
  // приходится брать не из ленты: себя в нём надо вернуть ровно тем же
  // именем, иначе бот попадёт в собственный список участников.
  let myName = "";

  const leafTexts = (root) => {
    const out = [];
    const walk = (n) => {
      if (n.nodeType === 3) {
        const t = n.textContent.trim();
        if (t) out.push(t);
        return;
      }
      if (n.nodeType !== 1) return;
      for (const c of n.childNodes) walk(c);
    };
    walk(root);
    return out;
  };

  // Jitsi отдаёт реплику одной строкой «Имя: текст» — AbstractCaptions.tsx
  // склеивает их через ": ". Делим по первому такому разделителю; если его
  // нет, это реплика без имени, а не имя без реплики.
  const splitLine = (raw) => {
    const i = raw.indexOf(": ");
    if (i <= 0) return { speaker: "", text: raw.trim() };
    return { speaker: raw.slice(0, i).trim(), text: raw.slice(i + 2).trim() };
  };

  // --- кто говорит прямо сейчас ----------------------------------------------
  //
  // На публичном meet.jit.si субтитров нет вовсе (transcription.enabled=false),
  // а значит и имён говорящих взять неоткуда: whisper слышит речь, но не знает,
  // кто говорит. Доминантный говорящий — единственный источник имён на этой
  // площадке, и он же страховка на Meet, где субтитры зависят от того, тем ли
  // языком слушает распознавание.
  //
  // Спрашиваем сначала приложение, потом DOM, и порядок здесь не вкусовщина.
  // Поле dominantSpeaker в сторе — ровно то место, откуда его берут собственные
  // e2e-тесты Jitsi (tests/specs/media/activeSpeaker.spec.ts). А класс на
  // плитке отсутствует сразу в трёх законных случаях: при
  // interfaceConfig.DISABLE_DOMINANT_SPEAKER_INDICATOR, во время доставки
  // перевода (Thumbnail.tsx ставит вместо него рамку перевода) и когда плитки
  // просто нет — лента участников виртуализируется, и говорящего за пределами
  // видимой части в DOM не будет.
  //
  // "" означает «никто не говорит», null — «спросить не у кого». Разница
  // важна: на null мы идём в DOM, на "" — нет.
  const dominantFromStore = () => {
    const store = window.APP && window.APP.store;
    if (!store || typeof store.getState !== "function") return null;
    let st = null;
    try {
      st = store.getState()["features/base/participants"];
    } catch (e) {
      return null;
    }
    if (!st) return null;
    const id = st.dominantSpeaker;
    if (!id) return "";
    // Конференцию здесь берём напрямую, а не через conf(): тот считает объект
    // годным только при наличии isLocalAudioMuted, и переименование метода про
    // микрофон утащило бы за собой имена говорящих. Каждый метод спрашивается
    // сам за себя.
    const c = (window.APP && window.APP.conference) || null;
    // Себя в ленту не пускаем: молчащий бот всё равно участник, и его имя в
    // ленте — это его имя в follow-up.
    if (c && typeof c.getMyUserId === "function" && c.getMyUserId() === id) return "";
    if (st.local && st.local.id === id) return "";
    // remote — это Map, а не объект: индексация по ключу вернула бы undefined
    // всегда, и имя молча не находилось бы никогда.
    const p = st.remote && typeof st.remote.get === "function" ? st.remote.get(id) : null;
    if (p && p.name) return String(p.name).trim();
    // Имя участника в сторе бывает пустым — тогда спрашиваем конференцию.
    if (c && typeof c.getParticipantById === "function") {
      const jp = c.getParticipantById(id);
      if (jp && typeof jp.getDisplayName === "function") {
        return String(jp.getDisplayName() || "").trim();
      }
    }
    return "";
  };

  // Запасной путь: литеральный класс dominant-speaker на плитке
  // (Thumbnail.tsx). Не сгенерированный — соседний класс подсветки как раз
  // хешируется tss-react и меняется от сборки к сборке. На dominant-speaker
  // опирается собственный e2e-набор Jitsi, и сильнее гарантии снаружи не бывает.
  const dominantFromDOM = () => {
    const out = [];
    for (const tile of document.querySelectorAll(".videocontainer.dominant-speaker")) {
      if (String(tile.id || "").indexOf("localVideoContainer") === 0) continue;
      const n = tile.querySelector(".displayname");
      const t = n ? (n.innerText || "").trim() : "";
      // Одного участника Jitsi рисует дважды — в основной ленте и в stage, —
      // поэтому по имени дедуплицируем.
      if (t && t !== myName && out.indexOf(t) < 0) out.push(t);
    }
    return out;
  };

  const setInputValue = (el, value) => {
    const setter = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype, "value").set;
    setter.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
  };

  window.__steno = {
    // Имя вводится на экране перед входом и, если у комнаты есть лобби, ещё
    // раз там. Без имени бот выглядит случайным гостем, а участники должны
    // видеть, кто пишет разговор.
    setName(name) {
      // Запоминаем всегда, даже если поля нет: настроенное имя бота от этого
      // не перестаёт быть известным, а участников по нему потом отличают.
      myName = name;
      const el =
        document.querySelector("#premeeting-name-input") ||
        document.querySelector('[data-testid="lobby.nameField"]');
      if (!el) return false;
      if (el.value !== name) setInputValue(el, name);
      return true;
    },

    // Возвращает, сколько всего пришлось выключить. Ноль означает «не нашёл»,
    // а не «уже выключено», поэтому настоящее состояние отдаётся отдельно — в
    // muteState().
    muteSelf() {
      let n = 0;
      const c = conf();
      if (c) {
        if (!c.isLocalAudioMuted()) {
          c.muteAudio(true);
          n++;
        }
        if (typeof c.isLocalVideoMuted === "function" && !c.isLocalVideoMuted()) {
          c.muteVideo(true);
          n++;
        }
        return n;
      }
      // До инициализации приложения остаются кнопки. Их подпись меняется
      // вместе с состоянием: «выключить микрофон» значит, что он включён.
      //
      // Подпись «уже выключено» проверяется первой, и это не вкусовщина:
      // «Unmute microphone» содержит «mute microphone» целиком, и порядок
      // наоборот означал бы, что бот включает микрофон, думая, что выключает.
      for (const b of buttons()) {
        const l = label(b);
        if (MUTED_MIC.test(l) || MUTED_CAM.test(l)) continue;
        if (LIVE_MIC.test(l) || LIVE_CAM.test(l)) {
          b.click();
          n++;
        }
      }
      return n;
    },

    // "muted" — точно знаем, что микрофон и камера выключены; "live" — точно
    // знаем, что нет; "unknown" — спросить не у кого.
    muteState() {
      const c = conf();
      if (c && typeof c.isLocalVideoMuted === "function") {
        return c.isLocalAudioMuted() && c.isLocalVideoMuted() ? "muted" : "live";
      }
      const ls = buttons().map(label);
      const state = (mutedRe, liveRe) =>
        ls.some((l) => mutedRe.test(l)) ? "muted"
          : ls.some((l) => liveRe.test(l)) ? "live" : "";
      const mic = state(MUTED_MIC, LIVE_MIC);
      const cam = state(MUTED_CAM, LIVE_CAM);
      if (mic === "" || cam === "") return "unknown";
      return mic === "muted" && cam === "muted" ? "muted" : "live";
    },

    clickJoin() {
      const b =
        document.querySelector('[data-testid="prejoin.joinMeeting"]') ||
        document.querySelector('[data-testid="lobby.knockButton"]');
      if (!b || !visible(b)) return false;
      b.click();
      return true;
    },

    // Между экраном перед входом и звонком бывает лобби со своей кнопкой
    // «попроситься». Пока её не нажать, хост заявки не увидит — и бот
    // простоял бы там весь admission, а потом ушёл бы с «хост не впустил».
    // У Meet такого экрана нет, и метода этого в meet.js тоже нет: бот
    // спрашивает его через opt() и на отсутствие не обижается.
    //
    // Нажимать повторно безопасно: кнопка пропадает, как только заявка ушла.
    reknock() {
      const nameEl = document.querySelector('[data-testid="lobby.nameField"]');
      if (nameEl && myName && nameEl.value !== myName) setInputValue(nameEl, myName);
      const b = document.querySelector('[data-testid="lobby.knockButton"]');
      if (!b || !visible(b)) return false;
      b.click();
      return true;
    },

    // Впустили ли нас. Экран перед входом и лобби снимают признак: на них
    // разметка конференции уже может быть смонтирована.
    inCall() {
      if (prejoin() || lobby()) return false;
      const c = conf();
      if (c && typeof c.isJoined === "function") return !!c.isJoined();
      return (
        document.querySelector("#localVideoContainer") !== null ||
        document.querySelector("#videoconference_page") !== null
      );
    },

    left() {
      if (this.inCall()) return false;
      // enableClosePage уводит на отдельную страницу закрытия.
      if (/\/close\d*(\.html)?$/i.test(location.pathname)) return true;
      return LEFT_TEXT.test(document.body.innerText || "");
    },

    // Имена всех, включая себя: бот сравнивает длину этого списка с единицей,
    // чтобы понять, что остался один. Вернуть только чужих значило бы уйти с
    // разговора один на один сразу после входа.
    //
    // Себя возвращаем ровно тем именем, которым представились, а не тем, что
    // написано в плитке: к своей плитке Jitsi дописывает «(me)», и такая
    // строка не совпала бы с именем бота — он попал бы в собственный список
    // участников и в шапку follow-up.
    participants() {
      const out = [];
      let sawSelf = false;
      for (const tile of document.querySelectorAll(".videocontainer")) {
        if (String(tile.id || "").indexOf("localVideoContainer") === 0) {
          sawSelf = true;
          continue;
        }
        const n = tile.querySelector(".displayname");
        const t = n ? (n.innerText || "").trim() : "";
        if (t) out.push(t);
      }
      if (!out.length) {
        // Лента участников бывает свёрнута или отключена настройкой.
        const c = conf();
        if (c && typeof c.listMembers === "function") {
          for (const m of c.listMembers()) {
            const t = m && typeof m.getDisplayName === "function" ? m.getDisplayName() : "";
            if (t) out.push(t);
          }
        }
      }
      if ((sawSelf || this.inCall()) && myName) out.push(myName);
      return out;
    },

    // Кого Jitsi считает говорящим прямо сейчас. Отрезки «с какой по какую
    // секунду говорил кто» собираются в Go (speakers.go): здесь состояния нет,
    // поэтому перезагрузка страницы ничего не ломает.
    speaking() {
      const one = dominantFromStore();
      if (one !== null) return one === "" ? [] : [one];
      return dominantFromDOM();
    },

    // Текущие видимые строки субтитров. Сшивку в реплики делает Go: здесь нет
    // состояния, поэтому перезагрузка страницы ничего не ломает.
    captions() {
      const region = captionRegion();
      if (!region) return { ok: false, lines: [] };
      const lines = [];
      for (const p of region.querySelectorAll("p")) {
        const raw = leafTexts(p).join(" ").trim();
        if (raw) lines.push(splitLine(raw));
      }
      return { ok: true, lines };
    },

    captionsOn() {
      return ccRequested || captionRegion() !== null;
    },

    // Знает ли сама страница, что субтитров не будет. Публичный meet.jit.si
    // отдаёт в config.js transcription.enabled=false и
    // disableClosedCaptions=true — жать там нечего, и «не нашли кнопку» надо
    // отличать от «кнопки и не было».
    captionsUnavailable() {
      const c = window.config;
      if (!c) return false; // конфиг ещё не загрузился — не наговаривать
      const t = c.transcription;
      if (t && (t.enabled === false || t.disableClosedCaptions === true)) return true;
      if (Array.isArray(c.toolbarButtons) && c.toolbarButtons.indexOf("closedcaptions") < 0) {
        return true;
      }
      return false;
    },

    // Одна попытка переключить субтитры. Кнопка чаще всего спрятана в меню
    // «Ещё», а открыть меню и нажать в нём за один такт нельзя: меню рисуется
    // не мгновенно. Поэтому возвращаем, что успели сделать, — бот позовёт ещё
    // раз через полторы секунды.
    toggleCaptions() {
      const cc = byLabel(CC_LABEL);
      if (cc) {
        cc.click();
        ccRequested = !ccRequested;
        return "clicked";
      }
      const more = byLabel(MORE_ACTIONS);
      if (more) {
        more.click();
        return "menu";
      }
      return "";
    },

    // Заглушки: язык распознавания Jitsi берёт из настроек сервера, отдельного
    // выбора для гостя в интерфейсе нет.
    openCaptionSettings() { return false; },
    pickCaptionLanguage() { return { ok: false }; },
    closeDialog() { return false; },

    // Отладка: что на странице похоже на субтитры и на участников. Чинить
    // съём вслепую, тратя на попытку по заходу в живой звонок, невозможно.
    debugCaptions() {
      const region = captionRegion();
      return {
        regions: region
          ? [{
            label: String(region.getAttribute("class") || ""),
            matches: true,
            children: region.children.length,
            sample: (region.innerText || "").slice(0, 120),
          }]
          : [],
        labelled: [],
        jsnames: [],
        participants: document.querySelectorAll(".videocontainer").length,
        captionButtons: buttons()
          .map(label)
          .filter((l) => CC_LABEL.test(l) || MORE_ACTIONS.test(l))
          .slice(0, 6),
      };
    },

    // Отладка подсветки: видно ли стор, что в нём лежит и что говорит DOM.
    // Без этого «имён нет» неотличимо от «в звонке молчали».
    debugSpeaking() {
      const store = window.APP && window.APP.store;
      let hasStore = false;
      let dominant = "";
      if (store && typeof store.getState === "function") {
        try {
          const st = store.getState()["features/base/participants"];
          hasStore = !!st;
          dominant = (st && st.dominantSpeaker) || "";
        } catch (e) {
          hasStore = false;
        }
      }
      return {
        how: hasStore ? "store" : "dom",
        store: hasStore,
        dominantId: String(dominant),
        speaking: this.speaking(),
        tiles: [...document.querySelectorAll(".videocontainer")].slice(0, 8).map((t) => ({
          id: String(t.id || ""),
          classes: String(t.getAttribute("class") || ""),
        })),
      };
    },
  };
} catch (e) {
  // Лучше внятная строчка в логе бота, чем неопределённый window.__steno и
  // загадочный таймаут через полторы минуты.
  window.__stenoError = String((e && e.message) || e);
}
