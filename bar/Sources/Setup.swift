import Foundation

// Где лежит настройка steno: путь к базе, а заодно адрес панели с паролем.
//
// Приложению в строке меню неоткуда взять ни путь к базе, ни пароль: у него нет
// текущего каталога, из которого человек запускает команды, и переменные из
// .env ему никто не передаёт. Поэтому оно повторяет тот же поиск, что и сам
// steno (confpath.go): указатель в ~/.config/steno/path, а рядом с найденным
// конфигом — data/ и .env.
//
// Главное здесь — что «нашёл настройку» и «сервис умеет принимать приглашения»
// разведены. Показывать записанное можно всегда: данные лежат в базе. Панель
// выключена или пароля нет — это мешает только одному действию, позвать бота, и
// сказать об этом надо там же, где его зовут, а не вместо всего содержимого.

struct Setup: Equatable {
    var configPath: String   // /Users/имя/steno/steno.json
    var dataDir: String      // /Users/имя/steno/data
    var maxRecording: TimeInterval   // bot.max_duration
    var addr: String
    var base: URL?           // http://127.0.0.1:8080 — если адрес разобрался
    var password: String?
    var passwordEnv: String
    var envPath: String
    var panelEnabled: Bool
    var lang: String         // «lang» из настройки: приложение говорит на том же языке

    var dbPath: String { dataDir + "/steno.db" }
    var dir: String { (configPath as NSString).deletingLastPathComponent }

    /// Почему приглашение недоступно, даже если сервис поднят. Ровно то, что
    /// сервис не сможет обойти сам.
    var inviteTrouble: String? {
        if !panelEnabled {
            return L.t("В %@ стоит panel.enabled = false — пока он там стоит, "
                     + "сервис не поднимет панель, а звать бота приложению больше некуда.",
                       Conf.pretty(configPath))
        }
        if base == nil {
            return L.t("В %@ написано panel.addr = «%@» — из этого не собрать адрес.",
                       Conf.pretty(configPath), addr)
        }
        if password == nil {
            return L.t("Пароль панели лежит в переменной %@, а её нет ни в окружении, "
                     + "ни в %@. Допиши в этот файл строку «%@=…» — тот же пароль, "
                     + "которым панель открывается в браузере.",
                       passwordEnv, Conf.pretty(envPath), passwordEnv)
        }
        return nil
    }
}

enum SetupTrouble: Error, Equatable {
    /// Настройки нет нигде, где её принято держать.
    case noConfig(searched: [String])
    /// Файл есть, но прочитать его не вышло.
    case unreadable(path: String, why: String)

    var title: String {
        switch self {
        case .noConfig: return L.t("Не нашёл настройку steno")
        case .unreadable: return L.t("Настройка не читается")
        }
    }

    var detail: String {
        switch self {
        case .noConfig(let searched):
            return L.t("Искал: ") + searched.map { Conf.pretty($0) }.joined(separator: ", ")
                + L.t(". Если steno ещё не настроен — запусти в терминале «steno setup».")
                + L.t(" Если настройка лежит в другом месте — выбери файл вручную.")
        case .unreadable(let path, let why):
            return "\(Conf.pretty(path)): \(why)"
        }
    }

    var configPath: String? {
        switch self {
        case .noConfig: return nil
        case .unreadable(let p, _): return p
        }
    }
}

enum Conf {
    /// Куда человек показал файл сам. Хранится в настройках приложения, потому
    /// что указатель steno переживает не всё: его затирает `steno setup` в
    /// другом каталоге, а тесты — и вовсе временным путём, который потом
    /// исчезает.
    static let overrideKey = "configPath"

    static func find() -> Result<Setup, SetupTrouble> {
        var searched: [String] = []
        for path in candidates(&searched) {
            guard FileManager.default.fileExists(atPath: path) else { continue }
            return read(configPath: path)
        }
        return .failure(.noConfig(searched: searched))
    }

    /// Порядок ровно тот же, что у steno: явное указание главнее указателя,
    /// указатель — умолчания. Каталог из указателя проверяется на месте:
    /// steno в rememberedConfigPath делает то же самое и по той же причине.
    private static func candidates(_ searched: inout [String]) -> [String] {
        var out: [String] = []
        func add(_ p: String?) {
            guard let p, !p.isEmpty else { return }
            if !out.contains(p) { out.append(p) }
        }
        if let manual = UserDefaults.standard.string(forKey: overrideKey) {
            add(manual)
        }
        add(ProcessInfo.processInfo.environment["STENO_CONFIG"])
        searched.append(pointerPath)
        add(pointerTarget())
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let fallback = home + "/steno/steno.json"
        searched.append(fallback)
        add(fallback)
        return out
    }

    static var pointerPath: String {
        FileManager.default.homeDirectoryForCurrentUser.path + "/.config/steno/path"
    }

