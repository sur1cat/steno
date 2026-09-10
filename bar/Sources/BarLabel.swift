import AppKit
import SwiftUI

// То, что видно в строке меню всегда.
//
// Метка MenuBarExtra не рисует SwiftUI-вью так, как это делает панель: из
// контейнера доходит только первый ребёнок, цвет с шаблонной картинки
// снимается, а SF-символ внутри Text не приезжает вовсе. На этом уже потеряли
// время в соседнем проекте (pitwall/bar/Sources/Variants.swift), и обходить
// каждое ограничение по отдельности бесполезно.
//
// Обход один на все: не отдавать строке вью, а отдать картинку. Рисуем что
// угодно через ImageRenderer, помечаем результат «не шаблон» — и цвета
// доезжают, а вместе с ними и составная метка из точки со секундомером.

struct BarLabel: View {
    let state: BarState

    /// Высота полезной части пункта строки меню.
    private static let barHeight: CGFloat = 16

    var body: some View {
        if let image = rendered {
            Image(nsImage: image)
        } else {
            // Если растеризация когда-нибудь не выйдет, читаемая запаска лучше
            // пустого места: пустое место человек примет за «всё в порядке».
            Text(fallback).monospacedDigit()
        }
    }

    @MainActor private var rendered: NSImage? {
        // Строка меню живёт в системном оформлении, а не в оформлении
        // приложения, поэтому тему берём у него и подсовываем рисовальщику.
        let appearance = NSApplication.shared.effectiveAppearance
        let dark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        let renderer = ImageRenderer(
            content: content
                .frame(height: Self.barHeight)
                .environment(\.colorScheme, dark ? .dark : .light)
        )
        renderer.scale = NSScreen.main?.backingScaleFactor ?? 2
        guard let cg = renderer.cgImage else { return nil }
        let image = NSImage(cgImage: cg,
                            size: NSSize(width: CGFloat(cg.width) / renderer.scale,
                                         height: CGFloat(cg.height) / renderer.scale))
        // Шаблонную картинку система перекрашивает сама — этой красный цвет
        // нужен свой, иначе идущая запись перестанет отличаться от покоя.
        image.isTemplate = false
        return image
    }

    private var font: Font { .system(size: 11, weight: .medium).monospacedDigit() }

    @ViewBuilder private var content: some View {
        switch state.kind {
        case .starting:
            Image(systemName: "waveform")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(.tertiary)
        case .idle:
            Image(systemName: "waveform")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(.primary)
        case .recording(let elapsed, let count, let note):
            // Красная точка и бегущие секунды — то, ради чего такие приложения
            // и держат: видно с одного взгляда и не спутаешь с покоем.
            //
            // У заметки на месте точки микрофон: человек, который только что
            // нажал «наговорить», должен видеть, что пишут именно его, а не
            // чужой созвон, — и что можно говорить.
            HStack(spacing: 4) {
                if note {
                    Image(systemName: "mic.fill")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(Color.red)
                } else {
                    Circle().fill(Color.red).frame(width: 8, height: 8)
                }
                if count > 1 {
                    Text("×\(count)").font(font).foregroundStyle(.primary)
                }
                Text(Format.stopwatch(elapsed)).font(font).foregroundStyle(.primary)
            }
        case .serviceDown:
            // Сервис не работает: показанное — вчерашнее, а следующий созвон
            // никто не запишет. Значок в покое об этом молчал бы.
            HStack(spacing: 3) {
                Image(systemName: "waveform")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(.primary)
                Circle().fill(Color.orange).frame(width: 5, height: 5)
            }
        case .trouble:
            HStack(spacing: 3) {
                Image(systemName: "waveform")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(.tertiary)
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(.orange)
            }
        }
    }

    private var fallback: String {
        switch state.kind {
        case .recording(let elapsed, _, let note):
            return (note ? "🎙 " : "● ") + Format.stopwatch(elapsed)
        case .trouble: return "steno !"
        case .serviceDown: return "steno ·"
        default: return "steno"
        }
    }
}
