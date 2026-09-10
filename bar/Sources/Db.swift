import Foundation
import SQLite3

// Чтение базы steno напрямую.
//
// Раньше приложение спрашивало данные у HTTP-панели, и значок оказывался пустым
// каждый раз, когда сервис не запущен. Но всё, что показывает строка меню, лежит
// в SQLite рядом с настройкой, и читать это можно без сервиса вообще. Сервис
// нужен для действий — позвать бота на созвон, — а не для того, чтобы показать
// уже записанное.
//
// Открываем на чтение и ничего не пишем: PRAGMA query_only запрещает запись
// на уровне самой SQLite, а WAL и так позволяет читателю не мешать писателю.
// Схему не создаём и не догоняем миграциями: это дело сервиса. Если чего-то в
// базе нет — значит, версия сервиса старше приложения, и честнее показать это
// словами, чем молча дорисовать пустоту.
//
// Тонкость, на которой это ломалось: база в режиме WAL требует индексный файл
// -shm, и создать его умеет только соединение с правом записи. Пока сервис
// работает, файл есть и SQLITE_OPEN_READONLY проходит; стоит сервису
// остановиться — SQLite убирает -shm за собой, и следующее чтение падает с
// «unable to open database file». То есть строго читающее соединение работало бы
// ровно в том случае, ради которого затевалось прямое чтение, и отказывало в
// том, ради которого оно нужнее всего. Поэтому: сначала пробуем только чтение,
// а если база отвечает именно этим отказом — переоткрываем на запись и тут же
// запираем себя query_only. Файлы SQLite при этом заводит свои служебные, а
// данных мы не трогаем.
//
// Запросы взяты из store.go и projects.go, чтобы цифры в строке меню и в панели
// сходились. Особенно расход: это ровно TotalSpend.

/// Всё, что приложение читает из базы за один заход.
struct DbSnapshot {
    var live: [LiveCall] = []
    var stuck: [LiveCall] = []
    /// Последние созвоны, свежие сверху: вкладка показывает не только сегодня —
    /// вчерашний созвон ищут ничуть не реже.
    var meetings: [Meeting] = []
    var todaySpend: Double = 0
    var tasks: [Item] = []
    var questions: [Item] = []
    var projects: [ProjectRow] = []
    var takenAt = Date()

    var today: [Meeting] { meetings.filter { Calendar.current.isDateInToday($0.started) } }

    var todaySeconds: Int {
        today.reduce(0) { $0 + $1.seconds } + live.reduce(0) {
            $0 + Int(Date().timeIntervalSince($1.started))
        }
    }

    var lastFollowup: Meeting? { meetings.first { $0.hasFollowup } }
}

struct ProjectRow: Identifiable {
    let name: String
    let about: String
    let tasks: Int
    let questions: Int
    let done: Int
    var id: String { name }
}

/// Follow-up созвона — то, ради чего steno и заводили. В меню показываем
/// коротко: о чём договорились, кто что должен, какие решения приняли.
struct Followup {
    var title = ""
    var tldr: [String] = []
    var tasks: [(owner: String, what: String)] = []
    var decisions: [String] = []
    var questions: [String] = []

    var isEmpty: Bool {
        tldr.isEmpty && tasks.isEmpty && decisions.isEmpty && questions.isEmpty
    }
}

/// Ссылка, которой сервис помечает надиктованную заметку (noteURL в note.go).
/// Строка меню должна отличать её от созвона: у заметки нет ни комнаты, ни
/// второго участника, зато есть кнопка «остановить», которой у созвона нет.
let stenoNoteURL = "steno://note"

struct LiveCall: Identifiable {
    let id: String
    let title: String
    let meetUrl: String
    let started: Date
    let participants: [String]

    var isNote: Bool { meetUrl == stenoNoteURL }
}

struct Meeting: Identifiable {
    let id: String
    let title: String
    let started: Date
    let seconds: Int
    let status: String
    let leftReason: String
    let hasFollowup: Bool
    let meetUrl: String