    /// Что записал `steno setup`. Файл содержит путь к самому конфигу; если
    /// там оказался каталог — берём в нём steno.json, чтобы не спорить с
    /// будущими версиями о формате указателя.
    private static func pointerTarget() -> String? {
        guard let raw = try? String(contentsOfFile: pointerPath, encoding: .utf8) else { return nil }
        let path = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !path.isEmpty else { return nil }
        var dir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &dir) else { return nil }
        return dir.boolValue ? path + "/steno.json" : path
    }

    static func read(configPath: String) -> Result<Setup, SetupTrouble> {
        let data: Data
        do {
            data = try Data(contentsOf: URL(fileURLWithPath: configPath))
        } catch {
            return .failure(.unreadable(path: configPath, why: error.localizedDescription))
        }
        guard let root = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
            return .failure(.unreadable(path: configPath, why: L.t("это не JSON")))
        }
        let panel = root["panel"] as? [String: Any] ?? [:]
        // Умолчания — те же, что в DefaultConfig(): адрес :8080, пароль из
        // STENO_PANEL_PASSWORD, панель выключена, пока её не включили,
        // data_dir — ./data рядом с настройкой.
        let addr = (panel["addr"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? ":8080"
        let env = (panel["password_env"] as? String).flatMap { $0.isEmpty ? nil : $0 }
            ?? "STENO_PANEL_PASSWORD"
        let secure = panel["secure"] as? Bool ?? false
        let dir = (configPath as NSString).deletingLastPathComponent
        let envPath = dir + "/.env"
        let bot = root["bot"] as? [String: Any] ?? [:]
        let pass = password(env: env, envPath: envPath).flatMap { $0.isEmpty ? nil : $0 }
        return .success(Setup(
            configPath: configPath,
            dataDir: absolute(root["data_dir"] as? String ?? "./data", base: dir),
            maxRecording: duration(bot["max_duration"]) ?? 4 * 3600,
            addr: addr,
            base: url(addr: addr, secure: secure),
            password: pass,
            passwordEnv: env,
            envPath: envPath,
            panelEnabled: panel["enabled"] as? Bool ?? false,
            lang: root["lang"] as? String ?? ""))
    }

    /// Пути в настройке считаются от неё самой, а не от текущего каталога —
    /// то же правило, что в LoadConfig: у приложения в строке меню текущий
    /// каталог вообще «/», и относительный data_dir иначе указал бы в корень.
    private static func absolute(_ path: String, base: String) -> String {
        let p = path.isEmpty ? "./data" : path
        if p.hasPrefix("/") { return p }
        if p.hasPrefix("~") { return NSString(string: p).expandingTildeInPath }
        // standardizingPath убирает «./» и «..» — иначе человек читал бы в
        // сообщении об ошибке путь вида «/Users/имя/steno/./data/steno.db».
        return ((base as NSString).appendingPathComponent(p) as NSString).standardizingPath
    }

    /// «4h0m0s» — так Go записывает длительность. Число трактуем как
    /// наносекунды: столько же значит time.Duration без обёртки.
    static func duration(_ raw: Any?) -> TimeInterval? {
        if let n = raw as? Double { return n / 1e9 }
        guard let s = (raw as? String)?.trimmingCharacters(in: .whitespaces), !s.isEmpty
        else { return nil }
        var total: Double = 0
        var number = ""
        var seen = false
        let units: [Character: Double] = ["h": 3600, "m": 60, "s": 1]
        for ch in s {
            if ch.isNumber || ch == "." {
                number.append(ch)
            } else if let mult = units[ch], let value = Double(number) {
                total += value * mult
                number = ""
                seen = true
            } else if ch == "µ" || ch == "n" || ch == "u" {
                number = ""   // доли секунды нам не нужны
            }
        }
        return seen ? total : nil
    }

    /// Заданное окружение главнее файла — то же правило, что в loadDotEnv:
    /// в проде переменные приходят снаружи, и файл не должен их перебивать.
    private static func password(env: String, envPath: String) -> String? {
        if let v = ProcessInfo.processInfo.environment[env]?
            .trimmingCharacters(in: .whitespaces), !v.isEmpty {
            return v
        }
        return dotenv(envPath)[env]
    }

    /// Разбор .env — построчно и без затей, как в setup.go: комментарии,
    /// первый знак равенства, кавычки по краям.
    static func dotenv(_ path: String) -> [String: String] {
        guard let raw = try? String(contentsOfFile: path, encoding: .utf8) else { return [:] }
        var out: [String: String] = [:]
        for line in raw.split(separator: "\n", omittingEmptySubsequences: false) {
            let s = line.trimmingCharacters(in: .whitespaces)
            if s.isEmpty || s.hasPrefix("#") { continue }
            guard let eq = s.firstIndex(of: "=") else { continue }
            let k = String(s[s.startIndex..<eq]).trimmingCharacters(in: .whitespaces)
            var v = String(s[s.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
            v = v.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
            if !k.isEmpty { out[k] = v }
        }
        return out
    }

    /// Адрес из конфига — это адрес прослушивания, а не адрес для похода в
    /// гости. «:8080» и «0.0.0.0:8080» значат «на всех интерфейсах», и стучаться
    /// по ним нельзя — стучимся на петлю.
    static func url(addr: String, secure: Bool) -> URL? {
        let a = addr.trimmingCharacters(in: .whitespaces)
        guard !a.isEmpty else { return nil }
        var host = a
        var port = ""
        if let colon = a.lastIndex(of: ":"), !a.hasSuffix("]") {
            host = String(a[a.startIndex..<colon])
            port = String(a[a.index(after: colon)...])
        }
        if host.isEmpty || host == "0.0.0.0" || host == "::" || host == "[::]" {
            host = "127.0.0.1"
        }
        guard !port.isEmpty, Int(port) != nil else { return nil }
        return URL(string: "\(secure ? "https" : "http")://\(host):\(port)")
    }

    /// Домашний каталог в путях сворачивается в ~: длинный путь в узком меню
    /// переносится на три строки и читается хуже, чем не читается вовсе.
    static func pretty(_ path: String) -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        if path == home { return "~" }
        if path.hasPrefix(home + "/") { return "~" + path.dropFirst(home.count) }
        return path
    }
}
