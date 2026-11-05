import { useLayoutEffect, type RefObject } from "react";

/** A text box that grows with what is typed, up to `lines` lines, then
 * scrolls (WhatsApp's): no drag handle. */
export function useAutoGrow(
  ref: RefObject<HTMLTextAreaElement | null>,
  value: string,
  lines = 6,
) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    const cs = getComputedStyle(el);
    const lh =
      parseFloat(cs.lineHeight) || 1.4 * (parseFloat(cs.fontSize) || 16);
    const pad =
      parseFloat(cs.paddingTop) +
      parseFloat(cs.paddingBottom) +
      parseFloat(cs.borderTopWidth) +
      parseFloat(cs.borderBottomWidth);
    const max = lh * lines + (Number.isFinite(pad) ? pad : 0);
    el.style.height = `${Math.min(el.scrollHeight, max)}px`;
    el.style.overflowY = el.scrollHeight > max ? "auto" : "hidden";
  }, [ref, value, lines]);
}
