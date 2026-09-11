# Вступительная схема README — assets/intro-{,ru-}{dark,light}.svg.
#
# Сверху — граф с ветвлением: четыре источника сходятся в бота, бот расходится
# в два файла, два файла в две дорожки, дорожки снова сходятся в расшифровку.
# Снизу — карточка, которая показывает, чем созвон стал на этом шаге. Топология
# отвечает «как устроено», карточка — «что я получу».
#
# Пересобрать оба языка:   python3 assets/src/intro.py en && python3 assets/src/intro.py ru
# Снять кадр на секунде:   node assets/src/frame.mjs assets/intro-light.svg 24.5 out.png
import html, math, os, sys, zlib

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
DUR = 32.5
W, H = 1160, 556
F  = "-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,Helvetica,Arial,sans-serif"
FM = "'SF Mono',Menlo,Consolas,monospace"
CW = 7.1

TH = {
 "dark":  dict(bg="#0B0F0D", box="#141E18", line="#26362E", fg="#DCE8E2", mut="#7C8F86",
               acc="#6BCB9B", accbg="#12251C", accln="#3E6B55", dim="#3D4E46",
               card="#101A15", rec="#F0714E", fillbg="#1B3A2C"),
 "light": dict(bg="#F7F9F8", box="#FFFFFF", line="#E1E8E4", fg="#111827", mut="#6B7280",
               acc="#2F9E6E", accbg="#E8F6EF", accln="#A9D9C3", dim="#CBD6D1",
               card="#FFFFFF", rec="#D9542F", fillbg="#D8F0E4"),
}

RU = {
 "in": "источники", "records": "пишет", "on disk": "на диске",
 "text · names": "текст · имена", "follow-up": "follow-up", "out": "разослано",
 "Calendar": "Календарь", "Bot mailbox": "Почта бота", "Telegram": "Telegram",
 "Call by link": "Вызов по ссылке", "Google Docs": "Google Docs", "Slack": "Slack",
 "Upload · Note": "Загрузка · Заметка", "a file you already have": "запись, что уже есть",
 "Panel · ui · menu bar": "Панель · ui · меню",
 "+ project state": "+ состояние проектов",
 "nothing to send": "отправлять нечего",
 "sent to": "разослано", "in": "каналы",
 "#meetings, plus a DM to whoever owns a task": "#созвоны и в личку тем, на ком задача",
 "the team chat it came from": "тот же чат, откуда пришёл",
 "· and in the panel, the terminal and the menu bar — without being sent anywhere":
     "· и в панели, в терминале и в строке меню — никуда не отправляясь",
 "Bot in a container": "Бот в контейнере", "Chromium · ffmpeg": "Chromium · ffmpeg",
 "whisper": "whisper", "on this machine": "на этой машине",
 "or another adapter": "или другой адаптер",
 "speaker names": "имена говорящих", "from the platform": "от площадки",
 "transcript": "расшифровка", "stitched by time": "сшиты по времени",
 "Claude": "Claude", "or Codex, or local": "или Codex, или своя",
 "rows in the calendar": "строки в календаре",
 "joins": "идёт", "skipped": "пропущен", "1:1 with Ana": "1:1 с Аной",
 "#nosteno in the title": "#беззаписи в заголовке",
 "47m12s": "47м12с", "4 participants": "4 участника",
 "mic and camera off": "микрофон и камера выключены",
 "12.4 MB · 47m12s": "12.4 МБ · 47м12с",
 "everything this call left behind": "всё, что осталось от созвона",
 "→ a task": "→ задача", "→ an open question": "→ открытый вопрос",
 "sent": "разослано", "already there": "и так на месте",
 "Panel": "Панель", "menu bar": "строка меню",
 "in the browser": "в браузере", "in the terminal": "в терминале",
 "on the Mac": "на маке",
 "subscription or key": "подписка или ключ", "in the cloud": "в облаке",
 "ChatGPT plan": "подписка ChatGPT",
 "Release planning": "Планёрка по релизу 2.4",
 "Ana Ribeiro · Marek Nowak · Priya Nair · Tom Ellis":
     "Участник А · Участник Б · Участник В · Участник Г",
 "The bot shows up a minute early. Nobody presses anything.":
     "Бот придёт за минуту до начала. Никто ничего не нажимает.",
 "audio, and the platform's own captions": "звук и субтитры самой площадки",
 "47m12s  ·  4 participants  ·  mic and camera off":
     "47м12с  ·  участников 4  ·  микрофон и камера выключены",
 "Chromium under a virtual display, inside a container.":
     "Chromium под виртуальным дисплеем, внутри контейнера.",
 "two files, side by side": "два файла рядом",
 "12.4 MB": "12.4 МБ", "412 lines, with who said them": "412 реплик, с именами",
 "Everything stays in one directory on your machine.":
     "Всё остаётся в одном каталоге на твоей машине.",
 "one transcript: words from the engine, names from the platform":
     "одна расшифровка: слова от движка, имена от площадки",
 "Marek Nowak": "Участник Б", "Tom Ellis": "Участник Г", "Ana Ribeiro": "Участник А",
 "I'll have the migration done by Thursday.": "Миграцию закончу к четвергу.",
 "I'll take a copy of prod and test the rollback.": "Возьму копию прода и проверю откат.",
 "Let's take on-call separately, we won't settle it now.":
     "Про дежурство отдельно, сейчас не решим.",
 "397 of 412 lines carry a name. Text and names come from different places on purpose.":
     "397 реплик из 412 с именем. Текст и имена берутся из разных мест намеренно.",
 "the follow-up, written knowing what is still open":
     "follow-up, собранный с оглядкой на то, что ещё открыто",
 "Release 2.4 moves to Friday because the migration will not make it":
     "Релиз 2.4 сдвинули на пятницу: миграция не успевает",
 "finish the schema migration": "закончить миграцию схемы",
 "test the rollback on a copy of prod": "проверить откат на копии прода",
 "due Sep 11": "срок 11.09", "due Sep 4": "срок 04.09",
 "· decision: release on Friday, 11 September — the migration cannot land sooner":
     "· решение: релиз в пятницу 11 сентября — миграция не успевает раньше",
 "Every line keeps the quote and the second it was said.":
     "У каждой строки есть цитата и секунда, на которой это прозвучало.",
 "where it went": "куда ушло", "the team chat": "чат команды",
 "4 tasks  ·  2 decisions  ·  2 open questions  ·  $0.12":
     "4 задачи  ·  2 решения  ·  2 вопроса  ·  $0.12",
 "And it stays: per project, until something closes it.":
     "И остаётся: по проектам, пока что-нибудь их не закроет.",
}
LANG = "en"
def L(s): return RU.get(s, s) if LANG == "ru" else s
def esc(s): return html.escape(L(s))
def esc_raw(s): return html.escape(s)