    var isNote: Bool { meetUrl == stenoNoteURL }

    /// Показывать ли строку тревожно. «Ушёл раньше времени» — тоже беда: запись
    /// есть, но кусок разговора в неё не попал.
    ///
    /// У заметки в этом поле стоит не беда, а происхождение: «надиктовано в
    /// микрофон». Без оговорки каждая заметка загоралась бы тревожной, а
    /// «ушёл раньше» звучало бы про человека, который просто договорил.
    /// Обычный конец созвона: все разошлись, бот ушёл следом. store.go, где
    /// эта строка и пишется, прямо оговаривает, что «остался один» — норма, а
    /// повод посмотреть — «бота вывели» и «страница закрылась». Знаем оба
    /// написания: строку записала та установка, у которой был свой язык.
    private static let normalEndings: Set<String> = ["остался один", "left alone"]

    private var endedNormally: Bool { Meeting.normalEndings.contains(leftReason) }

    var troubled: Bool {
        if status == "failed" || status == "publish_failed" { return true }
        return !isNote && !leftReason.isEmpty && !endedNormally
    }

    var statusWord: String {
        switch status {
        case "failed": return L.t("не получилось")
        case "publish_failed": return L.t("не отправилось")
        case "recording": return isNote ? L.t("наговаривается") : L.t("пишется")
        case "transcribed": return L.t("расшифровано")
        case "summarized": return L.t("без отправки")
        default:
            if isNote { return "" }
            return leftReason.isEmpty || endedNormally ? "" : L.t("ушёл раньше")
        }
    }
}

/// Пункт проекта: задача, вопрос или решение.
struct Item: Identifiable {
    let id: String
    let project: String
    let kind: String
    let text: String
    let owner: String
    let due: String
    let opened: Date
}

enum DbError: Error {
    case missing(String)
    case cannotOpen(String)
    case query(String)

    var message: String {
        switch self {
        case .missing(let p): return L.t("базы нет: %@", Conf.pretty(p))
        case .cannotOpen(let why): return why
        case .query(let why): return why
        }
    }
}

final class Db {
    private var h: OpaquePointer?

    let path: String

    /// Соединение живёт один заход и закрывается: держать его открытым между
    /// опросами было бы дешевле, но тогда подменённый файл базы (steno setup
    /// заново, перенос каталога) остался бы невидимым — открытый дескриптор
    /// продолжал бы читать удалённый inode и показывать вчерашний день.
    /// Открытие файла стоит доли миллисекунды, ошибка такого рода — доверия.
    ///
    /// Файл должен существовать: заводить пустую базу нельзя ни в коем случае —
    /// «сервис ни разу не запускался» выглядело бы тогда как «созвонов не было».
    init(path: String) throws {
        self.path = path
        guard FileManager.default.fileExists(atPath: path) else {
            throw DbError.missing(path)
        }
        if let handle = Db.open(path, SQLITE_OPEN_READONLY) {
            h = handle
        } else if let handle = Db.open(path, SQLITE_OPEN_READWRITE) {
            h = handle
        } else {
            throw DbError.cannotOpen(L.t("база не открывается: %@", Conf.pretty(path)))
        }
    }

    /// Открыть и сразу проверить запросом: SQLITE_OPEN_READONLY откладывает
    /// настоящую проверку до первого обращения, и без пробного запроса ошибка
    /// вылезла бы посреди выборки, где её уже не отличить от «схема не та».
    private static func open(_ path: String, _ flags: Int32) -> OpaquePointer? {
        var handle: OpaquePointer?
        guard sqlite3_open_v2(path, &handle, flags, nil) == SQLITE_OK, let handle else {
            sqlite3_close_v2(handle)
            return nil
        }
        // Сервис пишет в базу в тот же момент, что мы читаем. В WAL это
        // разрешено, но короткая блокировка индексного файла всё же случается —
        // лучше подождать её, чем показать человеку ошибку на ровном месте.
        sqlite3_busy_timeout(handle, 3000)
        sqlite3_exec(handle, "PRAGMA query_only=1", nil, nil, nil)
        if sqlite3_exec(handle, "SELECT 1 FROM sqlite_schema LIMIT 1", nil, nil, nil) != SQLITE_OK {
            sqlite3_close_v2(handle)
            return nil
        }
        return handle
    }

