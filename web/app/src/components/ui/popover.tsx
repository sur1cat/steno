"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useEscapeHandler } from "./escape-stack";
import { ZOOM_EVENT } from "@/lib/use-zoom";

interface PopoverProps {
  open: boolean;
  onClose: () => void;
  children: React.ReactNode;
  align?: "left" | "right";
  className?: string;
  /** Set the panel's min-width to the trigger's width so a combobox dropdown
   *  lines up with its input instead of shrinking to its content. */
  matchWidth?: boolean;
}

export function Popover({ open, onClose, children, align = "left", className = "", matchWidth = false }: PopoverProps) {
  // The menu is rendered through a portal and positioned `fixed` against the
  // trigger's wrapper. An in-flow absolute child is clipped by any
  // overflow:auto/hidden ancestor — the /vehicles table wraps its rows in an
  // overflow-x-auto box, so a short table (e.g. a single car) cut the status
  // menu off and made it unreachable. The portal escapes that clip entirely.
  // `anchorRef` is a zero-size marker left in normal flow; its parentElement is
  // the trigger's positioning wrapper, which we measure to place the menu.
  const anchorRef = useRef<HTMLSpanElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const [style, setStyle] = useState<React.CSSProperties | null>(null);
  // Во что браузер превращает наши top/left: куда попадает локальный ноль и
  // сколько визуальных пикселей выходит из одного локального. См. calibrate().
  const frame = useRef<{ ox: number; oy: number; sx: number; sy: number } | null>(null);

  // Escape through the shared LIFO stack so a popover opened over a dialog
  // takes the Escape without also closing the dialog underneath.
  useEscapeHandler(open, onClose);

  useEffect(() => {
    if (!open) return;
    // pointerdown, not mousedown: iOS Safari does not synthesise a mouse event
    // for a tap on a non-interactive element, so a phone tap outside the menu
    // never dismissed it. pointerdown fires for mouse, touch and pen at the same
    // point in the sequence as mousedown, so desktop toggling is unchanged.
    const handlePointer = (e: PointerEvent) => {
      const target = e.target as Node;
      const panel = popoverRef.current;
      if (!panel || panel.contains(target)) return;
      // A tap on the trigger must NOT dismiss here: the consumer's own toggle
      // owns the trigger, and dismissing first would let that toggle re-open the
      // menu, leaving it stuck. The trigger lives in the anchor's parent (the
      // positioning wrapper we measure), so treat that subtree as "inside" too.
      const trigger = anchorRef.current?.parentElement;
      if (trigger && trigger.contains(target)) return;
      onClose();
    };
    document.addEventListener("pointerdown", handlePointer);
    return () => document.removeEventListener("pointerdown", handlePointer);
  }, [open, onClose]);

  // Собственная система координат панели измеряется, а не выводится из стилей.
  //
  // Раньше поправка бралась из `parseFloat(document.documentElement.style.zoom)`
  // — то есть предполагалось, что масштаб приложения всегда стоит ИНЛАЙНОВЫМ
  // стилем на <html>. Пока это так, арифметика верна (проверено в chromium:
  // снос 0px на 100/125/150%). Но чтение инлайнового стиля не видит ни правила
  // из таблицы стилей, ни зума на другом предке, и тогда поправка молча
  // становится единицей: замер показал снос 282px вправо и 59px вниз при 1.25 —
  // ровно то «и по горизонтали, и по вертикали», с которым в 30169538 сняли
  // портал с выпадашек формы, так и не сумев доказать причину.
  //
  // Здесь причина и не нужна. Ставим панель в две известные локальные точки,
  // смотрим, где она оказалась, и получаем перевод из визуальных координат в
  // локальные — какой бы zoom, transform или контейнер его ни задавали.
  // Считается один раз на открытие: при прокрутке масштаб не меняется, а два
  // лишних чтения на каждый кадр стоили бы двух принудительных перерасчётов.
  // useCallback с пустыми зависимостями: функция читает только ref'ы, поэтому
  // ссылка стабильна и её можно честно держать в зависимостях эффекта.
  const calibrate = useCallback(() => {
    const pop = popoverRef.current;
    if (!pop) return null;
    const prevTop = pop.style.top;
    const prevLeft = pop.style.left;
    pop.style.position = "fixed";
    pop.style.top = "0px";
    pop.style.left = "0px";
    const zero = pop.getBoundingClientRect();
    pop.style.top = "100px";
    pop.style.left = "100px";
    const hundred = pop.getBoundingClientRect();
    pop.style.top = prevTop;
    pop.style.left = prevLeft;
    const sx = (hundred.left - zero.left) / 100;
    const sy = (hundred.top - zero.top) / 100;
    // Ноль означает, что панель не двигается от top/left (её выключили из
    // потока позиционирования). Тогда единица — прежнее поведение.
    return { ox: zero.left, oy: zero.top, sx: sx || 1, sy: sy || 1 };
  }, []);

  // Position before paint, and keep the menu glued to its trigger while the
  // page scrolls or resizes. Flip above when it would overflow the viewport
  // bottom; clamp horizontally so it never spills off either edge.
  useLayoutEffect(() => {
    if (!open) {
      setStyle(null);
      frame.current = null;
      return;
    }
    const place = () => {
      const anchor = anchorRef.current?.parentElement;
      const pop = popoverRef.current;
      if (!anchor || !pop) return;
      const f = (frame.current ??= calibrate());
      if (!f) return;
      const a = anchor.getBoundingClientRect();
      const p = pop.getBoundingClientRect();
      // Отступы задуманы в CSS-пикселях, а сравниваются с визуальными rect'ами,
      // поэтому масштабируются тем же множителем.
      const gap = 4 * f.sy;
      const margin = 8 * f.sx;
      const flipUp =
        a.bottom + gap + p.height > window.innerHeight && a.top - gap - p.height > margin;
      const rawLeft = align === "right" ? a.right - p.width : a.left;
      const left = Math.max(margin, Math.min(rawLeft, window.innerWidth - p.width - margin));
      // Целевая точка известна в визуальных координатах; переводим её в
      // локальные по измеренной системе координат панели.
      const visualTop = flipUp ? a.top - gap - p.height : a.bottom + gap;
      const top = (visualTop - f.oy) / f.sy;
      const nextLeft = (left - f.ox) / f.sx;
      const nextMinW = matchWidth ? a.width / f.sx : undefined;
      // Skip the state update (and the portal re-render it triggers) when the
      // position is unchanged — place() runs on every scroll frame.
      setStyle((prev) =>
        prev && prev.top === top && prev.left === nextLeft && prev.minWidth === nextMinW
          ? prev
          : { position: "fixed", top, left: nextLeft, ...(nextMinW != null ? { minWidth: nextMinW } : {}) },
      );
    };
    place();
    // Пересчёт системы координат — только там, где она реально может смениться:
    // размер окна и смена масштаба приложения (CSS zoom не поднимает resize,
    // поэтому слушаем собственное событие useZoom).
    const recalibrate = () => {
      frame.current = calibrate();
      place();
    };
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", recalibrate);
    window.addEventListener(ZOOM_EVENT, recalibrate);
    // Re-place when the panel's OWN content resizes within one open session — a
    // consumer that swaps content (e.g. a combobox's list → taller inline create
    // form) or fills async options would otherwise keep the flip/clamp computed
    // against the height at open, and spill off-screen near the viewport edge.
    const ro = new ResizeObserver(place);
    const pop = popoverRef.current;
    if (pop) ro.observe(pop);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", recalibrate);
      window.removeEventListener(ZOOM_EVENT, recalibrate);
      ro.disconnect();
    };
  }, [open, align, matchWidth, calibrate]);

  if (!open) return null;

  return (
    <>
      <span ref={anchorRef} className="hidden" aria-hidden />
      {createPortal(
        <div
          ref={popoverRef}
          // Hidden until measured so it never flashes at the top-left corner.
          style={style ?? { position: "fixed", top: 0, left: 0, visibility: "hidden" }}
          // z-[1100] — ВЫШЕ модалки (Dialog держит z-[1000]).
          //
          // Оба портала висят прямо в body и потому лежат в одном контексте
          // наложения: с прежним z-50 панель открывалась ПОД модалкой. Снаружи
          // это выглядело как «выпадашка не работает» — она честно
          // открывалась, просто её не было видно, и выбрать другого менеджера
          // в модалке эффективности было невозможно.
          //
          // Попап всегда порождён чем-то и по смыслу должен лежать над этим
          // чем-то, поэтому значение общее, а не «для случая внутри диалога».
          // Устойчивый маркер для тестов: искать панель по стилевому классу
          // значило привязать проверку положения к слою наложения, и первая же
          // правка z-index её роняла.
          data-popover-panel=""
          className={`z-[1100] rounded-lg border border-[var(--border)] bg-[var(--popover)] py-1 shadow-lg ${className}`}
        >
          {children}
        </div>,
        document.body,
      )}
    </>
  );
}
