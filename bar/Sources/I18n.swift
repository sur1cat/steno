import Foundation

// Язык приложения — тот же, что у steno, и берётся оттуда же.
//
// Порядок: «lang» из steno.json, потом STENO_LANG и обычные переменные
// локали, в конце — язык системы. Умолчание английское: steno лежит на
// GitHub, и тот, кто открывает его впервые, английского ждёт.
//
// Каталог устроен как gettext: ключ перевода — сама русская строка из кода.
// Незнакомая строка возвращается по-русски: пропуск виден на экране и чинится
// одной строкой в I18nEN.swift, а не оставляет пустое место.
enum L {
    static let ru = "ru"
    static let en = "en"

    private(set) static var lang: String = resolve(fromEnvironment())

    /// Язык из настройки. Зовётся, когда Setup нашёлся: до этого момента
    /// приложение уже успевает сказать пару фраз, и они идут на языке
    /// окружения.
    static func use(_ configured: String?) {
        guard let configured, !configured.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        lang = resolve(configured)
    }

    static func t(_ russian: String) -> String {
        if lang == ru { return russian }
        return englishCatalog[russian] ?? russian
    }

    /// Строка с подстановками. Ключ каталога — сам шаблон, места вставок
    /// помечены %@: порядок слов в переводе бывает другим, и склеивать куски
    /// плюсом означало бы запретить его менять.
    static func t(_ russian: String, _ args: String...) -> String {
        var out = t(russian)
        for a in args {
            guard let r = out.range(of: "%@") else { break }
            out.replaceSubrange(r, with: a)
        }
        return out
    }

    private static func resolve(_ raw: String?) -> String {
        let s = (raw ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        return s.hasPrefix(ru) ? ru : en
    }

    private static func fromEnvironment() -> String {
        let env = ProcessInfo.processInfo.environment
        for key in ["STENO_LANG", "LC_ALL", "LC_MESSAGES", "LANG"] {
            if let v = env[key], !v.trimmingCharacters(in: .whitespaces).isEmpty { return v }
        }
        return Locale.preferredLanguages.first ?? en
    }
}
