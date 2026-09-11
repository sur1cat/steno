import AppKit
import SwiftUI

// Меню под иконкой.
//
// Устроено как маленькая панель с вкладками, а не как длинная лента: состояние
// сверху, разделы посередине, действия — внутри своего раздела. Прошлый вариант
// складывал всё в один столбец, и меню приходилось проглядывать сверху вниз,
// чтобы найти одну строку.
//
// Высота постоянная. Меню, которое прыгает от количества созвонов, каждый раз
// оказывается другой формы, и глазу приходится заново искать, где что; поэтому
// рамка стоит на месте, а список внутри вкладки прокручивается.
//
// Фон рисуем сами и непрозрачным. Окно строки меню по умолчанию просвечивает
// материалом, и обои проступают сквозь него цветной размывкой — на тёмной теме
// это выглядит поломкой отрисовки, а не оформлением.
//
// Правило содержания: отсюда узнают и делают, а не переходят. Задача
// закрывается здесь же, follow-up читается здесь же; браузер — по явной просьбе.

enum Tab: String, CaseIterable {
    case calls, tasks, projects

    var title: String {
        switch self {
        case .calls: return L.t("созвоны")
        case .tasks: return L.t("задачи")
        case .projects: return L.t("проекты")
        }
    }
}

struct PanelView: View {
    @ObservedObject var loader: Loader

    /// Вкладка запоминается между открытиями: тот, кто живёт в задачах, должен
    /// попадать в задачи, а не возвращаться каждый раз к созвонам.
    @AppStorage("tab") private var tabRaw = Tab.calls.rawValue
    @State private var picked = 0
    @State private var openedCall: Meeting?
    @State private var followup: Followup?
    @State private var loadingFollowup = false
    @State private var inviting = false
    @State private var link = ""
    @State private var linkFromClipboard = false
    @State private var titleText = ""
    @State private var sending = false
    @State private var inviteMessage: (ok: Bool, text: String)?
    @FocusState private var linkFocused: Bool

    private static let width: CGFloat = 360
    private static let bodyHeight: CGFloat = 288

