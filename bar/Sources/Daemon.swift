import AppKit
import Foundation

// Сервис: жив ли он и как его поднять.
//
// Фоновый режим у steno уже есть — `serve -d`, `stop`, `status`, `autostart`, —
// и приложение им пользуется, а не изобретает своё. Запускать сервис
// подпроцессом было бы прямой ошибкой: подпроцесс умирает вместе с
// приложением, и человек, закрывший строку меню, остался бы без записи созвона.
//
// Состояние читаем из pid-файла (steno.pid рядом с настройкой) — того самого,
// который serve пишет для stop и status. Это дешевле, чем запускать `steno
// status` каждые несколько секунд, и даёт время запуска, чтобы сказать «работает
// с 09:12», а не бессодержательное «работает».

enum Daemon {
    enum State: Equatable {
        case running(pid: Int, since: Date)
        /// Pid-файл остался от прошлого запуска: сервис упал или был убит.
        case stale(pid: Int)
        case stopped

        var isRunning: Bool { if case .running = self { return true }; return false }
    }

    static let binaryKey = "stenoBinary"

    // --- состояние -----------------------------------------------------------

    static func state(configPath: String) -> State {
        let path = (configPath as NSString).deletingLastPathComponent + "/steno.pid"
        guard let data = FileManager.default.contents(atPath: path),
              let o = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              let pid = o["pid"] as? Int, pid > 0
        else { return .stopped }
        let since = (o["started"] as? String).flatMap(stamp) ?? Date()
        return alive(pid) ? .running(pid: pid, since: since) : .stale(pid: pid)
    }

    /// Жив ли процесс и он ли это. Номера переиспользуются, поэтому мало
    /// спросить «существует ли pid» — сверяем ещё и имя, иначе после
    /// перезагрузки чужой процесс с тем же номером выдавался бы за steno.
    private static func alive(_ pid: Int) -> Bool {
        var info = kinfo_proc()
        var size = MemoryLayout<kinfo_proc>.stride
        var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PID, Int32(pid)]
        let rc = sysctl(&mib, 4, &info, &size, nil, 0)
        guard rc == 0, size > 0 else { return false }
        let name = withUnsafePointer(to: info.kp_proc.p_comm) {
            $0.withMemoryRebound(to: CChar.self, capacity: Int(MAXCOMLEN) + 1) {
                String(cString: $0)
            }
        }
        return name.hasPrefix("steno")
    }

    /// Go пишет время как «2026-09-09T18:22:01.102617+05:00». Доли секунды
    /// бывают любой длины, а ISO8601DateFormatter уверенно понимает только три
    /// знака — поэтому дробную часть просто отбрасываем: в строке меню всё
    /// равно показываются часы и минуты.
    private static func stamp(_ s: String) -> Date? {
        var clean = s
        if let dot = s.firstIndex(of: "."),
           let end = s[dot...].firstIndex(where: { $0 == "+" || $0 == "-" || $0 == "Z" }) {
            clean = String(s[s.startIndex..<dot]) + String(s[end...])
        }
        return ISO8601DateFormatter().date(from: clean)
            ?? ISO8601DateFormatter().date(from: s)
    }

    // --- действия ------------------------------------------------------------

    /// Поднять сервис в фоне. Возвращает текст ошибки или nil, если получилось.
    static func start(configPath: String) -> String? {
        run(["serve", "-d", "-c", configPath])
    }

    static func autostart(_ on: Bool, configPath: String) -> String? {
        run(["autostart", on ? "on" : "off", "-c", configPath])
    }

    /// Выключатель исполнения ТЗ — той же командой, что и в терминале. Файл
    /// правит steno, а не приложение: у него один разбор и один порядок полей.
    static func agent(_ on: Bool, configPath: String) -> String? {
        run(["agent", on ? "on" : "off", "-c", configPath])
    }

    static func agentAuto(_ on: Bool, configPath: String) -> String? {
        run(["agent", "auto", on ? "on" : "off", "-c", configPath])
    }

    /// Включён ли автозапуск. Смотрим сам файл службы: спрашивать бинарник на
    /// каждую отрисовку меню — значит запускать процесс ради одной строки.
    static var autostartOn: Bool {
        let p = NSHomeDirectory() + "/Library/LaunchAgents/com.github.sur1cat.steno.plist"
        return FileManager.default.fileExists(atPath: p)
    }

    static func logPath(configPath: String) -> String {
        (configPath as NSString).deletingLastPathComponent + "/steno.log"
    }

    static func revealLog(configPath: String) {
        let p = logPath(configPath: configPath)
        if FileManager.default.fileExists(atPath: p) {
            NSWorkspace.shared.selectFile(p, inFileViewerRootedAtPath: "")
        }
    }

    /// Последние строки лога — чтобы объяснить, почему сервис не поднялся.
    static func logTail(configPath: String, lines: Int = 8) -> String? {
        guard let text = try? String(contentsOfFile: logPath(configPath: configPath),
                                     encoding: .utf8) else { return nil }
        let rows = text.split(separator: "\n").suffix(lines)
        return rows.isEmpty ? nil : rows.joined(separator: "\n")
    }

    // --- запуск бинарника ----------------------------------------------------

    private static func run(_ args: [String]) -> String? {
        guard let exe = binary() else {
            return L.t("не нашёл steno — ни в /opt/homebrew/bin, ни в /usr/local/bin, ")
                 + L.t("ни в ~/go/bin. Если он лежит иначе, укажи путь: ")
                 + L.t("defaults write dev.sur1cat.steno.bar stenoBinary /путь/к/steno")
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = pipe
        do {
            try p.run()
        } catch {
            return error.localizedDescription
        }
        // serve -d отвязывается и выходит сам, autostart тоже отвечает быстро.
        // Ждём именно завершения команды: её код возврата и есть ответ на
        // вопрос «получилось ли», а без него пришлось бы гадать по таймеру.
        let out = pipe.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        if p.terminationStatus == 0 { return nil }
        let text = String(data: out, encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return text.isEmpty ? L.t("steno вышел с кодом %@", "\(p.terminationStatus)") : text
    }

    static func binary() -> String? {
        if let custom = UserDefaults.standard.string(forKey: binaryKey),
           FileManager.default.isExecutableFile(atPath: custom) {
            return custom
        }
        let places = ["/opt/homebrew/bin/steno", "/usr/local/bin/steno",
                      NSHomeDirectory() + "/go/bin/steno"]
        return places.first { FileManager.default.isExecutableFile(atPath: $0) }
    }
}