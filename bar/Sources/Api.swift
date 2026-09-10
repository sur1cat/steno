import Foundation

// Ходок в JSON-API панели — ради одного действия: позвать бота на созвон.
//
// Показывать записанное сюда незачем, это читается прямо из базы (Db). А вот
// увести бота на встречу умеет только живой сервис, и другого пути к нему,
// кроме панели, нет.
//
// Панель пускает по общему паролю и держит вход в подписанной cookie
// (panel.go): POST /api/login кладёт её, а всё остальное без неё отвечает 401.
// Никакого обходного пути тут нет и не нужно — приложение логинится честно тем
// же паролем, что человек вводит в браузере, и хранит cookie в памяти своего
// процесса: на диск её класть незачем, вход стоит один запрос.

/// Ответ панели на приглашение. Слова в message — её, не наши: она одна знает,
/// почему ссылка не подошла и есть ли свободное место под ещё одну запись.
struct InviteReply: Decodable {
    let status: String
    let message: String?
    let error: String?
}

/// Ответ панели на кнопку заметки. Слова в message тоже её: она одна знает,
/// занят ли микрофон, дала ли macOS к нему доступ и не идёт ли уже запись.
struct NoteReply: Decodable {
    let id: String?
    let status: String?
    let message: String?
    let error: String?
    let recording: Bool?
    let seconds: Int?
}

enum ApiError: Error, Equatable {
    /// До панели не достучались. Почти всегда это «сервис не запущен».
    case down(String)
    /// Пароль есть, но панель его не приняла.
    case badPassword
    /// На адресе кто-то есть, но это не панель steno.
    case notPanel
    /// Панель ответила, но не тем.
    case http(Int, String)
    case malformed(String)

    var message: String {
        switch self {
        case .down(let why): return why
        case .badPassword: return "панель не приняла пароль"
        case .notPanel: return "отвечает не панель steno"
        case .http(let code, let text): return text.isEmpty ? "ответ \(code)" : text
        case .malformed(let what): return "непонятный ответ: \(what)"
        }
    }
}

actor Api {
    private let base: URL
    private let password: String
    private let session: URLSession
    private var authorized = false

    init(base: URL, password: String) {
        self.base = base
        self.password = password
        let cfg = URLSessionConfiguration.ephemeral
        cfg.httpShouldSetCookies = true
        cfg.httpCookieAcceptPolicy = .always
        cfg.timeoutIntervalForRequest = 8
        cfg.waitsForConnectivity = false
        self.session = URLSession(configuration: cfg)
    }

    func post<T: Decodable>(_ path: String, body: [String: Any], as type: T.Type) async throws -> T {
        var r = request(path, method: "POST")
        r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        r.httpBody = try JSONSerialization.data(withJSONObject: body)
        return try decode(try await send(r))
    }

    /// Вход делается лениво и переделывается ровно один раз на запрос: cookie
    /// живёт месяц, но пароль панели могли сменить, а сервис — перезапустить.
    /// Повторять до бесконечности нельзя — неверный пароль сервис придерживает
    /// секундой, и настойчивое приложение превратилось бы в перебор.
    private func send(_ req: URLRequest) async throws -> Data {
        if !authorized { try await login() }
        let (data, code) = try await raw(req)
        if code == 401 {
            authorized = false
            try await login()
            let (again, code2) = try await raw(req)
            guard code2 == 200 else { throw ApiError.http(code2, errorText(again)) }
            return again
        }
        guard code == 200 || code == 202 else { throw ApiError.http(code, errorText(data)) }
        return data
    }

    private func login() async throws {
        var r = request("/api/login", method: "POST")
        r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        r.httpBody = try JSONSerialization.data(withJSONObject: ["password": password])
        let (data, code) = try await raw(r)
        if code == 401 { throw ApiError.badPassword }
        // Порт бывает занят чужим сервисом — на этой машине так и случилось.
        // Чужой сервер отвечает 404 на неизвестный ему адрес, и без отдельной
        // ветки это выглядело бы как «панель сломалась», хотя панели тут нет.
        if code == 404 { throw ApiError.notPanel }
        guard code == 200 else { throw ApiError.http(code, errorText(data)) }
        guard let o = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              o["authenticated"] != nil else {
            throw ApiError.notPanel
        }
        authorized = true
    }

    private func raw(_ req: URLRequest) async throws -> (Data, Int) {
        do {
            let (data, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            return (data, code)
        } catch let e as URLError {
            throw ApiError.down(Api.why(e))
        } catch {
            throw ApiError.down(error.localizedDescription)
        }
    }

    private func request(_ path: String, method: String) -> URLRequest {
        var r = URLRequest(url: base.appendingPathComponent(path))
        r.httpMethod = method
        r.setValue("steno-bar", forHTTPHeaderField: "User-Agent")
        return r
    }

    private func decode<T: Decodable>(_ data: Data) throws -> T {
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw ApiError.malformed(String(describing: T.self))
        }
    }

    /// Панель отвечает ошибками по-русски и своими словами — их и показываем,
    /// вместо того чтобы переводить код обратно в текст и терять подробность.
    private func errorText(_ data: Data) -> String {
        if let o = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
           let e = o["error"] as? String, !e.isEmpty {
            return e
        }
        return ""
    }

    /// Сетевые ошибки — человеческими словами. «Ошибка -1004» не говорит
    /// ничего; «соединение отклонено» говорит, что слушать некому.
    nonisolated static func why(_ e: URLError) -> String {
        switch e.code {
        case .cannotConnectToHost, .cannotFindHost:
            return "соединение отклонено"
        case .timedOut:
            return "ответа не дождался"
        case .networkConnectionLost:
            return "соединение оборвалось"
        case .notConnectedToInternet:
            return "сети нет"
        case .appTransportSecurityRequiresSecureConnection:
            return "macOS не пустила по http — панель не на петле, включи TLS"
        default:
            return e.localizedDescription
        }
    }
}
