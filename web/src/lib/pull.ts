// Pull to refresh: how far the ring follows the finger, and when letting
// go refreshes. Only an installed app on a phone uses it (a browser tab
// has its own).

/** Past this many pixels of pull, letting go refreshes. */
export const PULL_AT = 64;

/** How far the ring moves for a finger `dy` pixels down: less far, and
 * slowing towards a stop at 96 (about 105 px of pull refreshes). */
export function pullShown(dy: number): number {
  if (dy <= 0) return 0;
  const max = 96;
  return Math.round(max * (1 - Math.exp(-dy / max)));
}

/** Whether a pull of `dy` pixels refreshes when the finger lifts. */
export function pullRefreshes(dy: number): boolean {
  return pullShown(dy) >= PULL_AT;
}

/** The bits of an element `insideScroller` reads (a real Element fits). */
export interface ScrollNode {
  parentElement: ScrollNode | null;
  scrollHeight: number;
  clientHeight: number;
  tagName?: string;
}

/** Whether a touch that starts on `el` is inside something that scrolls up
 * and down by itself (a message list, a long sheet). Dragging down there is
 * reading what came before, not asking the page to load again. */
export function insideScroller(
  el: ScrollNode | null,
  overflowOf: (n: ScrollNode) => string = (n) =>
    getComputedStyle(n as unknown as Element).overflowY,
): boolean {
  for (let n = el; n; n = n.parentElement) {
    if (n.tagName === "BODY" || n.tagName === "HTML") return false;
    const o = overflowOf(n);
    if ((o === "auto" || o === "scroll") && n.scrollHeight > n.clientHeight)
      return true;
  }
  return false;
}