    private var tab: Tab {
        get { Tab(rawValue: tabRaw) ?? .calls }
        nonmutating set { tabRaw = newValue.rawValue }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            tabs
            Rule()
            body_
                .frame(width: Self.width, height: Self.bodyHeight, alignment: .topLeading)
            Rule()
            footer
        }
        .frame(width: Self.width)
        .background(Ground())
        .onChange(of: tabRaw) { _, _ in
            picked = 0
            openedCall = nil
        }
    }

    // --- шапка ---------------------------------------------------------------

    /// Микрофон вместо волны, пока идёт заметка: это её признак и в строке
    /// меню, и здесь — одно и то же должно выглядеть одинаково.
    private var headerIcon: String {
        loader.liveNote != nil ? "mic.fill" : "waveform"
    }

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: headerIcon).font(.system(size: 12, weight: .semibold))
                .foregroundStyle(loader.snapshot.live.isEmpty ? AnyShapeStyle(.secondary)
                                                              : AnyShapeStyle(Color.red))
            // Идущая запись видна из любой вкладки: это то, ради чего в строку
            // меню и смотрят, и прятать её за выбором раздела нельзя.
            if let call = loader.snapshot.live.first {
                Text(Format.stopwatch(loader.now.timeIntervalSince(call.started)))
                    .font(.system(size: 12, weight: .semibold).monospacedDigit())
                    .foregroundStyle(Color.red)
                Text(call.isNote ? L.t("заметка") : (call.title.isEmpty ? L.t("созвон без названия") : call.title))
                    .font(.system(size: 11.5)).foregroundStyle(.secondary).lineLimit(1)
                if loader.snapshot.live.count > 1 {
                    Text("+\(loader.snapshot.live.count - 1)")
                        .font(.system(size: 11).monospacedDigit()).foregroundStyle(.secondary)
                }
            } else {
                Text("steno").font(.system(size: 12.5, weight: .semibold))
            }
            Spacer(minLength: 6)
            noteButton
            IconButton(icon: "arrow.clockwise", help: L.t("обновить")) {
                Task { await loader.refresh() }
            }
            Menu {
                Toggle(L.t("Запускать при входе в систему"), isOn: Binding(
                    get: { loader.autostartOn },
                    set: { on in Task { await loader.setAutostart(on) } }))
                    .disabled(loader.setup == nil || loader.busyWithService)
                // Выключатели агента — здесь, а не в веб-панели: это право
                // писать файлы на этой машине, и даёт его тот, кто за ней
                // сидит. Меняются командой steno agent, файл правит она же.
                Toggle(L.t("Отдавать задачи агенту"), isOn: Binding(
                    get: { loader.setup?.agentEnabled ?? false },
                    set: { on in Task { await loader.setAgent(on) } }))
                    .disabled(loader.setup == nil || loader.busyWithService)
                Toggle(L.t("Собирать ТЗ после каждого разбора"), isOn: Binding(
                    get: { loader.setup?.agentAutoSpec ?? false },
                    set: { on in Task { await loader.setAgentAuto(on) } }))
                    .disabled(loader.setup == nil || loader.busyWithService)
                Button(L.t("Открыть панель в браузере")) { open("/") }
                    .disabled(!loader.panelReachable)
                Button(L.t("Показать лог сервиса")) { revealLog() }
                    .disabled(loader.setup == nil)
                Divider()
                if let path = loader.setup?.configPath ?? loader.trouble?.configPath {
                    Text(Conf.pretty(path))
                }
                Button(L.t("Выбрать другой steno.json…")) {
                    if chooseConfig() { Task { await loader.refresh() } }
                }
                Divider()
                Button(L.t("Выйти")) { NSApplication.shared.terminate(nil) }
                    .keyboardShortcut("q")
            } label: {
                Image(systemName: "ellipsis").font(.system(size: 11))
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .frame(width: 16)
        }
        .padding(.horizontal, 12).padding(.top, 9).padding(.bottom, 8)
    }

    // --- вкладки -------------------------------------------------------------

    private var tabs: some View {
        HStack(spacing: 2) {
            ForEach(Tab.allCases, id: \.self) { t in
                TabButton(title: t.title, count: count(t), active: t == tab) { tab = t }
            }
            Spacer()
            // Стрелки переключают вкладки; кнопки невидимы и нужны только ради
            // сочетаний клавиш — окно строки меню своего меню команд не имеет.
            Button("") { move(-1) }.keyboardShortcut(.leftArrow, modifiers: [])
            Button("") { move(1) }.keyboardShortcut(.rightArrow, modifiers: [])
            Button("") { tab = .calls }.keyboardShortcut("1", modifiers: .command)
            Button("") { tab = .tasks }.keyboardShortcut("2", modifiers: .command)
            Button("") { tab = .projects }.keyboardShortcut("3", modifiers: .command)
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 10).padding(.bottom, 7)
    }

    private func count(_ t: Tab) -> Int? {
        switch t {
        case .calls: return loader.snapshot.meetings.isEmpty ? nil : loader.snapshot.today.count
        case .tasks: return loader.snapshot.tasks.isEmpty ? nil : loader.snapshot.tasks.count
        case .projects: return loader.snapshot.projects.isEmpty ? nil : loader.snapshot.projects.count
        }
    }

    private func move(_ step: Int) {
        let all = Tab.allCases
        guard let i = all.firstIndex(of: tab) else { return }
        tab = all[(i + step + all.count) % all.count]
    }

    // --- содержимое ----------------------------------------------------------

    @ViewBuilder private var body_: some View {
        switch loader.health {
        case .starting:
            Hint(text: L.t("смотрю, что происходит…"))
        case .broken(let title, let detail):
            TroubleView(title: title, detail: detail, loader: loader)
        case .ok:
            if inviting {
                inviteForm
            } else if let call = openedCall {
                followupView(call)
            } else {
                switch tab {
                case .calls: callsTab
                case .tasks: tasksTab
                case .projects: projectsTab
                }
            }
        }
    }

    // --- вкладка «созвоны» ---------------------------------------------------

    @ViewBuilder private var callsTab: some View {
        let snap = loader.snapshot
        if snap.meetings.isEmpty && snap.live.isEmpty && snap.stuck.isEmpty {
            Empty(title: L.t("Созвонов ещё не было"),
                  detail: L.t("Вставь ссылку на встречу — бот придёт, запишет разговор ")
                        + L.t("и разберёт его сам: выжимка, задачи, решения."),
                  action: (L.t("Позвать бота на созвон"), { openInvite() }))
        } else {
            List_ {
                if !snap.today.isEmpty || snap.todaySpend > 0 {
                    SummaryLine(left: L.t("сегодня ") + Format.calls(snap.today.count + snap.live.count),
                                right: snap.todaySpend > 0 ? Format.money(snap.todaySpend) : "")
                }
                ForEach(Array(snap.stuck.enumerated()), id: \.element.id) { _, call in
                    StuckLine(call: call)
                }
                ForEach(Array(snap.meetings.enumerated()), id: \.element.id) { i, m in
                    Line(selected: picked == i, action: { pickCall(i, m) }) {
                        HStack(spacing: 8) {
                            Text(Format.short(m.started))
                                .font(.system(size: 11).monospacedDigit())
                                .foregroundStyle(.tertiary)
                                .frame(width: 42, alignment: .leading)
                            Text(m.title.isEmpty ? L.t("без названия") : m.title)
                                .font(.system(size: 12.5)).lineLimit(1)
                            Spacer(minLength: 4)
                            if m.troubled {
                                Text(m.statusWord).font(.system(size: 10.5))
                                    .foregroundStyle(.orange)
                            } else if m.hasFollowup {
                                Image(systemName: "chevron.right").font(.system(size: 9))
                                    .foregroundStyle(.tertiary)
                            } else if m.seconds > 0 {
                                Text(Format.duration(m.seconds))
                                    .font(.system(size: 11).monospacedDigit())
                                    .foregroundStyle(.tertiary)
                            }
                        }
                    }
                }
            }
        }
    }

    private func pickCall(_ i: Int, _ m: Meeting) {
        picked = i
        guard m.hasFollowup else { return }
        openedCall = m
        followup = nil
        loadingFollowup = true
        Task {
            followup = await loader.followup(m.id)
            loadingFollowup = false
        }
    }

    /// Follow-up прямо в меню: коротко о чём договорились, кто что должен и
    /// какие решения приняли. Ради этого в браузер ходить не надо.
    @ViewBuilder private func followupView(_ call: Meeting) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                IconButton(icon: "chevron.left", help: L.t("назад")) { openedCall = nil }
                Text(call.title.isEmpty ? L.t("без названия") : call.title)
                    .font(.system(size: 12, weight: .semibold)).lineLimit(1)
                Spacer(minLength: 4)
                Text(Format.short(call.started))
                    .font(.system(size: 10.5).monospacedDigit()).foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 12).padding(.vertical, 7)
            Rule()
            if loadingFollowup {
                Hint(text: L.t("читаю follow-up…"))
            } else if let f = followup, !f.isEmpty {
                List_ {
                    if !f.tldr.isEmpty {
                        Caption(L.t("о чём договорились"))
                        ForEach(Array(f.tldr.enumerated()), id: \.offset) { _, line in
                            Bullet(text: line)
                        }
                    }
                    if !f.tasks.isEmpty {
                        Caption(L.t("задачи"))
                        ForEach(Array(f.tasks.enumerated()), id: \.offset) { _, t in
                            Bullet(text: t.what,
                                   meta: Format.blank(t.owner) ? nil : t.owner)
                        }
                    }
                    if !f.decisions.isEmpty {
                        Caption(L.t("решения"))
                        ForEach(Array(f.decisions.enumerated()), id: \.offset) { _, d in
                            Bullet(text: d)
                        }
                    }
                    if !f.questions.isEmpty {
                        Caption(L.t("открытые вопросы"))
                        ForEach(Array(f.questions.enumerated()), id: \.offset) { _, q in
                            Bullet(text: q)
                        }
                    }
                    if loader.panelReachable {
                        Button(L.t("весь follow-up в панели ↗")) { open("/m/\(call.id)") }
                            .buttonStyle(LinkLike())
                            .padding(.horizontal, 12).padding(.top, 4)
                    }
                }
            } else {
                Empty(title: L.t("Follow-up пустой"),
                      detail: L.t("Разговор записан, но выжимка не собралась — так бывает, ")
                            + L.t("когда созвон оборвался в самом начале."))
            }
        }
    }

    // --- вкладка «задачи» ----------------------------------------------------

    @ViewBuilder private var tasksTab: some View {
        let snap = loader.snapshot
        if snap.tasks.isEmpty && snap.questions.isEmpty {
            Empty(title: L.t("Задач нет"),
                  detail: loader.snapshot.meetings.isEmpty
                        ? L.t("Они появляются сами: сходи с ботом на созвон, и он разберёт, ")
                          + L.t("кто что обещал сделать.")
                        : L.t("Всё разобрано и закрыто. Новые появятся сами — после ")
                          + L.t("следующего созвона."))
        } else {
            List_ {
                ForEach(Array(snap.tasks.enumerated()), id: \.element.id) { i, item in
                    Line(selected: picked == i, action: { picked = i }) {
                        HStack(alignment: .top, spacing: 8) {
                            // Отметить сделанной — прямо здесь: ради одной
                            // галочки открывать браузер незачем.
                            Button {
                                Task { await loader.closeTask(item) }
                            } label: {
                                Image(systemName: "circle")
                                    .font(.system(size: 12))
                                    .foregroundStyle(.tertiary)
                            }
                            .buttonStyle(.plain)
                            .help(L.t("отметить сделанной"))
                            VStack(alignment: .leading, spacing: 2) {
                                Text(item.text).font(.system(size: 12.5)).lineLimit(2)
                                    .fixedSize(horizontal: false, vertical: true)
                                Text(meta(item)).font(.system(size: 10.5))
                                    .foregroundStyle(.tertiary).lineLimit(1)
                                specLine(item)
                            }
                            Spacer(minLength: 4)
                            specAction(item)
                        }
                    }
                }
                if !snap.questions.isEmpty {
                    Caption(L.t("открытые вопросы"))
                    ForEach(snap.questions) { q in
                        Bullet(text: q.text,
                               meta: Format.blank(q.project) ? nil : q.project)
                    }
                }
            }
        }
    }

    /// ТЗ по задаче — строкой под задачей: есть ли, что с ним. Прямо здесь, а
    /// не за кликом: «у этой задачи уже есть ветка» — то, ради чего в строку
    /// меню и смотрят между созвонами.
    @ViewBuilder private func specLine(_ item: Item) -> some View {
        if loader.specRequested[item.id] != nil {
            Text(L.t("ТЗ: собирается…")).font(.system(size: 10.5)).foregroundStyle(.orange)
        } else if let sp = loader.snapshot.specs[item.id] {
            HStack(spacing: 4) {
                Circle().fill(sp.tone).frame(width: 5, height: 5)
                Text(sp.word).font(.system(size: 10.5)).foregroundStyle(.secondary).lineLimit(1)
                if !sp.branch.isEmpty && (sp.status == "done" || sp.status == "running") {
                    Text(sp.branch).font(.system(size: 10, design: .monospaced))
                        .foregroundStyle(.tertiary).lineLimit(1)
                }
            }
        }
    }

    /// Одно действие у правого края строки: собрать ТЗ, отдать агенту или
    /// открыть готовое. Ровно одно, потому что ширина 360 не вмещает два.
    @ViewBuilder private func specAction(_ item: Item) -> some View {
        let sp = loader.snapshot.specs[item.id]
        let building = loader.specRequested[item.id] != nil
        if !Format.blank(item.project) && item.project != "не определён" && item.kind == "task" {
            if building {
                EmptyView()
            } else if let sp {
                if sp.status == "draft" && sp.runnable && loader.setup?.agentEnabled == true {
                    Button(L.t("агенту")) { Task { await loader.runSpec(sp) } }
                        .buttonStyle(Flat(prominent: true))
                        .disabled(loader.specBusy || !loader.service.isRunning)
                        .help(L.t("отдать ТЗ агенту: рабочая копия, ветка, коммит — никогда push"))
                } else if loader.panelReachable {
                    Button(L.t("открыть")) { open("/s/\(sp.id)") }
                        .buttonStyle(Flat())
                        .help(L.t("ТЗ целиком — в панели"))
                }
            } else {
                Button(L.t("ТЗ")) { Task { await loader.buildSpec(item) } }
                    .buttonStyle(Flat())
                    .disabled(loader.specBusy || !loader.service.isRunning)
                    .help(loader.service.isRunning
                          ? L.t("написать ТЗ по репозиторию проекта — около минуты")
                          : L.t("ТЗ собирает сервис, а он не запущен"))
            }
        }
    }

    /// Исполнитель, срок и проект — то, чего не хватает в самой формулировке.
    private func meta(_ item: Item) -> String {
        var parts = [Format.blank(item.owner) ? L.t("без исполнителя") : item.owner]
        if let due = Format.due(item.due) { parts.append(L.t("до %@", due)) }
        // Проект тоже бывает записан словами «не определён».
        if !Format.blank(item.project) { parts.append(item.project) }
        return parts.joined(separator: " · ")
    }

    // --- вкладка «проекты» ---------------------------------------------------

    @ViewBuilder private var projectsTab: some View {
        let projects = loader.snapshot.projects
        if projects.isEmpty {
            Empty(title: L.t("Проектов пока нет"),
                  detail: L.t("Проект собирает задачи и решения по всем созвонам сразу, ")
                        + L.t("а не по одному. Завести его можно в панели."),
                  action: loader.panelReachable
                        ? (L.t("Открыть панель"), { open("/projects") }) : nil)
        } else {
            List_ {
                ForEach(Array(projects.enumerated()), id: \.element.id) { i, p in
                    Line(selected: picked == i, action: { picked = i }) {
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 8) {
                                Text(p.name).font(.system(size: 12.5, weight: .medium))
                                Spacer(minLength: 4)
                                Text(numbers(p)).font(.system(size: 10.5).monospacedDigit())
                                    .foregroundStyle(.tertiary)
                            }
                            if !p.about.isEmpty {
                                Text(p.about).font(.system(size: 10.5))
                                    .foregroundStyle(.tertiary).lineLimit(2)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                }
            }
        }
    }

    private func numbers(_ p: ProjectRow) -> String {
        var parts: [String] = []
        if p.tasks > 0 { parts.append(L.t("%@ задач", "\(p.tasks)")) }
        if p.questions > 0 { parts.append(L.t("%@ вопр.", "\(p.questions)")) }
        if p.done > 0 { parts.append(L.t("%@ закрыто", "\(p.done)")) }
        return parts.isEmpty ? L.t("пусто") : parts.joined(separator: " · ")
    }

    // --- приглашение ---------------------------------------------------------

    /// Форма спрятана за кнопкой: зовут бота редко, а место она занимала бы
    /// всегда. Разворачивается на месте вкладки и так же сворачивается.
    private var inviteForm: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                IconButton(icon: "chevron.left", help: L.t("назад")) { closeInvite() }
                Text(L.t("Позвать бота на созвон")).font(.system(size: 12, weight: .semibold))
                Spacer()
            }
            .padding(.bottom, 2)
            TextField("https://meet.google.com/…", text: $link)
                .textFieldStyle(.plain)
                .font(.system(size: 12.5))
                .padding(.horizontal, 8).padding(.vertical, 6)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.06)))
                .overlay(RoundedRectangle(cornerRadius: 6)
                    .stroke(Color.primary.opacity(0.12), lineWidth: 1))
                .focused($linkFocused)
                .onSubmit { send() }
            if linkFromClipboard {
                HStack(spacing: 6) {
                    Text(L.t("ссылка из буфера обмена")).font(.system(size: 10.5))
                        .foregroundStyle(.tertiary)
                    Button(L.t("очистить")) {
                        link = ""
                        linkFromClipboard = false
                    }
                    .buttonStyle(LinkLike())
                }
            }
            TextField(L.t("название, если нужно"), text: $titleText)
                .textFieldStyle(.plain)
                .font(.system(size: 12.5))
                .padding(.horizontal, 8).padding(.vertical, 6)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.06)))
                .overlay(RoundedRectangle(cornerRadius: 6)
                    .stroke(Color.primary.opacity(0.12), lineWidth: 1))
                .onSubmit { send() }
            // Позвать бота может только живой сервис — и сказать об этом надо
            // здесь, где зовут, а не вместо всего содержимого меню.
            if let why = loader.setup?.inviteTrouble {
                Text(why).font(.system(size: 10.5)).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            } else if !loader.service.isRunning {
                HStack(spacing: 8) {
                    Text(L.t("нужен запущенный сервис")).font(.system(size: 10.5))
                        .foregroundStyle(.orange)
                    Button(loader.busyWithService ? L.t("запускаю…") : L.t("запустить")) {
                        Task { await loader.startService() }
                    }
                    .buttonStyle(Flat())
                    .disabled(loader.busyWithService)
                }
            }
            HStack(spacing: 8) {
                Button(sending ? L.t("зову…") : L.t("Позвать")) { send() }
                    .buttonStyle(Flat(prominent: true))
                    .keyboardShortcut(.defaultAction)
                    .disabled(sending || !canInvite
                              || link.trimmingCharacters(in: .whitespaces).isEmpty)
                Button(L.t("Отмена")) { closeInvite() }
                    .buttonStyle(Flat())
                Spacer()
            }
            if let m = inviteMessage {
                Text(m.text).font(.system(size: 10.5))
                    .foregroundStyle(m.ok ? Color.green : Color.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12).padding(.vertical, 10)
    }

    // --- подвал --------------------------------------------------------------

    private var footer: some View {
        HStack(spacing: 8) {
            // Отмена закрытия живёт в подвале: это единственное место, которое
            // видно из любой вкладки, а промахнуться галочкой легко.
            if let m = loader.noteMessage {
                Image(systemName: m.ok ? "mic.fill" : "exclamationmark.triangle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(m.ok ? Color.green : Color.orange)
                Text(m.text).font(.system(size: 10.5)).lineLimit(2)
                    .foregroundStyle(m.ok ? AnyShapeStyle(.secondary) : AnyShapeStyle(Color.orange))
                Spacer(minLength: 4)
                IconButton(icon: "xmark", help: L.t("скрыть")) { loader.forgetNoteMessage() }
            } else if let m = loader.specMessage {
                // Ответ сервиса на кнопку ТЗ — его словами: он один знает,
                // почему задача не про код или чего не хватает исполнителю.
                Image(systemName: m.ok ? "doc.text" : "exclamationmark.triangle.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(m.ok ? Color.green : Color.orange)
                Text(m.text).font(.system(size: 10.5)).lineLimit(2)
                    .foregroundStyle(m.ok ? AnyShapeStyle(.secondary) : AnyShapeStyle(Color.orange))
                Spacer(minLength: 4)
                IconButton(icon: "xmark", help: L.t("скрыть")) { loader.forgetSpecMessage() }
            } else if let closed = loader.justClosed {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 10)).foregroundStyle(.green)
                Text(closed.text).font(.system(size: 10.5)).lineLimit(1)
                    .foregroundStyle(.secondary)
                Button(L.t("вернуть")) { Task { await loader.undoClose() } }
                    .buttonStyle(LinkLike())
                Spacer(minLength: 4)
                IconButton(icon: "xmark", help: L.t("скрыть")) { loader.forgetClosed() }
            } else if let why = loader.writeError {
                Text(L.t("не записалось: %@", why)).font(.system(size: 10.5))
                    .foregroundStyle(.orange).lineLimit(1)
                Spacer(minLength: 4)
            } else {
                Circle()
                    .fill(loader.service.isRunning ? Color.green.opacity(0.7) : Color.orange)
                    .frame(width: 6, height: 6)
                Text(serviceLine).font(.system(size: 10.5))
                    .foregroundStyle(loader.service.isRunning ? AnyShapeStyle(.tertiary)
                                                              : AnyShapeStyle(Color.orange))
                    .lineLimit(1)
                if !loader.service.isRunning && loader.setup != nil {
                    Button(loader.busyWithService ? L.t("запускаю…") : L.t("запустить")) {
                        Task { await loader.startService() }
                    }
                    .buttonStyle(Flat())
                    .disabled(loader.busyWithService)
                }
                Spacer(minLength: 4)
                Button(L.t("+ позвать бота")) { openInvite() }
                    .buttonStyle(Flat())
                    .disabled(inviting)
            }
        }
        .padding(.horizontal, 10).padding(.vertical, 7)
    }

    /// Кнопка заметки. Стоит в шапке, а не в подвале: заметку наговаривают на
    /// ходу, и до неё должно быть одно движение из любой вкладки. В подвале она
    /// делила бы строку с «позвать бота» и состоянием сервиса — на 360 точках
    /// это три подписи в ряд, то есть обрезанный текст.
    ///
    /// Состояние берётся из базы (liveNote), а не из своего флага: приложение
    /// могли перезапустить посреди записи, и кнопка обязана показывать то, что
    /// есть на самом деле, а не то, что помнит.
    @ViewBuilder private var noteButton: some View {
        if loader.liveNote != nil {
            Button(L.t("отмена")) { Task { await loader.cancelNote() } }
                .buttonStyle(LinkLike())
                .disabled(loader.noteBusy)
                .help(L.t("выбросить запись, не разбирая"))
            // Без секундомера на самой кнопке: он уже бежит слева, красным и
            // крупно. Вторые те же цифры не добавляют ничего, зато переносят
            // подпись на две строки — панель шириной 360 этого не прощает.
            Button {
                Task { await loader.stopNote() }
            } label: {
                Label(L.t("стоп"), systemImage: "stop.fill")
                    .labelStyle(.titleAndIcon)
                    .lineLimit(1)
                    .fixedSize()
            }
            .buttonStyle(Flat())
            .disabled(loader.noteBusy)
            .keyboardShortcut("n", modifiers: .command)
            .help(L.t("остановить и разобрать (⌘N)"))
        } else {
            Button {
                Task { await loader.startNote() }
            } label: {
                Label(loader.noteBusy ? L.t("включаю…") : L.t("заметка"), systemImage: "mic.fill")
                    .labelStyle(.titleAndIcon)
                    .lineLimit(1)
                    .fixedSize()
            }
            .buttonStyle(Flat())
            .disabled(loader.noteBusy || !loader.service.isRunning)
            .keyboardShortcut("n", modifiers: .command)
            .help(loader.service.isRunning
                  ? L.t("наговорить заметку — запись начнётся сразу (⌘N)")
                  : L.t("заметку пишет сервис, а он не запущен"))
        }
    }

    private var serviceLine: String {
        switch loader.service {
        case .running(_, let since):
            return L.t("сервис с ") + (Calendar.current.isDateInToday(since)
                                  ? Format.time(since) : Format.when(since))
        // Оставшийся pid-файл — обычное дело после steno stop: steno не удаляет
        // его нарочно (release в daemon.go), так что это не авария.
        case .stale, .stopped:
            return L.t("сервис не запущен")
        }
    }

    // --- поступки ------------------------------------------------------------

    private var canInvite: Bool {
        loader.service.isRunning && loader.setup?.inviteTrouble == nil
    }

    private func open(_ path: String) {
        guard let base = loader.setup?.base else { return }
        NSWorkspace.shared.open(base.appendingPathComponent(path))
    }

    private func revealLog() {
        guard let path = loader.setup?.configPath ?? loader.trouble?.configPath else { return }
        Daemon.revealLog(configPath: path)
    }

    /// Поле всегда начинается пустым, и заполняется только если в буфере лежит
    /// именно ссылка на созвон. Прошлый вариант помнил введённое с прошлого раза
    /// и подставлял «похожее на ссылку» — человек набирал название новой
    /// встречи, а звали его на позавчерашнюю.
    private func openInvite() {
        inviting = true
        inviteMessage = nil
        titleText = ""
        link = ""
        linkFromClipboard = false
        if let clip = NSPasteboard.general.string(forType: .string),
           let found = Format.meetingLink(clip) {
            link = found
            linkFromClipboard = true
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { linkFocused = true }
    }

    private func closeInvite() {
        inviting = false
        inviteMessage = nil
        link = ""
        titleText = ""
        linkFromClipboard = false
    }

    private func send() {
        let url = link.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !url.isEmpty, !sending, canInvite else { return }
        sending = true
        Task {
            let r = await loader.invite(url: url, title: titleText)
            sending = false
            inviteMessage = r
            if r.ok {
                link = ""
                titleText = ""
                linkFromClipboard = false
            }
        }
    }
}

