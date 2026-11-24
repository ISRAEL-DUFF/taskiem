// Long messages are folded, as WhatsApp's are: the first lines, then
// "Read more", which shows the next stretch, and so on to the end. What is
// shown is cut where a word ends, never in the middle of a word or an emoji,
// and a message that would hide only a few words is not folded at all.

/** What the first view holds, and what each "Read more" adds. */
export const FIRST_CHARS = 600;
export const FIRST_LINES = 10;
export const STEP_CHARS = 1200;
export const STEP_LINES = 20;
/** Not worth a fold: what is left is shorter than this. */
const TAIL = 150;

export function reveal(
  text: string,
  taps: number,
): { shown: string; folded: boolean } {
  const chars = FIRST_CHARS + taps * STEP_CHARS;
  const lines = FIRST_LINES + taps * STEP_LINES;
  let cut = text.length;
  if (text.length > chars) cut = chars;
  let n = 0;
  for (let i = 0; i < Math.min(cut, text.length); i++) {
    if (text.charCodeAt(i) === 10 && ++n >= lines) {
      cut = i;
      break;
    }
  }
  if (cut >= text.length || text.length - cut < TAIL)
    return { shown: text, folded: false };
  // Back to where a word ends (within sixty characters), and not in the
  // middle of a surrogate pair.
  let end = cut;
  const floor = Math.max(0, cut - 60);
  while (end > floor && !/\s/.test(text.charAt(end))) end--;
  if (end <= floor) end = cut;
  const hi = text.charCodeAt(end - 1);
  if (hi >= 0xd800 && hi <= 0xdbff) end--;
  return { shown: text.slice(0, end).trimEnd(), folded: true };
}
