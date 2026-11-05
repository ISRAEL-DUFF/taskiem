import { useEffect, useRef, type RefObject } from "react";

/** True where a keyboard and a pointer that hovers are the usual hands: on a
 * phone, putting the cursor back in the box would raise the keyboard over a
 * screen the person only meant to close. */
export const hasHover = () =>
  typeof matchMedia === "function" && matchMedia("(hover: hover)").matches;

/**
 * Puts the cursor back in the message box. `closed` are things that, when
 * they go away (a panel, a sheet, a waiting photo, a reply), hand the focus
 * back, on a screen with a keyboard; `opened` are things that, when they
 * come (a reply to write, a photo to caption), ask for it everywhere.
 */
export function useComposerFocus(
  box: RefObject<HTMLElement | null>,
  closed: boolean[],
  opened: boolean[] = [],
) {
  const was = useRef({ closed, opened });
  useEffect(() => {
    const before = was.current;
    was.current = { closed, opened };
    const gone = before.closed.some((b, i) => b && !closed[i]);
    const came = opened.some((o, i) => o && !before.opened[i]);
    if (gone && !hasHover()) return;
    if (gone || came) requestAnimationFrame(() => box.current?.focus());
  });
}
