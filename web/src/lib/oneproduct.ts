// The One product kind of front (docs/spec/forms-that-sell.md §5.4): one
// product on a page of its own, with a promise line at the top, up to six
// feature chips each with an icon from a fixed set, and "Only 14 left"
// from the product's stock where the owner turns it on. The server checks
// the settings again (CheckFrontSettings); here is what the owner types.
import type { OneProductSettings } from "./fronts";

export const ONE_PRODUCT = "one-product";
export const MAX_PROMISE = 80;
export const MAX_CHIPS = 6;
export const MAX_CHIP_TEXT = 40;

/** The chips' icons, in the order offered, each with its words. */
export const CHIP_ICONS: { name: string; words: string }[] = [
  { name: "truck", words: "Delivery" },
  { name: "cash", words: "Money" },
  { name: "shield", words: "Guarantee" },
  { name: "star", words: "Star" },
  { name: "clock", words: "Time" },
  { name: "check", words: "Tick" },
  { name: "gift", words: "Gift" },
  { name: "leaf", words: "Natural" },
  { name: "phone", words: "Phone" },
  { name: "sparkle", words: "Sparkle" },
  { name: "heart", words: "Heart" },
  { name: "return", words: "Returns" },
];

/** A chip as typed. */
export interface ChipRow {
  icon: string;
  text: string;
}

/** The settings as the editor starts: the front's, or none. */
export function settingsRows(s: OneProductSettings | null | undefined): {
  promise: string;
  stock: boolean;
  chips: ChipRow[];
} {
  return {
    promise: s?.promise ?? "",
    stock: s?.stock_line ?? false,
    chips: (s?.chips ?? []).map((c) => ({ icon: c.icon, text: c.text })),
  };
}

const hasLink = (s: string) =>
  /(https?:\/\/|www\.|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})/i.test(s);

/** The settings typed, read; or what is wrong, in plain words. */
export function readSettings(rows: {
  promise: string;
  stock: boolean;
  chips: ChipRow[];
}): { settings: OneProductSettings; problem: string } {
  const promise = rows.promise.trim();
  const settings: OneProductSettings = { stock_line: rows.stock };
  if ([...promise].length > MAX_PROMISE || hasLink(promise))
    return {
      settings,
      problem: `The promise line is up to ${MAX_PROMISE} characters, with no links.`,
    };
  if (promise) settings.promise = promise;
  if (rows.chips.length > MAX_CHIPS)
    return { settings, problem: `Up to ${MAX_CHIPS} chips.` };
  const chips: { icon: string; text: string }[] = [];
  for (const [i, c] of rows.chips.entries()) {
    const text = c.text.trim();
    if (!CHIP_ICONS.some((x) => x.name === c.icon))
      return { settings, problem: `Choose an icon for chip ${i + 1}.` };
    if (!text || [...text].length > MAX_CHIP_TEXT || hasLink(text))
      return {
        settings,
        problem: `Chip ${i + 1} needs 1 to ${MAX_CHIP_TEXT} characters, with no links.`,
      };
    chips.push({ icon: c.icon, text });
  }
  if (chips.length) settings.chips = chips;
  return { settings, problem: "" };
}

/** What a One product front sells, in a line: "Smart Ear Cleaner, in 3
 * packages". */
export function sellsLine(
  s: { name: string; packages: unknown[] } | undefined,
): string {
  if (!s) return "Its order form sells nothing now: add one below.";
  const n = s.packages.length;
  return `${s.name || "A product"}, in ${n} ${n === 1 ? "package" : "packages"}`;
}
