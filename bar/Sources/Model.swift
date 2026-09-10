import Foundation
import SwiftUI

// Состояние приложения: что показывать в строке меню и в самом меню.
//
// Данные читаются из базы напрямую (Db), а не спрашиваются у сервиса. Разница
// не техническая: значок в строке меню должен отвечать на вопрос «что у меня с
// созвонами» всегда, а не только пока поднят steno serve. Поэтому упавший
// сервис здесь — не пустота, а отдельная строчка: записанное на месте,
// недоступно ровно одно действие, и видно, какое именно.
//
// Сервис остаётся нужен для приглашения бота: увести бота на созвон умеет
// только он.

enum Health: Equatable {
    case starting
    case ok
    /// Читать нечего и приложение не может это исправить само:
    /// нет настройки, нет базы, база не открывается.
    case broken(title: String, detail: String)
}

struct BarState: Equatable {
    enum Kind: Equatable {
        case idle
        /// note — наговаривается заметка, а не пишется созвон. Разница видна в
        /// строке меню: у заметки микрофон вместо точки. Иначе человек, нажавший
        /// «наговорить», видит ровно то же, что при чужом созвоне, и не понимает,
        /// его ли это запись и можно ли уже говорить.
        case recording(elapsed: TimeInterval, count: Int, note: Bool)
        /// Сервис не работает: записи сейчас не будет, и это стоит заметить.
        case serviceDown
        /// Данные не читаются вовсе.
        case trouble
        case starting
    }
    var kind: Kind = .starting
}

@MainActor
final class Loader: ObservableObject {
    @Published private(set) var health: Health = .starting
    @Published private(set) var snapshot = DbSnapshot()
    @Published private(set) var setup: Setup?
    @Published private(set) var trouble: SetupTrouble?
    @Published private(set) var service: Daemon.State = .stopped
    @Published private(set) var autostartOn = false
    /// Тикает раз в секунду — только ради секундомера идущей записи.
    @Published private(set) var now = Date()
    @Published private(set) var busyWithService = false
    @Published private(set) var serviceLog: String?
    /// Задача, которую только что закрыли, — чтобы предложить вернуть.
    @Published private(set) var justClosed: (id: String, text: String)?
    @Published private(set) var writeError: String?
    /// Заметка: пока запрос в пути, кнопка не должна нажиматься второй раз, а
    /// отказ сервиса надо показать словами — они у него человеческие.
    @Published private(set) var noteBusy = false
    @Published private(set) var noteMessage: (ok: Bool, text: String)?

    private var timer: Timer?
    private var tick: Timer?
    private var busy = false
    private var again = false

    /// База читается дёшево — это файл на диске, а не запрос по сети, — поэтому
    /// опрашиваем чаще, чем ходили бы к сервису. Пять секунд означают, что
    /// начавшаяся запись видна почти сразу.
    private let every: TimeInterval = 5

    var bar: BarState {
        switch health {
        case .starting: return BarState(kind: .starting)
        case .broken: return BarState(kind: .trouble)
        case .ok:
            if let first = snapshot.live.first {
                return BarState(kind: .recording(elapsed: now.timeIntervalSince(first.started),
                                                 count: snapshot.live.count,
                                                 note: first.isNote))
            }
            return BarState(kind: service.isRunning ? .idle : .serviceDown)
        }
    }

    /// Идущая заметка, если она идёт. Ищем по всем записям, а не по первой:
    /// заметку можно наговаривать, пока бот сидит на созвоне.
    var liveNote: LiveCall? { snapshot.live.first { $0.isNote } }

    /// Можно ли вести человека в панель браузера и звать бота. Панель поднимает
    /// сервис: без него по адресу никого нет.
    var panelReachable: Bool {
        service.isRunning && setup?.base != nil && setup?.panelEnabled == true
    }

