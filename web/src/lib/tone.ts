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
