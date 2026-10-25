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