    deinit { sqlite3_close_v2(h) }

    // --- выборки -------------------------------------------------------------

    /// serviceSince — с какого момента работает нынешний сервис. Записи, начатой
    /// раньше него, быть не может: писал её процесс, которого больше нет.
    func snapshot(dayStart: Date, stuckAfter: TimeInterval,
                  serviceRunning: Bool, serviceSince: Date? = nil) throws -> DbSnapshot {
        var s = DbSnapshot()

        // Идущие записи — по тому же признаку, по которому их заводит сервис:
        // статус recording и незакрытое время окончания. Никаких догадок.
        try each("""
            SELECT id, title, meet_url, started_at, participants
              FROM meetings WHERE status='recording' AND ended_at IS NULL
              ORDER BY started_at DESC
            """) { st in
            let call = LiveCall(id: text(st, 0), title: text(st, 1), meetUrl: text(st, 2),
                                started: date(st, 3), participants: names(text(st, 4)))
            // Незакрытая запись без работающего сервиса — не разговор, а след
            // прошлого запуска: писать её сейчас некому. Это уже не догадка по
            // времени, а простое рассуждение, и оно снимает секундомер, который
            // иначе бодро тикал бы над мёртвым сервисом. Вторая проверка — по
            // сроку: сервис живой, но запись идёт дольше, чем боту вообще
            // разрешено сидеть на созвоне.
            //
            // Третья — по времени старта. Сервис перезапустили, а строка от
            // убитого осталась «пишется»: без этой проверки в строке меню
            // тикает призрак — красный секундомер записи, которой нет. Такое
            // видели живьём после `steno stop` посреди заметки. Начаться
            // раньше своего сервиса запись не может.
            let beforeService = serviceSince.map { call.started < $0.addingTimeInterval(-2) } ?? false
            if !serviceRunning || beforeService
                || Date().timeIntervalSince(call.started) > stuckAfter {
                s.stuck.append(call)
            } else {
                s.live.append(call)
            }
        }

        // Последние созвоны — списком, а не только сегодняшние: вкладка живёт
        // не одним днём. Признак follow-up берём подзапросом, чтобы сразу знать,
        // какую строку есть смысл раскрывать.
        try each("""
            SELECT m.id, m.title, m.started_at, COALESCE(m.ended_at,0), m.status, m.left_reason,
                   EXISTS(SELECT 1 FROM followups f WHERE f.meeting_id = m.id), m.meet_url
              FROM meetings m
              WHERE NOT (m.status='recording' AND m.ended_at IS NULL)
              ORDER BY m.started_at DESC LIMIT 40
            """) { st in
            let started = date(st, 2)
            let ended = sqlite3_column_int64(st, 3)
            s.meetings.append(Meeting(
                id: text(st, 0), title: text(st, 1), started: started,
                seconds: ended > 0 ? Int(ended) - Int(started.timeIntervalSince1970) : 0,
                status: text(st, 4), leftReason: text(st, 5),
                hasFollowup: sqlite3_column_int64(st, 6) == 1,
                meetUrl: text(st, 7)))
        }

        // Расход за день — тот же запрос, что TotalSpend в store.go.
        try each("""
            SELECT COALESCE(SUM(f.cost_usd),0)
              FROM followups f JOIN meetings m ON m.id = f.meeting_id
              WHERE m.started_at >= ?
            """, int(dayStart)) { st in
            s.todaySpend = sqlite3_column_double(st, 0)
        }

        // Открытые пункты проектов — как OpenItems в projects.go. Свежие
        // сверху: после созвона человек ищет то, что записали только что.
        try each("""
            SELECT id, project, kind, text, owner, due, opened_at
              FROM project_items WHERE status='open' ORDER BY opened_at DESC
            """) { st in
            let item = Item(id: text(st, 0), project: text(st, 1), kind: text(st, 2),
                            text: text(st, 3), owner: text(st, 4),
                            due: text(st, 5), opened: date(st, 6))
            switch item.kind {
            case "question": s.questions.append(item)
            case "decision": break
            default: s.tasks.append(item)
            }
        }

        // Проекты заводит человек в панели, а счётчики считаем по пунктам.
        try each("""
            SELECT p.name,
                   COALESCE(NULLIF(p.about, ''),
                            (SELECT substr(c.primer, 1, 300) FROM project_context c
                              WHERE c.project = p.name), ''),
                   (SELECT COUNT(*) FROM project_items i
                     WHERE i.project = p.name AND i.status='open' AND i.kind='task'),
                   (SELECT COUNT(*) FROM project_items i
                     WHERE i.project = p.name AND i.status='open' AND i.kind='question'),
                   (SELECT COUNT(*) FROM project_items i
                     WHERE i.project = p.name AND i.status='done')
              FROM projects p ORDER BY p.name
            """) { st in
            // Справка о проекте бывает пустой, а рядом лежит собранный primer —
            // от него хватает первой строки, чтобы строка не была голой.
            s.projects.append(ProjectRow(name: text(st, 0), about: firstLine(text(st, 1)),
                                         tasks: Int(sqlite3_column_int64(st, 2)),
                                         questions: Int(sqlite3_column_int64(st, 3)),
                                         done: Int(sqlite3_column_int64(st, 4))))
        }
        return s
    }