# Шесть карточек. Тайминги графа считаются от начала карточки: коробка сначала
# наливается, потом из неё выходит точка, и только когда точка долетела, начинает
# наливаться следующая. Прежде все точки летели весь шаг разом — новые выходили
# раньше, чем прежние доходили, и стрелки читались как каша.
CARDS = [(0.6, 5.6), (5.6, 10.6), (10.6, 15.6), (15.6, 21.8), (21.8, 26.8), (26.8, 31.8)]
FLIGHT = 1.5           # сколько летит одна точка
FILL = 1.9             # сколько наливается одна коробка
GAP = 0.4              # пауза между «налилось» и «вышла точка»
STEP = 0.35            # разбег между соседними точками

def flight(t0): return (t0, t0 + FLIGHT)
def fill(t0): return (t0, t0 + FILL)

def fr(t): return max(0.0, min(1.0, t / DUR))

def anim(attr, base, hot, a, b, fade=0.9):
    ks, vs = [0, fr(a - 0.2), fr(a), fr(b), fr(b + fade), 1], [base, base, hot, hot, base, base]
    k2, v2 = [], []
    for k, v in zip(ks, vs):
        if k2 and k <= k2[-1]: v2[-1] = v; continue
        k2.append(k); v2.append(v)
    return (f'<animate attributeName="{attr}" dur="{DUR}s" repeatCount="indefinite" '
            f'keyTimes="{";".join(f"{k:.5f}" for k in k2)}" values="{";".join(str(v) for v in v2)}"/>')

def show(a, b, soft=0.3):
    ks, vs = [0, fr(a - soft), fr(a), fr(b), fr(b + soft), 1], [0, 0, 1, 1, 0, 0]
    k2, v2 = [], []
    for k, v in zip(ks, vs):
        if k2 and k <= k2[-1]: v2[-1] = v; continue
        k2.append(k); v2.append(v)
    return (f'<animate attributeName="opacity" dur="{DUR}s" repeatCount="indefinite" '
            f'keyTimes="{";".join(f"{k:.5f}" for k in k2)}" values="{";".join(str(v) for v in v2)}"/>')