/// Спросить у человека, где лежит steno.json, и запомнить ответ.
@MainActor
func chooseConfig() -> Bool {
    // Приложение без окон само в фокус не выходит, а невидимый модальный
    // диалог выглядит как зависший компьютер.
    NSApplication.shared.activate(ignoringOtherApps: true)
    let p = NSOpenPanel()
    p.title = L.t("Где лежит настройка steno")
    p.message = L.t("Выбери steno.json — рядом с ним лежат data/ с базой и .env")
    p.allowedContentTypes = [.json]
    p.canChooseDirectories = false
    p.allowsMultipleSelection = false
    guard p.runModal() == .OK, let url = p.url else { return false }
    UserDefaults.standard.set(url.path, forKey: Conf.overrideKey)
    return true
}

// --- кирпичики ---------------------------------------------------------------

/// Непрозрачная подложка меню. Без неё сквозь окно просвечивают обои, и низ
/// панели уходит в цветную размывку.
struct Ground: View {
    var body: some View {
        Rectangle().fill(Color(nsColor: .windowBackgroundColor))
    }
}

/// Разделитель потоньше системного: Divider в плотном меню читается как рамка.
struct Rule: View {
    var body: some View {
        Rectangle().fill(Color.primary.opacity(0.09)).frame(height: 1)
    }
}