    /// Follow-up созвона. Лежит одним JSON-полем — тем самым, что панель
    /// показывает страницей; здесь из него берём только то, что читается за
    /// несколько секунд.
    func followup(_ meetingID: String) throws -> Followup? {
        var payload: String?
        try each("SELECT payload FROM followups WHERE meeting_id = ?", meetingID) { st in
            payload = text(st, 0)
        }
        guard let payload, let data = payload.data(using: .utf8),
              let o = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        else { return nil }
        var f = Followup()
        f.title = o["title"] as? String ?? ""
        f.tldr = (o["tldr"] as? [String]) ?? []
        for raw in (o["action_items"] as? [[String: Any]]) ?? [] {
            let what = raw["what"] as? String ?? ""
            if what.isEmpty { continue }
            f.tasks.append((owner: raw["owner"] as? String ?? "", what: what))
        }
        for raw in (o["decisions"] as? [[String: Any]]) ?? [] {
            if let what = raw["what"] as? String, !what.isEmpty { f.decisions.append(what) }
        }
        for raw in (o["open_questions"] as? [[String: Any]]) ?? [] {
            if let what = raw["question"] as? String ?? raw["what"] as? String, !what.isEmpty {
                f.questions.append(what)
            }
        }
        return f
    }

    // --- запись ----------------------------------------------------------------

    // Единственное, что приложение меняет в базе, — статус пункта. Делается это
    // отдельным коротким соединением на запись: держать открытым право писать
    // ради двух нажатий в день незачем, а сервис пишет в ту же базу постоянно.
    // busy_timeout здесь обязателен — в WAL писатель один, и наткнуться на
    // чужую запись посреди созвона проще простого.
    //
    // Запрос — тот же, что делает панель (CloseItem в projects.go, ручка
    // apiCloseItem): условие status='open' не даёт закрыть дважды, а закрытое
    // руками отличается от закрытого автоматикой по заметке.