def txt(x, y, s, fill, size=12.5, anchor="start", mono=False, weight=400, pin=False):
    extra = f' textLength="{len(L(s))*CW:.1f}" lengthAdjust="spacing"' if (mono and pin and s) else ""
    return (f'<text x="{x}" y="{y}" text-anchor="{anchor}" font-family="{FM if mono else F}" '
            f'font-size="{size}" font-weight="{weight}" fill="{fill}"{extra} '
            f'xml:space="preserve">{esc(s)}</text>')

def draw(theme, suf):
    p = TH[theme]
    s = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}">',
         f'<rect width="{W}" height="{H}" rx="14" fill="{p["bg"]}"/>']

    def cap(x, y, text, win):
        a, b = win
        return (f'<text x="{x}" y="{y}" font-family="{F}" font-size="10.5" font-weight="700" '
                f'letter-spacing="1.3" fill="{p["dim"]}">{anim("fill", p["dim"], p["acc"], a, b)}'
                f'{esc_raw(L(text).upper())}</text>')

    def box(x, y, w, h, title, sub=None, win=None, mono=False, rx=10, hold=1.4,
            tsize=None, ssize=11.5, led=False):
        """Коробка наливается за FILL секунд и стоит налитой до hold после."""
        a, b = win
        # Идентификатор считается crc32, а не hash(): у hash() соль на каждый
        # запуск, и пересборка без единой правки меняла все четыре файла.
        cid = f"f{zlib.crc32(repr((suf, theme, x, y, title)).encode()):08x}"
        kt = ";".join(f"{fr(t):.5f}" for t in (0, a, b, b + hold, b + hold + 0.5, DUR))
        vals = f"0;0;{w};{w};0;0"
        edge = f"{x};{x};{x + w - 2};{x + w - 2};{x};{x}"
        out = [f'<clipPath id="{cid}"><rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}"/></clipPath>',
               f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="{p["box"]}" '
               f'stroke="{p["line"]}">{anim("stroke", p["line"], p["accln"], a, b + hold)}</rect>',
               f'<g clip-path="url(#{cid})">'
               f'<rect x="{x}" y="{y}" width="0" height="{h}" fill="{p["fillbg"]}">'
               f'<animate attributeName="width" dur="{DUR}s" repeatCount="indefinite" '
               f'keyTimes="{kt}" values="{vals}"/></rect>'
               # Бегущий край: без него наполнение читается только по разнице
               # оттенков, а она на тёмном фоне почти не видна.
               f'<rect x="{x}" y="{y}" width="2" height="{h}" fill="{p["acc"]}" opacity="0">'
               f'<animate attributeName="x" dur="{DUR}s" repeatCount="indefinite" '
               f'keyTimes="{kt}" values="{edge}"/>'
               f'{show(a, b, 0.08)}</rect></g>']
        if led:
            # Горящий кружок: из двух движков работает один, и какой — должно
            # быть видно без чтения. Загорается вместе с самой коробкой.
            out.append(f'<circle cx="{x+13}" cy="{y+h/2}" r="4" fill="{p["line"]}">'
                       f'{anim("fill", p["line"], p["acc"], a, b + hold)}</circle>')
        ty = y + h / 2 + (0 if sub is None else -6)
        # С кружком слева текст центрируется по остатку коробки, не по всей:
        # в узкой коробке подпись иначе наезжает на кружок.
        tx = x + 13 + (w - 13) / 2 if led else x + w / 2
        out.append(f'<text x="{tx}" y="{ty}" text-anchor="middle" dominant-baseline="middle" '
                   f'font-family="{FM if mono else F}" font-size="{13 if mono else 14}" '
                   f'font-weight="{500 if mono else 600}" fill="{p["fg"]}">'
                   f'{anim("fill", p["fg"], p["acc"], a, b + hold)}{esc(title)}</text>')
        if sub:
            out.append(f'<text x="{tx}" y="{y+h/2+13}" text-anchor="middle" '
                       f'dominant-baseline="middle" font-family="{F}" font-size="{ssize}" '
                       f'fill="{p["mut"]}">{esc(sub)}</text>')
        return "".join(out)

    def ghost(x, y, w, h, title, sub=None, rx=10):
        """Вариант, который не выбран. Он не наливается и не загорается ни разу
        за круг: пунктир, приглушённый цвет и пустой кружок вместо горящего."""
        out = [f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="none" '
               f'stroke="{p["dim"]}" stroke-width="1.4" stroke-dasharray="5 4"/>',
               f'<circle cx="{x+13}" cy="{y+h/2}" r="4" fill="none" stroke="{p["dim"]}" '
               f'stroke-width="1.4"/>']
        ty = y + h / 2 + (0 if sub is None else -6)
        tx = x + 13 + (w - 13) / 2
        out.append(f'<text x="{tx}" y="{ty}" text-anchor="middle" dominant-baseline="middle" '
                   f'font-family="{F}" font-size="14" font-weight="600" fill="{p["mut"]}">'
                   f'{esc(title)}</text>')
        if sub:
            out.append(f'<text x="{tx}" y="{y+h/2+13}" text-anchor="middle" '
                       f'dominant-baseline="middle" font-family="{F}" font-size="11.5" '
                       # Подпись тем же приглушённым цветом, что у остальных:
                       # цветом «выключено» её на светлой теме не прочесть, а
                       # пунктир и пустой кружок и так всё говорят.
                       f'fill="{p["mut"]}">{esc(sub)}</text>')
        return "".join(out)

    def turn(sel, body, n=3):
        """Выбор держится дольше одного круга: следующий проход идёт через
        другой движок и другого провайдера, и точка летит уже по ним. Кликать
        в README нечем — GitHub отдаёт SVG картинкой и скрипты из неё вырезает,
        так что выбор переключается сам.

        Коробки и надписи стоят на месте всегда. От прохода к проходу меняется
        только, какая из них горит и в какую ведёт сплошная линия с точкой:
        читается как переключатель, а не как переезд имён.
        """
        if isinstance(sel, int): sel = (sel,)
        ks = ";".join(f"{k / n:.5f}" for k in range(n))
        vs = ";".join("1" if k in sel else "0" for k in range(n))
        return (f'<g opacity="0"><animate attributeName="opacity" dur="{n * DUR}s" '
                f'repeatCount="indefinite" calcMode="discrete" keyTimes="{ks}" '
                f'values="{vs}"/>' + "".join(body) + "</g>")

    def offleg(x1, y1, y2, x2):
        """Ветка к невыбранному варианту. Пунктиром и без точки: путь есть, но
        сейчас по нему ничего не идёт."""
        return (f'<path d="M {x1} {y1} V {y2} H {x2-7}" fill="none" stroke="{p["dim"]}" '
                f'stroke-width="1.4" stroke-dasharray="5 4" stroke-linejoin="round"/>'
                f'<path d="M {x2-7} {y2-4} L {x2} {y2} L {x2-7} {y2+4} Z" fill="{p["dim"]}"/>')

    def wire(pid, x1, y1, x2, y2, win, elbow=True, dash=False, bend=None):
        """bend — где поворачивать. По умолчанию посередине; загрузка обходит
        бота снизу, и ей поворот нужен почти у самой цели."""
        a, b = win
        bx = bend if bend is not None else x1 + (x2 - x1) / 2
        d = (f"M {x1} {y1} H {bx} V {y2} H {x2-7}" if elbow else f"M {x1} {y1} H {x2-7}")
        da = ' stroke-dasharray="4 4"' if dash else ''""
        return (f'<path id="{pid}" d="{d}" fill="none" stroke="{p["line"]}" stroke-width="1.6"{da} '
                f'stroke-linejoin="round">{anim("stroke", p["line"], p["accln"], a, b)}</path>'
                f'<path d="M {x2-7} {y2-4} L {x2} {y2} L {x2-7} {y2+4} Z" fill="{p["line"]}">'
                f'{anim("fill", p["line"], p["acc"], a, b)}</path>'
                f'<circle r="3.6" fill="{p["acc"]}" opacity="0">{show(a, b, 0.05)}'
                f'<animateMotion dur="{DUR}s" repeatCount="indefinite" calcMode="linear" '
                f'keyTimes="0;{fr(a):.5f};{fr(b):.5f};1" keyPoints="0;0;1;1">'
                f'<mpath href="#{pid}"/></animateMotion></circle>')

    CY = 184
    A = [c[0] for c in CARDS]

    # Слева — каналы, которыми созвон приходит. Пятая коробка не канал: это
    # запись, которая уже есть, и она минует бота вовсе.
    s.append(cap(28, 40, "in", (A[0], A[0] + 4.4)))
    for i, name in enumerate(["Calendar", "Bot mailbox", "Telegram", "Call by link"]):
        y, t0 = 58 + i * 46, A[0] + i * STEP
        s.append(box(28, y, 152, 34, name, win=fill(t0), hold=1.6))
        s.append(wire(f"{suf}{theme}-in{i}", 180, y + 17, 236, CY,
                      flight(A[0] + FILL + GAP + i * STEP)))
    s.append(box(28, 250, 152, 44, "Upload · Note", "a file you already have",
                 win=fill(A[0] + 4 * STEP), hold=2.0))
    ua, ub = flight(A[1] + FILL + GAP + 2 * STEP)
    s.append(f'<path id="{suf}{theme}-up0" d="M 180 272 H 507 V 163" fill="none" '
             f'stroke="{p["line"]}" stroke-width="1.6" stroke-linejoin="round">'
             f'{anim("stroke", p["line"], p["accln"], ua, ub)}</path>'
             f'<path d="M 503 163 L 507 156 L 511 163 Z" fill="{p["line"]}">'
             f'{anim("fill", p["line"], p["acc"], ua, ub)}</path>'
             f'<circle r="3.6" fill="{p["acc"]}" opacity="0">{show(ua, ub, 0.05)}'
             f'<animateMotion dur="{DUR}s" repeatCount="indefinite" calcMode="linear" '
             f'keyTimes="0;{fr(ua):.5f};{fr(ub):.5f};1" keyPoints="0;0;1;1">'
             f'<mpath href="#{suf}{theme}-up0"/></animateMotion></circle>')

    s.append(cap(236, 40, "records", (A[1], A[1] + 4.4)))
    s.append(box(236, 140, 164, 88, "Bot in a container", "Chromium · ffmpeg",
                 win=fill(A[1]), hold=2.4))
    s.append(cap(432, 40, "on disk", (A[2], A[2] + 4.4)))
    s.append(wire(f"{suf}{theme}-d1", 400, 174, 432, 136, flight(A[1] + FILL + GAP)))
    s.append(wire(f"{suf}{theme}-d2", 400, 194, 432, 228, flight(A[1] + FILL + GAP + STEP)))
    s.append(box(432, 116, 150, 40, "audio.ogg", win=fill(A[2]), mono=True, hold=2.2))
    s.append(box(432, 208, 150, 40, "captions.jsonl", win=fill(A[2] + STEP), mono=True, hold=2.2))

    s.append(cap(614, 40, "text · names", (A[3], A[3] + 5.4)))
    # Движки распознавания — выбор одного, а не оба сразу. Обе коробки стоят
    # на месте с своими именами; от прохода к проходу меняется, какая горит:
    # в неё звук идёт линией и с точкой, во вторую — пунктиром и без точки.
    ENG = [("whisper", "on this machine", 80), ("Groq", "in the cloud", 134)]
    TX0, TX1 = 786, 956          # колонка follow-up: расшифровка во всю ширину
    TY = 220                     # расшифровка ниже стопки провайдеров
    for k, (name, sub, y) in enumerate(ENG):
        cy = y + 22
        other = ENG[1 - k]
        passes = (0, 2) if k == 0 else (1,)
        s.append(turn(passes, [
            wire(f"{suf}{theme}-t1-{k}", 582, 136, 614, cy, flight(A[2] + FILL + GAP)),
            offleg(598, 136, other[2] + 22, 614),
            box(614, y, 150, 44, name, sub, win=fill(A[3]), hold=2.6, led=True),
            ghost(614, other[2], 150, 44, other[0], other[1]),
            wire(f"{suf}{theme}-m1-{k}", 764, cy, TX0, TY + 20, flight(A[3] + FILL + GAP + 0.3)),
        ]))
    s.append(wire(f"{suf}{theme}-t2", 582, 228, 614, 228, flight(A[2] + FILL + GAP + STEP), elbow=False))
    s.append(box(614, 200, 150, 56, "speaker names", "from the platform",
                 win=fill(A[3] + STEP), hold=2.6))
    s.append(wire(f"{suf}{theme}-m2", 764, 228, TX0, TY + 36, flight(A[3] + FILL + GAP + 0.55)))
    s.append(box(TX0, TY, TX1 - TX0, 56, "transcript", "stitched by time",
                 win=fill(A[3] + FILL + GAP + FLIGHT + 0.6), hold=1.6))

    s.append(cap(TX0, 40, "follow-up", (A[4], A[4] + 4.6)))
    # Провайдеры — три отдельные коробки на общей шине слева: из расшифровки
    # линия поднимается по шине и заходит в выбранного, к остальным от шины
    # идут пунктирные отводы. Один провайдер — один проход.
    PROV = [("Claude", "subscription or key", 54),
            ("Codex", "ChatGPT plan", 104),
            ("Ollama", "on this machine", 154)]
    RX_, BX = 804, 824           # шина и левый край коробок провайдеров
    BW_ = TX1 - BX
    for k, (name, sub, y) in enumerate(PROV):
        cy = y + 21
        a, b = flight(A[4] + 0.1)
        pid = f"{suf}{theme}-up-{k}"
        body = [f'<path id="{pid}" d="M {RX_} {TY} V {cy} H {BX-7}" fill="none" stroke="{p["line"]}" '
                f'stroke-width="1.6" stroke-linejoin="round">{anim("stroke", p["line"], p["accln"], a, b)}</path>'
                f'<path d="M {BX-7} {cy-4} L {BX} {cy} L {BX-7} {cy+4} Z" fill="{p["line"]}">'
                f'{anim("fill", p["line"], p["acc"], a, b)}</path>'
                f'<circle r="3.6" fill="{p["acc"]}" opacity="0">{show(a, b, 0.05)}'
                f'<animateMotion dur="{DUR}s" repeatCount="indefinite" calcMode="linear" '
                f'keyTimes="0;{fr(a):.5f};{fr(b):.5f};1" keyPoints="0;0;1;1">'
                f'<mpath href="#{pid}"/></animateMotion></circle>']
        top = min(o[2] + 21 for o in PROV)
        if cy > top:
            # хвост шины выше выбранного — пунктиром, по нему сейчас ничего не идёт
            body.append(f'<path d="M {RX_} {cy} V {top}" fill="none" stroke="{p["dim"]}" '
                        f'stroke-width="1.4" stroke-dasharray="5 4"/>')
        for j, (oname, osub, oy) in enumerate(PROV):
            ocy = oy + 21
            if j == k:
                body.append(box(BX, y, BW_, 42, name, sub, win=fill(A[4] + 0.1 + FLIGHT),
                                hold=2.6, led=True, ssize=11))
            else:
                body.append(offleg(RX_, ocy, ocy, BX))
                body.append(ghost(BX, oy, BW_, 42, oname, osub))
        # Рассылка выходит из выбранного провайдера: ствол у каналов общий,
        # пунктиром идёт только хвост к панели — ниже последней ветки.
        for i, oyy in enumerate([58, 104, 150]):
            t0 = A[5] + 0.3 + i * STEP
            body.append(wire(f"{suf}{theme}-out{i}-{k}", TX1, cy, 978, oyy + 17, flight(t0 - FLIGHT)))
        t0 = A[5] + 0.3 + 3 * STEP
        pa, pb = flight(t0 - FLIGHT)
        tail = max(cy, 167)
        body.append(f'<path d="M 967 {tail} V 273 H 971" fill="none" stroke="{p["line"]}" '
                    f'stroke-width="1.6" stroke-dasharray="4 4" stroke-linejoin="round">'
                    f'{anim("stroke", p["line"], p["accln"], pa, pb)}</path>'
                    f'<path d="M 971 269 L 978 273 L 971 277 Z" fill="{p["line"]}">'
                    f'{anim("fill", p["line"], p["acc"], pa, pb)}</path>'
                    f'<path id="{suf}{theme}-panel-{k}" d="M {TX1} {cy} H 967 V 273 H 971" fill="none" '
                    f'stroke="none"/>'
                    f'<circle r="3.6" fill="{p["acc"]}" opacity="0">{show(pa, pb, 0.05)}'
                    f'<animateMotion dur="{DUR}s" repeatCount="indefinite" calcMode="linear" '
                    f'keyTimes="0;{fr(pa):.5f};{fr(pb):.5f};1" keyPoints="0;0;1;1">'
                    f'<mpath href="#{suf}{theme}-panel-{k}"/></animateMotion></circle>')
        s.append(turn(k, body))

    # Справа — только настоящие каналы рассылки. Панель отдельно и пунктиром:
    # туда ничего не отправляется, там всё и так лежит.
    s.append(cap(978, 40, "sent to", (A[5], A[5] + 4.6)))
    for i, name in enumerate(["Google Docs", "Slack", "Telegram"]):
        y, t0 = 58 + i * 46, A[5] + 0.3 + i * STEP
        s.append(box(978, y, 152, 34, name, win=fill(t0), hold=2.0))
    t0 = A[5] + 0.3 + 3 * STEP
    s.append(box(978, 250, 152, 46, "Panel · ui · menu bar", "nothing to send",
                 win=fill(t0), hold=2.0, tsize=12, ssize=11))

    # ── карточка «чем это стало» ────────────────────────────────────────────
    CX, CYT, CH_ = 28, 330, 204
    s.append(f'<rect x="{CX}" y="{CYT}" width="{W - 2*CX}" height="{CH_}" rx="12" '
             f'fill="{p["card"]}" stroke="{p["line"]}"/>')
    X, T = CX + 26, CYT + 40
    def row(i, dy=21): return T + i * dy
    # Блок во всю ширину карточки. Прежде всё жалось в левые две трети, и
    # добрая треть площади под графиком стояла пустой.
    BW = W - 2 * CX - 52
    RX = X + BW

    def slab(y, h, x=None, w=None):
        return (f'<rect x="{x if x is not None else X}" y="{y}" width="{w or BW}" height="{h}" '
                f'rx="9" fill="{p["box"]}" stroke="{p["line"]}"/>')

    def card(idx, body):
        a, b = CARDS[idx]
        # Уходящая карточка гаснет раньше, чем приходит следующая: на общем
        # месте они иначе четверть секунды стоят одна поверх другой, и текст
        # читается как каша.
        return f'<g opacity="0">{show(a + 0.25, b - 0.5, 0.18)}' + "".join(body) + "</g>"

    s.append(card(0, [
        txt(X, row(0), "rows in the calendar", p["mut"], 11.5),
        slab(380, 84),
        # Строка, которую берут.
        txt(X + 18, 404, "10:00", p["acc"], 12.5, mono=True, pin=True),
        txt(X + 96, 404, "Release planning", p["fg"], 13.5, weight=600),
        txt(X + 330, 404, "meet.google.com/abc-defg-hij", p["mut"], 11.5, mono=True, pin=True),
        txt(X + 640, 404, "Ana Ribeiro · Marek Nowak · Priya Nair · Tom Ellis", p["mut"], 11.5),
        txt(RX - 18, 404, "joins", p["acc"], 11.5, anchor="end", weight=600),
        f'<line x1="{X + 18}" y1="420" x2="{RX - 18}" y2="420" stroke="{p["line"]}"/>',
        # И строка, которую не берут: пометка в заголовке — и бот туда не идёт.
        txt(X + 18, 444, "16:00", p["mut"], 12.5, mono=True, pin=True),
        txt(X + 96, 444, "1:1 with Ana", p["mut"], 13.5),
        txt(X + 330, 444, "#nosteno in the title", p["mut"], 11.5, mono=True, pin=True),
        txt(RX - 18, 444, "skipped", p["mut"], 11.5, anchor="end", weight=600),
        txt(X, 502, "The bot shows up a minute early. Nobody presses anything.", p["fg"], 13),
    ]))
    bars = "".join(
        f'<rect x="{X + 18 + k*10}" y="{419 - abs(math.sin(k/2.1))*22:.1f}" width="4" rx="2" '
        f'height="{4 + abs(math.sin(k/2.1))*44:.1f}" fill="{p["acc"]}" opacity="0.55"/>'
        for k in range(97))
    s.append(card(1, [
        txt(X, row(0), "audio, and the platform's own captions", p["mut"], 11.5),
        slab(385, 68),
        bars,
        f'<circle cx="{RX - 32}" cy="419" r="5" fill="{p["rec"]}">'
        f'<animate attributeName="opacity" dur="1.3s" repeatCount="indefinite" values="1;0.2;1"/></circle>',
        txt(X, 482, "47m12s", p["fg"], 13),
        txt(X + 260, 482, "4 participants", p["fg"], 13),
        txt(X + 540, 482, "mic and camera off", p["fg"], 13),
        txt(RX, 482, "12.4 MB", p["fg"], 13, anchor="end"),
        txt(X, 512, "Chromium under a virtual display, inside a container.", p["mut"], 12.5),
    ]))
    HW = (BW - 20) // 2
    s.append(card(2, [
        txt(X, row(0), "two files, side by side", p["mut"], 11.5),
        slab(380, 46, w=HW),
        txt(X + 18, 408, "audio.ogg", p["fg"], 13, mono=True, pin=True),
        txt(X + HW - 18, 408, "12.4 MB · 47m12s", p["mut"], 11.5, anchor="end"),
        slab(380, 46, x=X + HW + 20, w=HW),
        txt(X + HW + 38, 408, "captions.jsonl", p["fg"], 13, mono=True, pin=True),
        txt(RX - 18, 408, "412 lines, with who said them", p["mut"], 11.5, anchor="end"),
        slab(438, 46),
        txt(X + 18, 466, "data/recordings/2026-09-08-1000-4f2a/", p["fg"], 12.5, mono=True, pin=True),
        txt(RX - 18, 466, "everything this call left behind", p["mut"], 11.5, anchor="end"),
        txt(X, 508, "Everything stays in one directory on your machine.", p["fg"], 13),
    ]))
    body = [txt(X, row(0), "one transcript: words from the engine, names from the platform", p["mut"], 11.5),
            slab(380, 92)]
    for k, (tc, who, what, mark) in enumerate(
            [("00:06:52", "Marek Nowak", "I'll have the migration done by Thursday.", "→ a task"),
             ("00:12:40", "Tom Ellis", "I'll take a copy of prod and test the rollback.", "→ a task"),
             ("00:31:30", "Ana Ribeiro", "Let's take on-call separately, we won't settle it now.",
              "→ an open question")]):
        y = 406 + k * 26
        body += [txt(X + 18, y, tc, p["mut"], 11.5, mono=True, pin=True),
                 txt(X + 110, y, who, p["acc"], 12.5, weight=600),
                 txt(X + 250, y, what, p["fg"], 12.5),
                 txt(RX - 18, y, mark, p["mut"], 11.5, anchor="end")]
    body.append(txt(X, 504, "397 of 412 lines carry a name. Text and names come from "
                            "different places on purpose.", p["mut"], 12.5))
    s.append(card(3, body))
    body = [txt(X, row(0), "the follow-up, written knowing what is still open", p["mut"], 11.5),
            txt(X, 396, "Release 2.4 moves to Friday because the migration will not make it",
                p["fg"], 14, weight=600),
            slab(408, 92)]
    for k, (who, what, due, tc) in enumerate(
            [("Marek Nowak", "finish the schema migration", "due Sep 11", "00:06:52"),
             ("Tom Ellis", "test the rollback on a copy of prod", "due Sep 4", "00:12:40")]):
        y = 434 + k * 26
        body += [txt(X + 18, y, "▸", p["acc"], 12),
                 txt(X + 38, y, who, p["acc"], 12.5, weight=600),
                 txt(X + 190, y, what, p["fg"], 12.5),
                 txt(RX - 200, y, due, p["mut"], 11.5, anchor="end"),
                 txt(RX - 18, y, tc, p["mut"], 11.5, mono=True, pin=True, anchor="end")]
    body += [txt(X + 18, 486, "· decision: release on Friday, 11 September — the migration "
                              "cannot land sooner", p["mut"], 12.5),
             txt(X, 520, "Every line keeps the quote and the second it was said.", p["fg"], 13)]
    s.append(card(4, body))
    # Последняя карточка о двух половинах: слева то, что разослано, справа то,
    # что никуда слать не нужно — оно и так на месте.
    body = [txt(X, row(0), "where it went", p["mut"], 11.5),
            txt(RX, row(0), "4 tasks  ·  2 decisions  ·  2 open questions  ·  $0.12",
                p["acc"], 13, weight=600, anchor="end"),
            slab(384, 108),
            f'<line x1="{X + 660}" y1="396" x2="{X + 660}" y2="480" stroke="{p["line"]}"/>',
            txt(X + 18, 402, "sent", p["mut"], 11),
            txt(X + 690, 402, "already there", p["mut"], 11)]
    for k, (name, where) in enumerate([("Google Docs", "docs.google.com/document/d/1abc"),
                                       ("Slack", "#meetings, plus a DM to whoever owns a task"),
                                       ("Telegram", "the team chat it came from")]):
        y = 424 + k * 24
        body += [txt(X + 18, y, "✓", p["acc"], 12.5),
                 txt(X + 40, y, name, p["fg"], 12.5, weight=600),
                 txt(X + 180, y, where, p["mut"], 11.5, mono=True, pin=True)]
    for k, (name, where) in enumerate([("Panel", "in the browser"), ("steno ui", "in the terminal"),
                                       ("menu bar", "on the Mac")]):
        y = 424 + k * 24
        body += [f'<circle cx="{X + 696}" cy="{y - 4}" r="4" fill="none" stroke="{p["dim"]}" '
                 f'stroke-width="1.4"/>',
                 txt(X + 716, y, name, p["mut"], 12.5, weight=600),
                 txt(RX - 18, y, where, p["mut"], 11.5, anchor="end")]
    body.append(txt(X, 518, "And it stays: per project, until something closes it.", p["fg"], 13))
    s.append(card(5, body))

    s.append("</svg>")
    path = f"{OUT}/intro{suf}-{theme}.svg"
    open(path, "w").write("".join(s))
    print("готов", theme, suf or "en", os.path.getsize(path) // 1024, "КБ")

LANG = sys.argv[1] if len(sys.argv) > 1 else "en"
SUF = "" if LANG == "en" else "-ru"
for t in TH: draw(t, SUF)