struct TabButton: View {
    let title: String
    let count: Int?
    let active: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Text(title).font(.system(size: 11.5, weight: active ? .semibold : .regular))
                if let count {
                    Text("\(count)").font(.system(size: 10).monospacedDigit())
                        .foregroundStyle(active ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tertiary))
                }
            }
            .foregroundStyle(active ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
            .padding(.horizontal, 9).padding(.vertical, 4)
            .background(RoundedRectangle(cornerRadius: 6)
                .fill(active ? Color.primary.opacity(0.10)
                             : (hovering ? Color.primary.opacity(0.05) : .clear)))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }
}

/// Прокручиваемый список вкладки. Рамка у него постоянная, содержимое — любое.
struct List_<Content: View>: View {
    @ViewBuilder let content: () -> Content

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 1) { content() }
                .padding(.vertical, 6)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

/// Строка списка: подсветка под курсором и метка выбранной строки слева —
/// так же, как в k9s, где выбранное видно, не отрывая глаз от колонки.
struct Line<Content: View>: View {
    var selected: Bool = false
    let action: () -> Void
    @ViewBuilder let content: () -> Content
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 0) {
                Rectangle()
                    .fill(selected ? Color.accentColor : .clear)
                    .frame(width: 2)
                content()
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .contentShape(Rectangle())
            .background(hovering ? Color.primary.opacity(0.06)
                                 : (selected ? Color.primary.opacity(0.035) : .clear))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }
}