    func start() {
        Task { await refresh() }
        timer = Timer.scheduledTimer(withTimeInterval: every, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        }
        tick = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self, !self.snapshot.live.isEmpty else { return }
                self.now = Date()
            }
        }
    }

    func stop() {
        timer?.invalidate(); timer = nil
        tick?.invalidate(); tick = nil
    }

    // --- опрос ---------------------------------------------------------------

    func refresh() async {
        // Опрос по таймеру и опрос после нажатия могут прийтись друг на друга.
        // Просто отбрасывать второй нельзя: именно он показывает, что бота
        // позвали. Поэтому опоздавший не пропадает, а повторяется следом.
        if busy {
            again = true
            return
        }
        busy = true
        defer {
            busy = false
            if again {
                again = false
                Task { await self.refresh() }
            }
        }

        switch Conf.find() {
        case .failure(let t):
            trouble = t
            setup = nil
            service = .stopped
            health = .broken(title: t.title, detail: t.detail)
            return
        case .success(let s):
            // Язык — до всего, что рисуется дальше: настройка steno главнее
            // окружения, из которого приложение запустили.
            L.use(s.lang)
            trouble = nil
            setup = s
            service = Daemon.state(configPath: s.configPath)
            autostartOn = Daemon.autostartOn
            await read(s)
        }
        now = Date()
    }

    private func read(_ s: Setup) async {
        let path = s.dbPath
        let stuckAfter = max(s.maxRecording, 900)
        let running = service.isRunning
        let since: Date? = { if case .running(_, let d) = service { return d }; return nil }()
        let dayStart = Calendar.current.startOfDay(for: Date())
        // Чтение уходит с главной очереди: база маленькая, но она на диске, а
        // подвисшее на секунду меню человек замечает сразу.
        let result: Result<DbSnapshot, Error> = await Task.detached(priority: .userInitiated) {
            do {
                let db = try Db(path: path)
                return .success(try db.snapshot(dayStart: dayStart, stuckAfter: stuckAfter,
                                                serviceRunning: running, serviceSince: since))
            } catch {
                return .failure(error)
            }
        }.value

        switch result {
        case .success(let snap):
            snapshot = snap
            health = .ok
        case .failure(let e):
            let why = (e as? DbError)?.message ?? e.localizedDescription
            if case .missing = e as? DbError {
                health = .broken(
                    title: L.t("Базы steno ещё нет"),
                    detail: L.t("Жду %@ — этот файл сервис заводит при первом запуске. "
                              + "Похоже, steno на этой машине ещё ни разу не работал.",
                                Conf.pretty(path)))
            } else {
                health = .broken(title: L.t("База не читается"), detail: why)
            }
        }
    }

    // --- задачи ---------------------------------------------------------------

    /// Отметить задачу сделанной. Пишем в базу тем же запросом, что и панель;
    /// после записи сразу перечитываем, чтобы список не расходился с базой.
    func closeTask(_ item: Item) async {
        guard let path = setup?.dbPath else { return }
        writeError = nil
        let id = item.id
        let result: String? = await Task.detached(priority: .userInitiated) {
            do {
                try Db.closeItem(id, at: path)
                return nil
            } catch {
                return (error as? DbError)?.message ?? error.localizedDescription
            }
        }.value
        if let result {
            writeError = result
            return
        }
        justClosed = (id: item.id, text: item.text)
        await refresh()
    }

    /// Вернуть последнюю закрытую задачу в работу.
    func undoClose() async {
        guard let path = setup?.dbPath, let closed = justClosed else { return }
        writeError = nil
        let id = closed.id
        let result: String? = await Task.detached(priority: .userInitiated) {
            do {
                try Db.reopenItem(id, at: path)
                return nil
            } catch {
                return (error as? DbError)?.message ?? error.localizedDescription
            }
        }.value
        if let result {
            writeError = result
            return
        }
        justClosed = nil
        await refresh()
    }

    func forgetClosed() { justClosed = nil }

    /// Follow-up созвона — читаем по требованию: он нужен, только когда строку
    /// раскрыли, а payload у длинного созвона весит прилично.
    func followup(_ meetingID: String) async -> Followup? {
        guard let path = setup?.dbPath else { return nil }
        return await Task.detached(priority: .userInitiated) {
            (try? Db(path: path))
                .flatMap { try? $0.followup(meetingID) } ?? nil
        }.value
    }

    // --- действия ------------------------------------------------------------

    /// Позвать бота. Ссылку не разбираем сами: её разбор живёт в сервисе
    /// (findMeetURL) и умеет объяснять словами, что не так, — а две проверки в
    /// двух местах однажды разойдутся, и приложение забракует живую ссылку.
    func invite(url: String, title: String) async -> (ok: Bool, text: String) {
        guard let s = setup, let base = s.base, let password = s.password else {
            return (false, L.t("некуда звать: не собрался адрес панели или нет пароля"))
        }
        let api = Api(base: base, password: password)
        var body: [String: Any] = ["url": url]
        let t = title.trimmingCharacters(in: .whitespacesAndNewlines)
        if !t.isEmpty { body["title"] = t }
        do {
            let r = try await api.post("/api/invite", body: body, as: InviteReply.self)
            await refresh()
            return (r.status == "started", r.message ?? r.error ?? L.t("готово"))
        } catch let e as ApiError {
            return (false, e.message)
        } catch {
            return (false, error.localizedDescription)
        }
    }

    // --- заметка ---------------------------------------------------------------

    /// Начать и остановить — одна дорога: ffmpeg держит сервис, а не мы.
    /// Приложение, убитое посреди заметки, не должно уносить запись с собой,
    /// поэтому запускать микрофон подпроцессом здесь нельзя.
    func startNote() async { await note("/api/note/start", L.t("пишу — говори")) }

    func stopNote() async { await note("/api/note/stop", L.t("расшифровываю и разбираю")) }

    /// Промах по кнопке. Отдельным действием, потому что иначе он стоит
    /// расшифровки и запроса к Claude, а в списке навсегда остаётся строка.
    func cancelNote() async { await note("/api/note/cancel", L.t("заметка выброшена")) }

    private func note(_ path: String, _ done: String) async {
        guard !noteBusy else { return }
        guard let s = setup, let base = s.base, let password = s.password else {
            noteMessage = (false, L.t("некому писать: не собрался адрес панели или нет пароля"))
            return
        }
        guard service.isRunning else {
            noteMessage = (false, L.t("заметку пишет сервис, а он не запущен"))
            return
        }
        noteBusy = true
        noteMessage = nil
        defer { noteBusy = false }
        let api = Api(base: base, password: password)
        do {
            let r = try await api.post(path, body: [:], as: NoteReply.self)
            // Слова сервиса главнее наших: он один знает, чем именно не
            // понравился микрофон.
            noteMessage = (true, r.message ?? done)
        } catch let e as ApiError {
            noteMessage = (false, e.message)
        } catch {
            noteMessage = (false, error.localizedDescription)
        }
        await refresh()
    }

    func forgetNoteMessage() { noteMessage = nil }

    /// Поднять сервис в фоне — тем же способом, каким это делает человек в
    /// терминале: `steno serve -d`. Подпроцессом запускать нельзя, он умрёт
    /// вместе с приложением и унесёт с собой запись идущего созвона.
    func startService() async {
        guard let path = setup?.configPath ?? trouble?.configPath else { return }
        busyWithService = true
        serviceLog = nil
        defer { busyWithService = false }
        if let why = Daemon.start(configPath: path) {
            serviceLog = why
            return
        }
        // serve -d выходит сразу, а сервис поднимается ещё секунду-другую.
        for _ in 0..<12 {
            try? await Task.sleep(nanoseconds: 400_000_000)
            if Daemon.state(configPath: path).isRunning { break }
        }
        await refresh()
        if !service.isRunning {
            serviceLog = Daemon.logTail(configPath: path) ?? L.t("сервис не поднялся")
        }
    }

    func setAutostart(_ on: Bool) async {
        guard let path = setup?.configPath else { return }
        busyWithService = true
        serviceLog = nil
        defer { busyWithService = false }
        if let why = Daemon.autostart(on, configPath: path) {
            serviceLog = why
        }
        autostartOn = Daemon.autostartOn
    }
}
