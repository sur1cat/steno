// Инжектится один раз после загрузки Meet и живёт в window.__steno.
// Всё, что здесь есть, опирается на ARIA-атрибуты и структуру, а не на классы:
// классы в Meet обфусцированы и меняются на каждом релизе.
try {
  const S = window.__stenoSelectors || {};

  // Записи в selectors.json — обычные подстроки, а не регулярки. Без
  // экранирования одна скобка в реальной подписи Meet («Субтитры (бета»)
  // роняла бы весь скрипт, и бот молча ждал бы 90 секунд впустую.
  const esc = (s) => String(s).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  // Пустой массив в JS истинный, поэтому `arr || fallback` его бы не заменил,
  // а new RegExp("") совпадает со всем подряд: очищенный список означал бы
  // «подходит любая кнопка».
  const rx = (arr, fallback) => {
    // Пустые строки выкидываются: одна такая в списке давала регулярку,
    // совпадающую со всем подряд, — а это «любая кнопка подходит» и
    // «мы уже вышли из звонка» на первом же опросе.
    const clean = (Array.isArray(arr) ? arr : []).filter(
      (s) => typeof s === "string" && s.trim() !== "");
    const list = clean.length ? clean : fallback;
    return new RegExp(list.map(esc).join("|"), "i");
  };

  const CAPTION_LABEL = rx(S.captionRegionLabels, ["caption", "субтитр"]);
  const CAPTION_SETTINGS = rx(S.captionSettingsLabels, ["caption settings", "настройки субтитр"]);
  const MORE_OPTIONS = rx(S.moreOptionsLabels, ["more options", "ещё", "другие действия"]);
  const SETTINGS_ITEM = rx(S.settingsMenuTexts, ["settings", "настройк"]);
  const CAPTION_TAB = rx(S.captionsTabTexts, ["captions", "субтитр"]);
  const LANGUAGE_LABEL = rx(S.captionLanguageLabels, ["language", "язык"]);
  const JOIN_TEXT = rx(S.joinButtonTexts, ["join now", "ask to join", "присоедин", "попросить"]);
  const NAME_LABEL = rx(S.nameInputLabels, ["your name", "ваше имя", "имя"]);
  const LEFT_TEXT = rx(S.leftMeetingTexts, ["you've left", "вы вышли", "return to home", "на главный экран"]);

  const visible = (el) => {
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden";
  };

  // Иконки Material рисуются лигатурой: имя иконки лежит прямо в тексте
  // элемента. Живой созвон дал строку {"speaker":"arrow_downward",
  // "text":"Jump to bottom"} — это кнопка «вниз» из области субтитров, а
  // разбор принял её за участника с репликой. Такая строка не просто мусор:
  // при выравнивании она забирает время у соседних реплик, и имя от кнопки
  // приезжает на чужой текст whisper.
  //
  // Отсюда два правила. Первое — служебные узлы в текст не идут: иконочные
  // теги и всё, что помечено aria-hidden (именно так Meet прячет от
  // скринридера лигатуры). Второе — в области субтитров пропускаются
  // элементы управления: реплика никогда не бывает кнопкой.
  const DECORATIVE_TAGS = new Set(["I", "SVG", "STYLE", "SCRIPT", "NOSCRIPT"]);
  const decorative = (n) =>
    DECORATIVE_TAGS.has(n.tagName) || n.getAttribute("aria-hidden") === "true";
  const control = (n) =>
    n.tagName === "BUTTON" ||
    /^(button|link|menuitem|tab|slider|checkbox|switch|progressbar)$/i.test(
      n.getAttribute("role") || "");

  // Плоский список текстов-листьев. Для строки субтитров первый лист — имя
  // (или alt аватарки), остальные — сама реплика. skipControls включается
  // там, где рядом с текстом живёт интерфейс.
  const leafTexts = (root, skipControls) => {
    const out = [];
    const walk = (n) => {
      if (n.nodeType === 3) {
        const t = n.textContent.trim();
        if (t) out.push(t);
        return;
      }
      if (n.nodeType !== 1) return;
      if (decorative(n)) return;
      if (skipControls && control(n)) return;
      if (n.tagName === "IMG") {
        const a = (n.getAttribute("alt") || "").trim();
        if (a) out.push(a);
        return;
      }
      for (const c of n.childNodes) walk(c);
    };
    walk(root);
    return out;
  };

  // --- кто говорит прямо сейчас ----------------------------------------------
  //
  // Имена для расшифровки берутся из субтитров, но субтитры — это результат
  // распознавания речи, и он подводит целиком. На живом созвоне Meet слушал
  // русскую речь английским языком и выдал 31 букву за 232 секунды: имена в
  // этих крохах были верные, а вешать их было не на что.
  //
  // Подсветка говорящего к распознаванию отношения не имеет: это состояние
  // интерфейса. Она работает при любом языке и даже при выключенных субтитрах,
  // и это единственный источник имён на публичном Jitsi, где субтитров нет
  // вовсе.
  //
  // Устойчивого признака «этот участник говорит» у Meet нет — это проверено, а
  // не предположено. Ни ARIA, ни data-атрибута со состоянием речи у него не
  // существует: aria-label строки участника содержит только имя, а
  // data-participant-id и data-self-name говорят о том, кто это, а не о том,
  // что он говорит. Подсветка рисуется элементом с обфусцированным классом, а
  // такие классы меняются от релиза к релизу — на них здесь принципиально не
  // опираются.
  //
  // Поэтому признак ищется пятью способами по очереди, от самого явного к
  // самому грубому, и каждый чинится правкой selectors.json без пересборки:
  //
  //  1. список селекторов из selectors.json (speakingSelectors) — сюда
  //     кладётся то, что видно на живом созвоне через debugSpeaking();
  //  2. полоски микрофона по jsname: Meet гонит прозрачность их обёртки от 0 к
  //     1, пока человек говорит. jsname генерируется Closure, но живёт
  //     заметно дольше имён классов — на том же основании в проекте уже
  //     работают captionRegionJsnames;
  //  3. любой атрибут, имя которого говорит о речи (data-is-speaking,
  //     data-audio-level и подобные), с непустым и не «ложным» значением. У
  //     нынешнего Meet таких нет, но эта проверка ничего не стоит и переживёт
  //     их появление;
  //  4. подпись для скринридера («… говорит», «… is speaking») — тоже на
  //     вырост: у Teams такая есть, у Meet пока нет;
  //  5. цвет заливки подсветки. Цвет Meet меняет реже, чем имена классов, но
  //     всё-таки меняет — последний рубеж, и обход ради него ограничен:
  //     getComputedStyle недёшев, а опрос идёт дважды в секунду.
  //
  // Если не сработало ничего, бот скажет об этом в логе прямым текстом, а
  // debugSpeaking() покажет, что на плитках вообще есть. Молчать нельзя:
  // отсутствие имён снаружи выглядит как удавшаяся запись.
  const SPEAKING_ATTR = /speak|talking|audio-?level/i;
  const SPEAKING_LABEL = rx(S.speakingLabels, ["is speaking", "говорит", "spricht"]);
  // jsname подставляется в селектор, поэтому пропускаем всё, что не похоже на
  // имя: чужая кавычка здесь — это сломанный селектор на каждом опросе.
  const SPEAKING_JSNAMES = (S.speakingJsnames || []).filter((j) => /^[\w-]+$/.test(j));
  const SPEAKING_COLORS = (S.speakingColors || []).filter((c) => typeof c === "string" && c !== "");

  // Обход элементов поддерева. Селектором «*» это не сделать: скрипт должен
  // работать и там, где querySelectorAll ограничен, а обход всё равно нужен
  // для дампа.
  const walkEls = (root, fn) => {
    if (fn(root)) return true;
    for (const c of root.children) if (walkEls(c, fn)) return true;
    return false;
  };

  // Значение атрибута, означающее «да». Ноль и "false" — это именно «нет»:
  // data-audio-level="0" стоит у всех молчащих плиток, и без этой проверки
  // говорящими оказались бы все сразу.
  const truthyAttr = (v) => {
    if (v === null || v === undefined) return false;
    const s = String(v).trim().toLowerCase();
    if (s === "" || s === "false" || s === "none") return false;
    const n = Number(s);
    if (s !== "" && !Number.isNaN(n)) return n > 0;
    return true;
  };

  const attrSaysSpeaking = (n) => {
    for (const a of n.attributes || []) {
      if (!SPEAKING_ATTR.test(a.name)) continue;
      // «…-id» и «…-name» — про то, кто это, а не про то, что он говорит:
      // такой атрибут стоит у плитки всегда, и по нему говорящими стали бы все.
      if (/(^|-)(id|name)$/i.test(a.name)) continue;
      if (truthyAttr(a.value)) return true;
    }
    return false;
  };

  // Чем именно плитка сочтена говорящей. Пустая строка — не говорит. Строка
  // возвращается, а не true, чтобы дамп показывал, какой из трёх способов
  // сработал: когда Meet переедет, чинить будут по этой строке.
  const speakingBy = (tile) => {
    for (const sel of S.speakingSelectors || []) {
      if (typeof sel !== "string" || sel.trim() === "") continue;
      let hit = null;
      try {
        hit = tile.querySelector(sel);
      } catch (e) {
        continue; // кривой селектор в конфиге не должен ронять весь опрос
      }
      if (hit && visible(hit)) return "селектор " + sel;
    }
    for (const j of SPEAKING_JSNAMES) {
      const bars = tile.querySelector('[jsname="' + j + '"]');
      // Смотреть надо на ОБЁРТКУ полосок, а не на них самих: сами полоски
      // Meet держит в разметке всегда, а от 0 к 1 гонит прозрачность родителя.
      const wrap = bars && bars.parentElement;
      if (!wrap) continue;
      if (parseFloat(getComputedStyle(wrap).opacity || "0") > 0.1) return "jsname " + j;
    }
    let how = "";
    // Обход ограничен по числу узлов: опрос идёт дважды в секунду по всем
    // плиткам сразу, и поддерево неизвестного размера здесь — это процессор,
    // отнятый у записи звука.
    let budget = 120;
    walkEls(tile, (n) => {
      if (budget-- <= 0) return true;
      if (attrSaysSpeaking(n)) {
        how = "атрибут";
        return true;
      }
      const l = n.getAttribute("aria-label");
      if (l && SPEAKING_LABEL.test(l)) {
        how = "aria-label";
        return true;
      }
      return false;
    });
    if (how) return how;
    if (SPEAKING_COLORS.length) {
      // Только листья и не больше сорока узлов на плитку: подсветка — это
      // заливка мелкого элемента, а обходить всё поддерево дважды в секунду
      // на десяти плитках слишком дорого.
      let left = 40;
      walkEls(tile, (n) => {
        if (left-- <= 0) return true;
        if (n.children.length) return false;
        const bg = getComputedStyle(n).backgroundColor;
        if (bg && SPEAKING_COLORS.indexOf(bg) >= 0) {
          how = "цвет " + bg;
          return true;
        }
        return false;
      });
    }
    return how;
  };

  // Имя — не предложение. Meet рисует прямо в плитке предупреждения вроде
  // «Others might still see your full video.», и на живом созвоне такое попало
  // в участники и доехало до follow-up наравне с человеком.
  //
  // Признака два, и нужны оба. Знак конца ловит короткую подпись («Запись
  // остановлена.»), которую по числу слов от имени не отличить. Счёт слов
  // ловит длинную подпись без точки. Цена — имя, оканчивающееся инициалом
  // («Иван П.»), сюда не пройдёт; лишний участник в follow-up дороже.
  const nameLike = (s) =>
    s.length < 60 && !/[.!?…]$/.test(s) && s.split(/\s+/).length <= 5;

  // Имя из плитки участника. Общая с participants() нарочно: лента подсветки и
  // список участников должны называть человека одной и той же строкой — иначе
  // имена не сойдутся ни с субтитрами, ни между собой.
  //
  // Берём первый текст, похожий на имя, а не просто первый: предупреждение
  // Meet рисует выше имени, и «первый» — это как раз оно.
  const tileName = (el) => leafTexts(el).find(nameLike) || "";

  // Имя, которым бот представился. Нужно ровно для одного: не считать
  // говорящим себя.
  let myName = "";

  // Своя плитка. data-self-name Meet ставит на узел с именем локального
  // участника — это и есть признак «это мы». Имя проверяется вторым: у
  // залогиненного бота экрана с полем имени нет, но и data-self-name никуда не
  // девается, а вот при переезде вёрстки останется хотя бы одна из проверок.
  const selfTile = (el) =>
    el.getAttribute("data-self-name") !== null ||
    el.querySelector("[data-self-name]") !== null ||
    (myName !== "" && tileName(el) === myName);

  // Панель «Участники». Полоски микрофона, по которым видно говорящего, Meet
  // рисует в её строках, а плитки в сетке виртуализируются — говорящего может
  // не быть в сетке вовсе. Строки панели помечены тем же data-participant-id,
  // что и плитки, поэтому speaking() их и так обойдёт; надо только, чтобы
  // панель была открыта.
  //
  // Панель своя, локальная: другие участники её не видят.
  //
  // Список подписей нарочно тесный. «участник» подстрокой ловит «Чат с
  // участниками», и бот открывал бы чат вместо панели — то есть жал бы
  // случайную кнопку в чужом созвоне.
  const PEOPLE_LABEL = rx(S.peoplePanelLabels,
    ["participants", "people", "show everyone", "показать всех", "участники"]);

  // Сама панель, а не кнопка, которая её открывает: у обеих подходящая
  // подпись, но список есть только у панели.
  const peoplePanel = () => {
    for (const el of document.querySelectorAll("[aria-label]")) {
      if (!PEOPLE_LABEL.test(el.getAttribute("aria-label") || "")) continue;
      if (control(el)) continue;
      if (el.querySelector('[role="listitem"]')) return el;
    }
    return null;
  };

  const captionRegion = () => {
    for (const el of document.querySelectorAll('[role="region"][aria-label]')) {
      if (CAPTION_LABEL.test(el.getAttribute("aria-label"))) return el;
    }
    for (const j of S.captionRegionJsnames || []) {
      const el = document.querySelector(`[jsname="${j}"]`);
      if (el) return el;
    }
    return null;
  };

  const buttons = () => [
    ...document.querySelectorAll('button,[role="button"]'),
  ].filter(visible);

  window.__steno = {
    // Экран перед входом: гость вводит имя. Возвращает true, если поле нашлось.
    setName(name) {
      // Запоминаем всегда, даже если поля нет: у залогиненного бота экрана с
      // именем не бывает, а знать своё имя всё равно надо — по нему бот
      // вычёркивает себя из ленты говорящих.
      myName = name;
      const inputs = [...document.querySelectorAll("input")].filter(visible);
      const target =
        inputs.find((i) => NAME_LABEL.test(i.getAttribute("aria-label") || "")) ||
        inputs.find((i) => NAME_LABEL.test(i.getAttribute("placeholder") || "")) ||
        inputs.find((i) => i.type === "text" || !i.type);
      if (!target) return false;
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype, "value").set;
      setter.call(target, name);
      target.dispatchEvent(new Event("input", { bubbles: true }));
      target.dispatchEvent(new Event("change", { bubbles: true }));
      return true;
    },

    // data-is-muted есть на кнопках микрофона и камеры в комнате ожидания и
    // не зависит от локали. Кликаем всё, что сейчас не заглушено.
    muteSelf() {
      let n = 0;
      for (const b of document.querySelectorAll('[role="button"][data-is-muted="false"]')) {
        if (!visible(b)) continue;
        b.click();
        n++;
      }
      return n;
    },

    clickJoin() {
      const b = buttons().find((b) => JOIN_TEXT.test(b.innerText || ""));
      if (!b) return false;
      b.click();
      return true;
    },

    // Впустили ли нас: в звонке появляются плитки участников.
    inCall() {
      return document.querySelectorAll("[data-participant-id]").length > 0;
    },

    left() {
      if (document.querySelectorAll("[data-participant-id]").length > 0) return false;
      return LEFT_TEXT.test(document.body.innerText || "");
    },

    participants() {
      const seen = new Map();
      for (const el of document.querySelectorAll("[data-participant-id]")) {
        const id = el.getAttribute("data-participant-id");
        if (seen.has(id)) continue;
        seen.set(id, tileName(el));
      }
      return [...seen.values()].filter(Boolean);
    },

    // Кого Meet подсвечивает как говорящего прямо сейчас. Отрезки «с какой по
    // какую секунду говорил кто» собираются в Go (speakers.go): здесь состояния
    // нет, поэтому перезагрузка страницы ничего не ломает.
    //
    // Одного человека Meet рисует несколькими плитками сразу — в ленте снизу и
    // на главной. Подсветиться может любая из них, поэтому по каждому
    // участнику ответы всех его плиток складываются, а не берётся первая.
    speaking() {
      const tiles = new Map();
      for (const el of document.querySelectorAll("[data-participant-id]")) {
        const id = el.getAttribute("data-participant-id");
        let t = tiles.get(id);
        if (!t) {
          t = { name: "", on: false, self: false };
          tiles.set(id, t);
        }
        if (!t.name) t.name = tileName(el);
        if (selfTile(el)) t.self = true;
        if (!t.on && speakingBy(el) !== "") t.on = true;
      }
      const out = [];
      for (const t of tiles.values()) {
        // Молчащий бот тоже участник: своё имя в ленте — это своё имя в
        // follow-up.
        if (t.self || !t.on || !t.name) continue;
        if (out.indexOf(t.name) < 0) out.push(t.name);
      }
      return out;
    },

    // Открыть панель участников. Зовётся один раз и только если подсветка
    // молчит: когда она и так работает, лишний клик в чужом созвоне ни к чему.
    //
    // Ответы: "open" — панель уже открыта, ничего не делали; "clicked" —
    // нажали; "" — кнопки не нашли. Повторный вызов безопасен — открытую
    // панель мы не трогаем, а значит и не закрываем её вторым нажатием.
    openPeoplePanel() {
      if (peoplePanel()) return "open";
      const b = buttons().find((b) => PEOPLE_LABEL.test(b.getAttribute("aria-label") || ""));
      if (!b) return "";
      b.click();
      return "clicked";
    },

    // Текущие видимые строки субтитров. Сшивку строк в реплики делает Go:
    // здесь нет состояния, поэтому перезагрузка страницы ничего не ломает.
    captions() {
      const region = captionRegion();
      if (!region) return { ok: false, lines: [] };
      const lines = [];
      for (const entry of region.children) {
        // skipControls: в области субтитров Meet держит ещё и кнопку «вниз».
        // Без этого её иконка становилась говорящим, а подпись — репликой.
        const t = leafTexts(entry, true);
        if (!t.length) continue;
        if (t.length === 1) {
          lines.push({ speaker: "", text: t[0] });
          continue;
        }
        // Meet рисует имя дважды: alt аватарки и подпись рядом. Второй дубль
        // иначе уезжает в начало реплики.
        const speaker = t[0];
        let i = 1;
        while (i < t.length && t[i] === speaker) i++;
        lines.push({ speaker, text: t.slice(i).join(" ") });
      }
      return { ok: true, lines };
    },

    captionsOn() {
      return captionRegion() !== null;
    },

    // Meet распознаёт речь тем языком, который выбран в его настройках, а не
    // тем, на котором говорят. Русская речь при английском языке даёт не
    // мусор, а почти пустые субтитры: на живом созвоне 232 секунды речи
    // превратились в 31 букву. Имена говорящих остаются верными, но их
    // почти не на что вешать.
    //
    // Отдельной кнопки «настройки субтитров» в нынешнем Meet нет: с 2024 года
    // путь идёт через общие настройки — ⋮ («Ещё») → «Настройки» → вкладка
    // «Субтитры» → «Язык встречи». Кнопка CC на нижней панели только включает
    // и выключает субтитры. Старый селектор искал кнопку, которой больше не
    // существует, — отсюда честное «не нашёл настройки субтитров» в логе.
    //
    // Шаг за раз, а не одним вызовом: между шагами Meet рисует меню и диалог,
    // и в одном синхронном вызове следующего элемента на странице ещё нет.
    // Состояния скрипт не держит — каждый вызов смотрит, что уже открыто,
    // поэтому повторный вызов безопасен.
    captionSettingsStep() {
      const byText = (nodes, re) =>
        nodes.filter(visible).find((n) =>
          re.test((n.innerText || "").trim()) ||
          re.test(n.getAttribute("aria-label") || ""));
      const all = (sel) => [...document.querySelectorAll(sel)];

      // Дошли: вкладка «Субтитры» выбрана.
      const tab = byText(all('[role="tab"]'), CAPTION_TAB);
      if (tab) {
        if (tab.getAttribute("aria-selected") === "true") {
          return { state: "captions-tab", done: true };
        }
        tab.click();
        return { state: "clicked-captions-tab", done: false };
      }

      // Диалог настроек открыт, но вкладки «Субтитры» не видно — скажем об
      // этом словами, а не молчанием: чинить придётся по этому же логу.
      const dialog = all('[role="dialog"]').filter(visible)[0];
      if (dialog) {
        return {
          state: "dialog-without-captions-tab",
          done: false,
          tabs: all('[role="tab"]').filter(visible)
            .map((t) => (t.innerText || "").trim().slice(0, 30)),
        };
      }

      // Меню ⋮ открыто — ищем в нём «Настройки».
      const item = byText(all('[role="menuitem"],[role="menuitemradio"]'), SETTINGS_ITEM);
      if (item) {
        item.click();
        return { state: "clicked-settings", done: false };
      }

      // Старая кнопка отдельных настроек субтитров. В нынешнем Meet её нет,
      // но она была раньше и может вернуться — пробуем до общего пути.
      const direct = buttons().find((b) =>
        CAPTION_SETTINGS.test(b.getAttribute("aria-label") || ""));
      if (direct) {
        direct.click();
        return { state: "clicked-caption-settings-button", done: false };
      }

      // Ничего не открыто — жмём ⋮.
      const more = buttons().find((b) => MORE_OPTIONS.test(b.getAttribute("aria-label") || ""));
      if (more) {
        more.click();
        return { state: "clicked-more-options", done: false };
      }
      return {
        state: "no-more-options-button",
        done: false,
        buttons: buttons().map((b) => (b.getAttribute("aria-label") || "").slice(0, 40))
          .filter(Boolean).slice(0, 25),
      };
    },

    // Что за язык стоит сейчас. Ответ приблизительный: закрытая выпадашка
    // обычно показывает выбранное значение своим текстом. Нужен для лога —
    // человек должен знать, каким языком Meet слушал, даже если поменять его
    // не вышло.
    captionLanguage() {
      for (const sel of document.querySelectorAll("select")) {
        if (!visible(sel)) continue;
        const opt = [...sel.options].find((o) => o.value === sel.value);
        if (opt) return (opt.textContent || "").trim();
      }
      const chosen = [...document.querySelectorAll('[role="option"][aria-selected="true"]')]
        .filter(visible)[0];
      if (chosen) return (chosen.innerText || "").trim();
      const combo = [...document.querySelectorAll('[role="combobox"]')]
        .filter(visible)
        .find((c) => LANGUAGE_LABEL.test(c.getAttribute("aria-label") || "") ||
          LANGUAGE_LABEL.test((c.parentElement && c.parentElement.innerText) || ""));
      if (combo) return (combo.innerText || "").trim().slice(0, 40);
      return "";
    },

    // pickCaptionLanguage ищет язык по видимому названию. Список названий
    // приходит снаружи, потому что подпись зависит от языка интерфейса Meet:
    // «Русский» у русского, «Russian» у английского.
    pickCaptionLanguage(names) {
      const want = new RegExp(names.map((s) =>
        String(s).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|"), "i");

      // Вариант первый: обычный <select>.
      for (const sel of document.querySelectorAll("select")) {
        for (const opt of sel.options) {
          if (want.test(opt.textContent || "")) {
            sel.value = opt.value;
            sel.dispatchEvent(new Event("change", { bubbles: true }));
            return { ok: true, picked: opt.textContent.trim(), how: "select" };
          }
        }
      }

      // Вариант второй: список ARIA. Какой именно разметкой Meet рисует
      // выбор языка, снаружи не проверить — поэтому берём все формы, которыми
      // список вариантов вообще бывает, а не одну угаданную.
      const OPTIONS = '[role="option"],[role="menuitemradio"],[role="radio"]';
      const pickOption = () => {
        const opts = [...document.querySelectorAll(OPTIONS)].filter(visible);
        const hit = opts.find((o) => want.test(o.innerText || "") ||
          want.test(o.getAttribute("aria-label") || ""));
        if (hit) {
          hit.click();
          return {
            ok: true,
            picked: ((hit.innerText || hit.getAttribute("aria-label") || "")).trim(),
            how: hit.getAttribute("role"),
          };
        }
        return null;
      };
      let r = pickOption();
      if (r) return r;

      // Список закрыт — открываем выпадашку и пробуем снова. Кроме combobox и
      // listbox сюда идут кнопки, подписанные языком: у Google выбор нередко
      // сделан обычной кнопкой, открывающей меню.
      const combos = [
        ...document.querySelectorAll('[role="combobox"],[role="listbox"]'),
        ...buttons().filter((b) =>
          LANGUAGE_LABEL.test(b.getAttribute("aria-label") || "") ||
          LANGUAGE_LABEL.test((b.innerText || "").trim())),
      ].filter(visible);
      for (const c of combos) {
        c.click();
        r = pickOption();
        if (r) return r;
      }
      return {
        ok: false,
        combos: combos.map((c) => (c.innerText || "").slice(0, 60)),
        options: [...document.querySelectorAll(OPTIONS)]
          .filter(visible).map((o) => (o.innerText || "").slice(0, 40)).slice(0, 40),
      };
    },

    closeDialog() {
      const b = buttons().find((b) =>
        /^(done|готово|close|закрыть|save|сохранить)$/i.test((b.innerText || "").trim()) ||
        /close|закрыть/i.test(b.getAttribute("aria-label") || ""));
      if (b) {
        b.click();
        return true;
      }
      return false;
    },

    // Отладка: показать, что на странице похоже на область субтитров и что в
    // ней лежит. Нужна ровно тогда, когда Meet переехал: чинить съём субтитров
    // вслепую, по одному заходу в звонок на попытку, невозможно.
    debugCaptions() {
      const regions = [...document.querySelectorAll('[role="region"]')].map((el) => ({
        label: el.getAttribute("aria-label"),
        matches: CAPTION_LABEL.test(el.getAttribute("aria-label") || ""),
        children: el.children.length,
        sample: (el.innerText || "").slice(0, 120),
      }));
      // Иногда субтитры лежат не в role=region, а просто в контейнере с
      // подходящей подписью.
      const labelled = [...document.querySelectorAll("[aria-label]")]
        .filter((el) => CAPTION_LABEL.test(el.getAttribute("aria-label") || ""))
        .map((el) => ({
          tag: el.tagName,
          role: el.getAttribute("role"),
          label: el.getAttribute("aria-label"),
          children: el.children.length,
          sample: (el.innerText || "").slice(0, 120),
        }));
      const jsnames = (S.captionRegionJsnames || []).map((j) => ({
        jsname: j,
        found: !!document.querySelector(`[jsname="${j}"]`),
        sample: (document.querySelector(`[jsname="${j}"]`)?.innerText || "").slice(0, 120),
      }));
      return {
        regions,
        labelled,
        jsnames,
        participants: document.querySelectorAll("[data-participant-id]").length,
        // Кнопки, по подписи похожие на субтитры: если область не нашлась,
        // может быть, они и не включились.
        captionButtons: [...document.querySelectorAll('button,[role="button"]')]
          .map((b) => b.getAttribute("aria-label") || "")
          .filter((l) => /caption|субтитр|подпис/i.test(l))
          .slice(0, 6),
      };
    },

    // Отладка подсветки говорящего. Устойчивого признака у Meet нет, и когда
    // он переедет, чинить придётся по этому дампу: он показывает сырые
    // атрибуты и классы плиток. Один заход в живой звонок — и в selectors.json
    // кладётся нужный селектор, без пересборки.
    debugSpeaking() {
      const tiles = [];
      for (const el of document.querySelectorAll("[data-participant-id]")) {
        if (tiles.length >= 6) break;
        const attrs = new Set();
        const classes = new Set();
        walkEls(el, (n) => {
          for (const a of n.attributes || []) {
            if (a.name === "class" || a.name === "style") continue;
            attrs.add(a.name + "=" + String(a.value).slice(0, 24));
          }
          const c = n.getAttribute("class");
          if (c) for (const one of String(c).split(/\s+/)) if (one) classes.add(one);
          return false;
        });
        tiles.push({
          id: el.getAttribute("data-participant-id"),
          name: tileName(el),
          self: !!selfTile(el),
          how: speakingBy(el),
          attrs: [...attrs].slice(0, 30),
          classes: [...classes].slice(0, 30),
        });
      }
      return { tiles, speaking: this.speaking() };
    },
  };
} catch (e) {
  // Лучше внятная строчка в логе бота, чем неопределённый window.__steno и
  // загадочный таймаут через полторы минуты.
  window.__stenoError = String((e && e.message) || e);
}