struct Caption: View {
    let text: String
    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text.uppercased())
            .font(.system(size: 9.5, weight: .semibold)).tracking(0.7)
            .foregroundStyle(.tertiary)
            .padding(.horizontal, 12).padding(.top, 7).padding(.bottom, 2)
    }
}

struct Bullet: View {
    let text: String
    var meta: String?

    var body: some View {
        HStack(alignment: .top, spacing: 7) {
            Text("·").font(.system(size: 12)).foregroundStyle(.tertiary)
            VStack(alignment: .leading, spacing: 1) {
                Text(text).font(.system(size: 12)).fixedSize(horizontal: false, vertical: true)
                if let meta, !meta.isEmpty {
                    Text(meta).font(.system(size: 10.5)).foregroundStyle(.tertiary)
                }
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 3)
    }
}

struct SummaryLine: View {
    let left: String
    let right: String

    var body: some View {
        HStack {
            Text(left).font(.system(size: 10.5)).foregroundStyle(.tertiary)
            Spacer()
            if !right.isEmpty {
                Text(right).font(.system(size: 10.5).monospacedDigit())
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 12).padding(.bottom, 3)
    }
}

struct StuckLine: View {
    let call: LiveCall

    var body: some View {
        HStack(alignment: .top, spacing: 7) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 9)).foregroundStyle(.orange)
            Text(L.t("«%@» от %@ осталась незакрытой — сервис остановили посреди созвона",
                     call.title.isEmpty ? L.t("созвон без названия") : call.title,
                     Format.when(call.started)))
                .font(.system(size: 10.5)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, 12).padding(.vertical, 4)
    }
}