    static func closeItem(_ id: String, at path: String) throws {
        try write("""
            UPDATE project_items SET status='done', note='закрыто руками из строки меню',
                   closed_in='', updated_at=? WHERE id=? AND status='open'
            """, at: path, id: id)
    }

    /// Вернуть задачу в работу: нажатие «сделано» бывает промахом, и без отмены
    /// оно было бы необратимым. Тот же ReopenItem, что и в панели.
    static func reopenItem(_ id: String, at path: String) throws {
        try write("""
            UPDATE project_items SET status='open', closed_in='', note='',
                   updated_at=? WHERE id=?
            """, at: path, id: id)
    }

    private static func write(_ sql: String, at path: String, id: String) throws {
        var h: OpaquePointer?
        guard sqlite3_open_v2(path, &h, SQLITE_OPEN_READWRITE, nil) == SQLITE_OK, let h else {
            sqlite3_close_v2(h)
            throw DbError.cannotOpen(L.t("база не открылась на запись"))
        }
        defer { sqlite3_close_v2(h) }
        sqlite3_busy_timeout(h, 5000)
        var st: OpaquePointer?
        guard sqlite3_prepare_v2(h, sql, -1, &st, nil) == SQLITE_OK, let st else {
            throw DbError.query(String(cString: sqlite3_errmsg(h)))
        }
        defer { sqlite3_finalize(st) }
        sqlite3_bind_int64(st, 1, Int64(Date().timeIntervalSince1970))
        sqlite3_bind_text(st, 2, id, -1, transient)
        guard sqlite3_step(st) == SQLITE_DONE else {
            throw DbError.query(String(cString: sqlite3_errmsg(h)))
        }
    }

    // --- мелочи --------------------------------------------------------------

    private func each(_ sql: String, _ arg: Int64? = nil,
                      _ row: (OpaquePointer) -> Void) throws {
        try each(sql, arg, nil, row)
    }

    private func each(_ sql: String, _ text: String,
                      _ row: (OpaquePointer) -> Void) throws {
        try each(sql, nil, text, row)
    }

    private func each(_ sql: String, _ number: Int64?, _ string: String?,
                      _ row: (OpaquePointer) -> Void) throws {
        var st: OpaquePointer?
        guard sqlite3_prepare_v2(h, sql, -1, &st, nil) == SQLITE_OK, let st else {
            throw DbError.query(String(cString: sqlite3_errmsg(h)))
        }
        defer { sqlite3_finalize(st) }
        if let arg = number { sqlite3_bind_int64(st, 1, arg) }
        // SQLITE_TRANSIENT: строку SQLite копирует себе, иначе она уедет из-под
        // запроса вместе с выходом из функции.
        if let string { sqlite3_bind_text(st, 1, string, -1, Db.transient) }
        while true {
            let rc = sqlite3_step(st)
            if rc == SQLITE_ROW { row(st); continue }
            if rc == SQLITE_DONE { return }
            throw DbError.query(String(cString: sqlite3_errmsg(h)))
        }
    }

    private func text(_ st: OpaquePointer, _ i: Int32) -> String {
        guard let c = sqlite3_column_text(st, i) else { return "" }
        return String(cString: c)
    }

    private func date(_ st: OpaquePointer, _ i: Int32) -> Date {
        Date(timeIntervalSince1970: Double(sqlite3_column_int64(st, i)))
    }

    private func int(_ d: Date) -> Int64 { Int64(d.timeIntervalSince1970) }

    private static let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

    private func firstLine(_ raw: String) -> String {
        for line in raw.split(separator: "\n") {
            let s = line.trimmingCharacters(in: CharacterSet(charactersIn: " \t#*-"))
            if s.count > 3 { return String(s.prefix(140)) }
        }
        return ""
    }

    /// Участники лежат JSON-массивом в одном поле.
    private func names(_ raw: String) -> [String] {
        guard let data = raw.data(using: .utf8),
              let list = try? JSONSerialization.jsonObject(with: data) as? [String]
        else { return [] }
        return list
    }
}
