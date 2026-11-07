// The colour a name is drawn in where there is no picture (the designs: a
// business's round mark, a product's tile): one of the boards' pairs, the
// same for a name every time, so "Jollof rice" is always peach.

export const TONES = ["pine", "lime", "peach", "yellow", "green", "lavender", "blue", "sand"] as const;
export type Tone = (typeof TONES)[number];

/** The tone of a name: a small hash of its letters over the pairs. */
export function toneOf(name: string): Tone {
  let h = 0;
  for (const ch of name.trim().toLowerCase()) h = (h * 31 + (ch.codePointAt(0) ?? 0)) >>> 0;
  return TONES[h % TONES.length] ?? "pine";
}

/** A tile's letters: the first letters of the name's first two words
 * ("Jollof rice" JR); one word gives its first letter and the next
 * consonant ("Zobo" ZB, "Chapman" CH), as the boards draw them. */
export function tileLetters(name: string): string {
  const words = name.split(/\s+/).filter((w) => /^\p{L}/u.test(w));
  if (words.length === 0) return name.slice(0, 1).toUpperCase();
  const [a = "", b] = words;
  if (b) return (a.charAt(0) + b.charAt(0)).toUpperCase();
  const next = a.slice(1).match(/[b-df-hj-np-tv-z]/i)?.[0] ?? a.charAt(1);
  return (a.charAt(0) + next).toUpperCase();
}
