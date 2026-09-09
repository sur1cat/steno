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
  const JOIN_TEXT = rx(S.joinButtonTexts, ["join now", "ask to join", "присоедин", "попросить"]);
  const NAME_LABEL = rx(S.nameInputLabels, ["your name", "ваше имя", "имя"]);
  const LEFT_TEXT = rx(S.leftMeetingTexts, ["you've left", "вы вышли", "return to home", "на главный экран"]);

  const visible = (el) => {
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden";
  };

  // Плоский список текстов-листьев. Для строки субтитров первый лист — имя
  // (или alt аватарки), остальные — сама реплика.
  const leafTexts = (root) => {
    const out = [];
    const walk = (n) => {
      if (n.nodeType === 3) {
        const t = n.textContent.trim();
        if (t) out.push(t);
        return;
      }
      if (n.nodeType !== 1) return;
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
        const t = leafTexts(el).filter((s) => s.length < 60);
        seen.set(id, t[0] || "");
      }
      return [...seen.values()].filter(Boolean);
    },

    // Текущие видимые строки субтитров. Сшивку строк в реплики делает Go:
    // здесь нет состояния, поэтому перезагрузка страницы ничего не ломает.
    captions() {
      const region = captionRegion();
      if (!region) return { ok: false, lines: [] };
      const lines = [];
      for (const entry of region.children) {
        const t = leafTexts(entry);
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

    // Meet распознаёт речь тем языком, который выбран в настройках субтитров,
    // а не тем, на котором говорят. По умолчанию там английский, и русская
    // речь превращается в бессмысленный английский текст. Имена при этом
    // остаются верными, но текст брать неоткуда.
    openCaptionSettings() {
      const b = buttons().find((b) =>
        CAPTION_SETTINGS.test(b.getAttribute("aria-label") || ""));
      if (!b) return false;
      b.click();
      return true;
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

      // Вариант второй: список ARIA. Сначала пробуем уже открытый список.
      const pickOption = () => {
        const opts = [...document.querySelectorAll('[role="option"]')].filter(visible);
        const hit = opts.find((o) => want.test(o.innerText || ""));
        if (hit) {
          hit.click();
          return { ok: true, picked: (hit.innerText || "").trim(), how: "option" };
        }
        return null;
      };
      let r = pickOption();
      if (r) return r;

      // Список закрыт — открываем выпадашку и пробуем снова.
      const combos = [...document.querySelectorAll('[role="combobox"],[role="listbox"]')]
        .filter(visible);
      for (const c of combos) {
        c.click();
        r = pickOption();
        if (r) return r;
      }
      return {
        ok: false,
        combos: combos.map((c) => (c.innerText || "").slice(0, 60)),
        options: [...document.querySelectorAll('[role="option"]')]
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
  };
} catch (e) {
  // Лучше внятная строчка в логе бота, чем неопределённый window.__steno и
  // загадочный таймаут через полторы минуты.
  window.__stenoError = String((e && e.message) || e);
}