/// Пустая вкладка объясняет, что делать, и по возможности предлагает это
/// сделать. Пустота без объяснения выглядит как поломка приложения, а не как
/// «ещё ничего не было»; пустота с объяснением, но без кнопки, отправляет
/// человека искать, где же её нажать.
struct Empty: View {
    let title: String
    let detail: String
    var action: (title: String, run: () -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Spacer(minLength: 0)
            Text(title).font(.system(size: 13, weight: .semibold))
            Text(detail).font(.system(size: 11.5)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let action {
                Button(action.title) { action.run() }
                    .buttonStyle(Flat(prominent: true))
                    .padding(.top, 4)
            }
            Spacer(minLength: 0)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        .padding(.horizontal, 16)
    }
}

struct Hint: View {
    let text: String

    var body: some View {
        Text(text).font(.system(size: 11.5)).foregroundStyle(.secondary)
            .padding(.horizontal, 14).padding(.vertical, 12)
    }
}

struct IconButton: View {
    let icon: String
    let help: String
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Image(systemName: icon).font(.system(size: 10.5))
                .foregroundStyle(.secondary)
                .padding(4)
                .background(RoundedRectangle(cornerRadius: 5)
                    .fill(hovering ? Color.primary.opacity(0.08) : .clear))
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(help)
    }
}

