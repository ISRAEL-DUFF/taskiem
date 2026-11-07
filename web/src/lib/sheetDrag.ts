// A sheet on a phone follows the finger down from its grab handle: thrown
// down (fast) or pulled past a third of its height, it closes; let go early
// and it springs back (docs/spec/chat-tools.md §2, the Motion artboard).

import { useCallback, useRef, type PointerEvent, type RefObject } from "react";

/** Whether a drag that went `dy` px in `ms` closes a sheet `h` px tall. */
export function throwsClosed(dy: number, ms: number, h: number): boolean {
  if (dy <= 0) return false;
  const speed = dy / Math.max(ms, 1); // px per ms
  return speed > 0.6 || dy > h / 3;
}

export function useSheetDrag(sheet: RefObject<HTMLElement | null>, onClose: () => void) {
  const start = useRef<{ y: number; t: number } | null>(null);
  const move = useCallback(
    (e: PointerEvent) => {
      const el = sheet.current;
      if (!start.current || !el) return;
      const dy = Math.max(0, e.clientY - start.current.y);
      el.style.transition = "none";
      el.style.transform = `translateY(${dy}px)`;
    },
    [sheet],
  );
  const end = useCallback(
    (e: PointerEvent) => {
      const el = sheet.current;
      const s = start.current;
      start.current = null;
      if (!el || !s) return;
      const dy = e.clientY - s.y;
      el.style.transition = "transform var(--t-picker) var(--spring-settle)";
      if (throwsClosed(dy, performance.now() - s.t, el.offsetHeight)) {
        el.style.transform = "translateY(100%)";
        window.setTimeout(onClose, 200);
      } else el.style.transform = "";
    },
    [sheet, onClose],
  );
  return {
    onPointerDown: (e: PointerEvent) => {
      start.current = { y: e.clientY, t: performance.now() };
      (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    },
    onPointerMove: move,
    onPointerUp: end,
    onPointerCancel: end,
  };
}
