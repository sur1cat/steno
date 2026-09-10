import Foundation

// Как читаются числа и время.
//
// Правила взяты из web/app/src/lib/fmt.ts: один и тот же созвон в браузере и в
// строке меню должен называться одинаково — человек смотрит то туда, то сюда и
// сверяет глазами. Отсюда же и согласование числа со словом: «2 созвона» вместо
// «2 созвон» — это разница между интерфейсом и машинным переводом.

enum Format {
    /// Локаль для дат: та же, что язык интерфейса. Зашитая ru_RU выдавала
    /// «8 сентября» посреди английского экрана.
    static var locale: Locale { Locale(identifier: L.lang == L.ru ? "ru_RU" : "en_US") }

    /// Секундомер идущей записи. До часа — минуты и секунды: тикающие секунды
    /// в строке меню и есть доказательство, что запись живая.
    static func stopwatch(_ seconds: TimeInterval) -> String {
        let s = max(0, Int(seconds))
        let h = s / 3600, m = (s % 3600) / 60, sec = s % 60
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, sec) }
        return String(format: "%02d:%02d", m, sec)
    }

    /// «40 мин», «1 ч 20 мин» — как в durRu.
    static func duration(_ seconds: Int) -> String {
        if seconds <= 0 { return "" }
        let h = seconds / 3600, m = (seconds % 3600) / 60
        return h > 0 ? L.t("%@ ч %@ мин", "\(h)", "\(m)") : L.t("%@ мин", "\(m)")
    }

    static func money(_ usd: Double) -> String {
        String(format: "$%.3f", usd)
    }

    static func time(_ d: Date) -> String {
        let f = DateFormatter()
        f.locale = locale
        f.dateFormat = "HH:mm"
        return f.string(from: d)
    }


    /// «сегодня, 16:54» / «вчера, 16:54» / «8 сентября, 16:54» — как dateRu в
    /// панели. Для зависшей записи без даты не обойтись: «с 16:54» о позавчера
    /// читается как «сегодня в 16:54».
    static func when(_ d: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(d) { return L.t("сегодня, ") + time(d) }
        if cal.isDateInYesterday(d) { return L.t("вчера, ") + time(d) }
        let f = DateFormatter()
        f.locale = locale
        f.dateFormat = cal.component(.year, from: d) == cal.component(.year, from: Date())
            ? "d MMMM, HH:mm" : "d MMMM y, HH:mm"
        return f.string(from: d)
    }

    /// Пустое поле, записанное словами. Модель, разбирающая созвон, вместо
    /// пропуска нередко пишет «не назначен» или «не определён», и в строке меню
    /// это превращается в «не назначен · не определён» — шум вместо сведений.
    static func blank(_ raw: String) -> Bool {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if s.isEmpty { return true }
        let stubs: Set<String> = ["-", "—", "–", L.t("нет"), L.t("никто"), "tbd", "n/a", "na", "none",
                                  "unassigned", L.t("не назначен"), L.t("не назначено"), L.t("не назначена"),
                                  L.t("не определён"), L.t("не определен"), L.t("не определено"),
                                  L.t("не указан"), L.t("не указано"), L.t("не задан"), L.t("не задано"),
                                  L.t("без срока"), L.t("без исполнителя")]
        return stubs.contains(s)
    }

    /// Срок в человеческом виде. «2026-09-10» — это то, что кладёт модель, а
    /// читать в меню приятнее «10 сентября».
    static func due(_ raw: String) -> String? {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if blank(s) { return nil }
        let iso = DateFormatter()
        iso.locale = Locale(identifier: "en_US_POSIX")
        iso.dateFormat = "yyyy-MM-dd"
        guard let d = iso.date(from: s) else { return s }
        let cal = Calendar.current
        if cal.isDateInToday(d) { return L.t("сегодня") }
        if cal.isDateInTomorrow(d) { return L.t("завтра") }
        let out = DateFormatter()
        out.locale = Locale(identifier: "ru_RU")
        out.dateFormat = cal.component(.year, from: d) == cal.component(.year, from: Date())
            ? "d MMMM" : "d MMMM y"
        return out.string(from: d)
    }

    /// Дата покороче: «сегодня» ни к чему в списке, где почти всё сегодня, а
    /// «09.09» читается с одного взгляда и занимает колонку постоянной ширины.
    static func short(_ d: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(d) { return time(d) }
        // Дата, а не «вчера»: слово шире колонки по-английски и рвалось
        // посреди себя. В узкой колонке дата и однозначнее.
        let f = DateFormatter()
        f.locale = locale
        f.dateFormat = cal.component(.year, from: d) == cal.component(.year, from: Date())
            ? "dd.MM" : "MM.yy"
        return f.string(from: d)
    }

    /// Ссылка на созвон в куске текста — или ничего. Подставлять в поле
    /// «что-то похожее на ссылку» нельзя: человек копирует за день десятки
    /// строк, и позавчерашний адрес в поле молча уведёт бота не туда.
    /// Проверяем по тем же двум площадкам, которые умеет сам сервис.
    static func meetingLink(_ raw: String) -> String? {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard text.count < 2000 else { return nil }
        let patterns = [
            #"https://meet\.google\.com/[a-z]{3}-[a-z]{4}-[a-z]{3}"#,
            #"https://[a-z0-9.-]*jit\.si/[^\s]+"#,
            #"https://meet\.jit\.si/[^\s]+"#,
        ]
        for p in patterns {
            if let r = text.range(of: p, options: [.regularExpression, .caseInsensitive]) {
                return String(text[r])
            }
        }
        return nil
    }

    static func word(_ n: Int, _ one: String, _ few: String, _ many: String) -> String {
        let m10 = n % 10, m100 = n % 100
        if m10 == 1 && m100 != 11 { return one }
        if (2...4).contains(m10) && !(12...14).contains(m100) { return few }
        return many
    }

    static func plural(_ n: Int, _ one: String, _ few: String, _ many: String) -> String {
        "\(n) \(word(n, one, few, many))"
    }

    static func calls(_ n: Int) -> String { plural(n, L.t("созвон"), L.t("созвона"), L.t("созвонов")) }
}
