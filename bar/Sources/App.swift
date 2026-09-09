import SwiftUI

// StenoBar — steno в строке меню macOS.
//
// Отдельная сборка и отдельный язык: сервис — это один Go-бинарник, который
// ставится через brew и работает фоном, а строка меню нужна на конкретном маке
// конкретного человека.
//
// Показанное приложение читает прямо из базы steno, рядом с настройкой, и
// потому отвечает даже тогда, когда сервис лежит. К сервису оно ходит ровно за
// одним — увести бота на созвон.

@main
struct StenoBarApp: App {
    @StateObject private var loader = Loader()

    var body: some Scene {
        MenuBarExtra {
            PanelView(loader: loader)
        } label: {
            BarLabel(state: loader.bar)
                .task { loader.start() }
        }
        .menuBarExtraStyle(.window)
    }
}
