// What the composer does with emoji, apart from the list of them (the list
// is lib/emojiList.ts, loaded with the panel): put one in the text, keep the
// recent ones, and give a hand a skin tone. Unicode's own characters: the
// app asks for no image set and no outside host (a device draws them as it
// draws any text, so they look a little different on each).

export interface EmojiItem {
  char: string;
  name: string;
  /** More words that find it: "happy" finds 😀. */
  words: string;
}

export interface EmojiGroup {
  id: string;
  label: string;
  icon: string;
  items: EmojiItem[];
}

/** The text with `insert` in place of the selection, and where the caret goes. */
export function insertAt(
  text: string,
  start: number,
  end: number,
  insert: string,
  max = 4000,
): { text: string; caret: number } {
  const a = Math.max(0, Math.min(start, text.length));
  const b = Math.max(a, Math.min(end, text.length));
  const next = text.slice(0, a) + insert + text.slice(b);
  if (next.length > max) return { text, caret: a };
  return { text: next, caret: a + insert.length };
}

/** The recent emoji with `char` first, no repeats, at most `max`. */
export function addRecent(list: string[], char: string, max = 24): string[] {
  return [char, ...list.filter((c) => c !== char)].slice(0, max);
}

const FE0F = "\uFE0F";
const MODIFIER = /[\u{1F3FB}-\u{1F3FF}]/gu;

/** The emoji without any skin tone modifier. */
export function stripTone(char: string): string {
  return char.replace(MODIFIER, "");
}

/**
 * The emoji in skin tone `tone` (1 to 5): the modifier goes after the first
 * character, the rest as it is (a selector right after the first goes), as
 * Unicode writes them (a person with a tone in a longer sequence too). Only
 * for an emoji that takes one: see withTone in lib/emojiList.ts.
 */
export function insertTone(char: string, tone: number): string {
  const cps = [...stripTone(char)];
  const first = cps.shift() ?? "";
  if (cps[0] === FE0F) cps.shift();
  return first + String.fromCodePoint(0x1f3fa + tone) + cps.join("");
}
