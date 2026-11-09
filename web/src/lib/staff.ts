// Staff who work from chat (docs/spec/chat-tools.md §7): the sale a
// cashier is making, and the shift's handover.
import type { StockItem } from "./stock";

export type PaidHow = "cash" | "transfer" | "pos";

/** The sale so far: its lines (what has a count), total and words. */
export function saleOf(items: StockItem[], qty: Record<string, number>) {
  const lines = items.filter((it) => (qty[it.id] ?? 0) > 0).map((item) => ({ item, quantity: qty[item.id] ?? 0 }));
  const total = lines.reduce((n, l) => n + l.quantity * (l.item.price_kobo ?? 0), 0);
  const things = lines.reduce((n, l) => n + l.quantity, 0);
  return { lines, total, count: things === 0 ? "Nothing yet" : things === 1 ? "1 thing" : `${things} things` };
}

/** A day's sales, a chart's bar. */
export interface DaySales {
  day: string;
  label: string;
  kobo: number;
}

/** The chart card's numbers (kept as a Taskiem message of kind chart). */
export interface SalesChart {
  title: string;
  total_kobo: number;
  change_pct: number | null;
  than: string;
  bars: DaySales[];
  highlight: number;
}

/** The morning briefing (GET /v1/chat-briefing). */
export interface Briefing {
  ready: boolean;
  day: string;
  key: string;
  put_off: boolean;
  /** The part of the day it is in Lagos, for the greeting. */
  part?: "morning" | "afternoon" | "evening";
  /** When its numbers were read, "14:05" Lagos time. */
  now?: string;
  sales?: { yesterday_kobo: number; change_pct: number | null; weekday: string; week: DaySales[] };
  owed_kobo?: number;
  low?: number;
  deliveries?: number;
  bookings?: number;
  /** Who is clocked in, and who is late (phase 3). */
  staff?: { in: { user_id: string; name?: string; in_at: string }[]; late: { user_id: string; name: string; due: string }[] };
}

/** Asks the briefing card to come back (the tools menu's Briefing, or
 * "briefing" typed in the Taskiem chat). */
export const SHOW_BRIEFING = "taskiem:show-briefing";
export const showBriefing = () => window.dispatchEvent(new Event(SHOW_BRIEFING));

/** Typed words that ask for the briefing rather than tell Taskiem. */
export const asksBriefing = (words: string) =>
  /^(show (me )?)?(the |my |today'?s )?(morning )?briefing[.!?]?$/i.test(words.trim());

/** The shift so far (GET /v1/chat-handover). */
export interface Handover {
  day: string;
  sales?: { total_kobo: number; count: number; by_how: Record<string, number>; waiting: number };
  orders_open?: number;
  deliveries?: { out: number; done: number };
  low: string[];
}

/** "Up 12% on last Wednesday", "Down 3% on the week before", "The same as …". */
export function changeWords(pct: number | null | undefined, than: string): string | undefined {
  if (pct === null || pct === undefined) return undefined;
  if (pct > 0) return `Up ${pct}% on ${than}`;
  if (pct < 0) return `Down ${-pct}% on ${than}`;
  return `The same as ${than}`;
}

/** Each bar's height, 0 to 1 of the tallest (a quiet day still shows). */
export function barHeights(bars: DaySales[]): number[] {
  const top = Math.max(0, ...bars.map((b) => b.kobo));
  return bars.map((b) => (top === 0 ? 0.04 : Math.max(0.04, b.kobo / top)));
}

/** The greeting's name: the first name, else the email's start. */
export function firstName(name: string | undefined, email: string | undefined): string {
  const n = (name ?? "").trim().split(/\s+/)[0];
  return n || (email ?? "").split("@")[0] || "";
}

/** The cash counted against the cash sold, in words. */
export function cashWords(counted: number, sold: number, naira: (k: number) => string): string {
  if (counted === sold) return "As sold";
  return counted < sold ? `${naira(sold - counted)} short` : `${naira(counted - sold)} over`;
}