/// Кнопки в языке меню, а не системного диалога: синяя капсула посреди плотной
/// тёмной разметки выглядит куском чужого окна.
struct Flat: ButtonStyle {
    var prominent = false
    @Environment(\.isEnabled) private var enabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 11, weight: .medium))
            .padding(.horizontal, 9).padding(.vertical, 4)
            .background(RoundedRectangle(cornerRadius: 6).fill(fill(configuration.isPressed)))
            .foregroundStyle(prominent && enabled ? AnyShapeStyle(Color.accentColor)
                                                  : AnyShapeStyle(enabled ? .primary : .tertiary))
            .opacity(enabled ? 1 : 0.8)
    }

    /// Заливка приглушённая даже у главной кнопки: сплошная синяя капсула в
    /// плотной разметке кричит громче всего содержимого меню.
    private func fill(_ pressed: Bool) -> Color {
        guard enabled else { return Color.primary.opacity(0.05) }
        if prominent { return Color.accentColor.opacity(pressed ? 0.30 : 0.18) }
        return Color.primary.opacity(pressed ? 0.16 : 0.08)
    }
}

/// Ссылка внутри меню: подчёркнутого синего в плотной разметке хватает одного.
struct LinkLike: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 10.5))
            .foregroundStyle(configuration.isPressed ? AnyShapeStyle(.secondary)
                                                     : AnyShapeStyle(Color.accentColor))
    }
}

/// Поломка чтения: что случилось и чем чинить.
struct TroubleView: View {
    let title: String
    let detail: String
    @ObservedObject var loader: Loader

    /// Базы нет ровно потому, что сервис ни разу не поднимался, — значит
    /// починка здесь та же кнопка, что и при упавшем сервисе.
    private var canStart: Bool {
        loader.setup != nil && !loader.service.isRunning
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 11)).foregroundStyle(.orange)
                Text(title).font(.system(size: 12.5, weight: .semibold))
            }
            Text(detail).font(.system(size: 11)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 8) {
                if canStart {
                    Button(loader.busyWithService ? L.t("запускаю…") : L.t("Запустить сервис")) {
                        Task { await loader.startService() }
                    }
                    .buttonStyle(Flat(prominent: true))
                    .disabled(loader.busyWithService)
                }
                Button(L.t("Выбрать steno.json…")) {
                    if chooseConfig() { Task { await loader.refresh() } }
                }
                .buttonStyle(Flat())
            }
            if let log = loader.serviceLog {
                Text(log).font(.system(size: 10, design: .monospaced))
                    .foregroundStyle(.secondary).lineLimit(5)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(7)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 6)
                        .fill(Color.primary.opacity(0.06)))
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
    }
}
