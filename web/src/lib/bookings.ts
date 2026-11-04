// Storefront bookings (docs/spec/storefronts.md 6.9; Phase 5, S4): the
// services table's column mapping, the availability editor's form, and how
// a booking's day and time read, always in Lagos time.

import type { Order } from "./orders";
import type { ColumnType, Table } from "./tables";
import { itemRow, DAYS, MAX_OPENINGS, columnsFor, formatPrice, type Columns, type Hours, type Item, type Span } from "./storefront";

// --- the services table ---

/** A services mapping: the catalogue's, plus how long a service takes, how
 * many at once, and the time kept free after it. Services have no stock. */
export interface ServiceColumns extends Omit<Columns, "stock"> {
  duration?: string;
  capacity?: string;
  buffer?: string;
}

export type ServiceKey = Exclude<keyof ServiceColumns, "price_unit">;

/** The services' fields, the column types each takes, and what it is for. */
export const SERVICE_MAPPING: { key: ServiceKey; label: string; types: ColumnType[]; required?: boolean; help: string }[] = [
  { key: "name", label: "Name", types: ["text"], required: true, help: "What the service is called" },
  { key: "duration", label: "Duration", types: ["integer", "number"], required: true, help: "How long it takes, in minutes (5 to 480)" },
  { key: "price", label: "Price", types: ["integer", "number"], help: "Shown in naira; empty means priced on the day" },
  { key: "description", label: "Description", types: ["text"], help: "A line about the service" },
  { key: "category", label: "Category", types: ["text"], help: "Groups services on the page" },
  { key: "photo", label: "Photo", types: ["text"], help: "Holds a photo of the service" },
  { key: "available", label: "Available", types: ["boolean"], help: "No hides the service" },
  { key: "capacity", label: "Capacity", types: ["integer", "number"], help: "How many customers at once; empty means one" },
  { key: "buffer", label: "Buffer", types: ["integer", "number"], help: "Minutes kept free after each booking" },
  { key: "sort", label: "Sort", types: ["integer", "number"], help: "Order on the page; otherwise by name" },
];

/** The tables services can come from: a text column for the name and a number column for the duration, neither personal. */
export const servicesTables = (tables: Table[]) =>
  tables.filter((t) => columnsFor(t.columns, ["text"]).length > 0 && columnsFor(t.columns, ["integer", "number"]).length > 0);

const SERVICE_GUESSES: Record<ServiceKey, string[]> = {
  name: ["name", "service", "title", "item"],
  duration: ["duration", "duration_minutes", "minutes", "length", "mins"],
  price: ["price", "price_naira", "price_kobo", "amount", "amount_kobo", "cost"],
  description: ["description", "details", "about"],
  category: ["category", "group", "section", "type"],
  photo: ["photo", "image", "picture"],
  available: ["available", "active", "bookable"],
  capacity: ["capacity", "seats", "spaces", "places"],
  buffer: ["buffer", "buffer_minutes", "gap"],
  sort: ["sort", "position", "order", "rank"],
};

/** A first mapping for a newly picked services table, by column names that
 * say what they hold. Only the name falls back to the first text column: a
 * duration is never guessed from a column that does not say so. */
export function guessServiceColumns(table: Pick<Table, "columns">): ServiceColumns {
  const out: ServiceColumns = { price_unit: "naira" };
  const used = new Set<string>();
  for (const m of SERVICE_MAPPING) {
    const allowed = columnsFor(table.columns, m.types).filter((c) => !used.has(c.name));
    const hit = SERVICE_GUESSES[m.key].map((g) => allowed.find((c) => c.name === g)).find(Boolean) ?? (m.key === "name" ? allowed[0] : undefined);
    if (hit) {
      out[m.key] = hit.name;
      used.add(hit.name);
    }
  }
  if (out.price?.endsWith("kobo")) out.price_unit = "kobo";
  return out;
}

/** The mapping without the fields left empty, as the API stores it. */
export function cleanServiceColumns(c: ServiceColumns): ServiceColumns {
  const out: ServiceColumns = {};
  for (const m of SERVICE_MAPPING) if (c[m.key]) out[m.key] = c[m.key];
  if (out.price) out.price_unit = c.price_unit || "naira";
  return out;
}

/** What is missing from a services mapping before it can be saved, or null. */
export function servicesProblem(table: string | null, c: ServiceColumns): string | null {
  if (!table) return null;
  if (!c.name) return "Choose the column with each service's name.";
  if (!c.duration) return "Choose the column with how long each service takes, in minutes.";
  return null;
}

// --- availability ---

/** When the business takes bookings (engine/storefront Availability). 0 means the default. */
export interface Availability {
  /** Monday first; every day empty means the card's opening hours. */
  days: (Span[] | null)[];
  off?: string[];
  ahead_days?: number;
  soonest_minutes?: number;
  cutoff_minutes?: number;
  auto_confirm?: boolean;
}

/** The server's defaults and bounds (engine/storefront slots.go). */
export const AVAILABILITY_DEFAULTS = { ahead_days: 30, soonest_minutes: 120, cutoff_minutes: 120 } as const;
export const MAX_AHEAD_DAYS = 180;
export const MAX_DAYS_OFF = 400;
/** A week, in minutes: the most how soon and the change cut-off may be. */
export const MAX_NOTICE_MINUTES = 7 * 24 * 60;

export type TimeUnit = "minutes" | "hours";

/** A length of time as the editor holds it: blank for the default. */
export interface Notice {
  value: string;
  unit: TimeUnit;
}

export interface BookingDay {
  open: boolean;
  spans: Span[];
}

export interface AvailabilityForm {
  /** Every day empty: bookings follow the card's opening hours. */
  sameAsHours: boolean;
  days: BookingDay[];
  off: string[];
  ahead: string;
  soonest: Notice;
  cutoff: Notice;
  autoConfirm: boolean;
}

const HHMM = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;
const DATE = /^\d{4}-\d{2}-\d{2}$/;

const toMinutes = (hhmm: string) => Number(hhmm.slice(0, 2)) * 60 + Number(hhmm.slice(3, 5));

/** A minutes setting as the editor shows it: whole hours in hours, else minutes; 0 (the default) blank. */
export function noticeToForm(minutes: number | undefined): Notice {
  if (!minutes) return { value: "", unit: "hours" };
  return minutes % 60 === 0 ? { value: String(minutes / 60), unit: "hours" } : { value: String(minutes), unit: "minutes" };
}

/** A notice in minutes: 0 when blank (the default), NaN when it is not a number. */
export function noticeMinutes(n: Notice): number {
  const v = n.value.trim();
  if (v === "") return 0;
  if (!/^\d+(\.\d+)?$/.test(v)) return NaN;
  return Math.round(Number(v) * (n.unit === "hours" ? 60 : 1));
}

/** The card's hours as booking hours: spans past midnight are left out, as the server does. */
function hoursDays(h: Hours | undefined | null): BookingDay[] {
  return Array.from({ length: 7 }, (_, i) => {
    const spans = (h?.days?.[i] ?? []).filter((s) => HHMM.test(s.open) && HHMM.test(s.close) && toMinutes(s.close) > toMinutes(s.open));
    return spans.length > 0 ? { open: true, spans: spans.slice(0, MAX_OPENINGS).map((s) => ({ ...s })) } : { open: false, spans: [{ open: "09:00", close: "17:00" }] };
  });
}

/** The API's availability as the editor holds it. With no booking hours of
 * its own, the days start from the card's opening hours, to edit from. */
export function availabilityToForm(a: Partial<Availability> | undefined | null, card?: Hours | null): AvailabilityForm {
  const own = (a?.days ?? []).some((d) => d && d.length > 0);
  const days = own
    ? Array.from({ length: 7 }, (_, i): BookingDay => {
        const spans = a?.days?.[i];
        return spans && spans.length > 0 ? { open: true, spans: spans.map((s) => ({ open: s.open, close: s.close })) } : { open: false, spans: [{ open: "09:00", close: "17:00" }] };
      })
    : hoursDays(card);
  return {
    sameAsHours: !own,
    days,
    off: [...(a?.off ?? [])].sort(),
    ahead: a?.ahead_days ? String(a.ahead_days) : "",
    soonest: noticeToForm(a?.soonest_minutes),
    cutoff: noticeToForm(a?.cutoff_minutes),
    autoConfirm: !!a?.auto_confirm,
  };
}

/** The editor's availability as the API takes it: a closed day is empty, and "same as opening hours" empties every day. */
export function availabilityFromForm(f: AvailabilityForm): Availability {
  const days = Array.from({ length: 7 }, (_, i): Span[] => {
    const d = f.days[i];
    if (f.sameAsHours || !d?.open) return [];
    return d.spans.slice(0, MAX_OPENINGS).map((s) => ({ open: s.open, close: s.close }));
  });
  const out: Availability = { days };
  const off = [...new Set(f.off.filter(Boolean))].sort();
  if (off.length) out.off = off;
  const ahead = Number(f.ahead.trim());
  if (f.ahead.trim() && Number.isInteger(ahead) && ahead > 0) out.ahead_days = ahead;
  const soonest = noticeMinutes(f.soonest);
  if (soonest > 0) out.soonest_minutes = soonest;
  const cutoff = noticeMinutes(f.cutoff);
  if (cutoff > 0) out.cutoff_minutes = cutoff;
  if (f.autoConfirm) out.auto_confirm = true;
  return out;
}

/** The problems with the availability, mirroring the server's check (day -1: not a day's). */
export function availabilityErrors(f: AvailabilityForm): { day: number; message: string }[] {
  const out: { day: number; message: string }[] = [];
  if (!f.sameAsHours) {
    f.days.forEach((d, i) => {
      if (!d.open) return;
      if (d.spans.length > MAX_OPENINGS) out.push({ day: i, message: `At most ${MAX_OPENINGS} booking periods on ${DAYS[i]}.` });
      for (const s of d.spans) {
        if (!HHMM.test(s.open) || !HHMM.test(s.close)) {
          out.push({ day: i, message: `Give ${DAYS[i]}'s start and end times.` });
          break;
        }
        if (toMinutes(s.close) <= toMinutes(s.open)) {
          out.push({ day: i, message: `Booking hours on ${DAYS[i]} must end after they start, the same day.` });
          break;
        }
      }
    });
    if (!f.days.some((d) => d.open)) out.push({ day: -1, message: "Open at least one day for bookings, or use your opening hours." });
  }
  if (f.off.length > MAX_DAYS_OFF) out.push({ day: -1, message: `At most ${MAX_DAYS_OFF} days off.` });
  for (const d of f.off) {
    if (!DATE.test(d) || Number.isNaN(Date.parse(`${d}T00:00:00Z`))) {
      out.push({ day: -1, message: `Day off ${d || "(empty)"} is not a date.` });
      break;
    }
  }
  const ahead = f.ahead.trim();
  if (ahead && (!/^\d+$/.test(ahead) || Number(ahead) < 1 || Number(ahead) > MAX_AHEAD_DAYS)) {
    out.push({ day: -1, message: `How far ahead is 1 to ${MAX_AHEAD_DAYS} days.` });
  }
  for (const [n, name] of [
    [f.soonest, "How soon"],
    [f.cutoff, "The change cut-off"],
  ] as const) {
    const m = noticeMinutes(n);
    if (Number.isNaN(m) || m > MAX_NOTICE_MINUTES) out.push({ day: -1, message: `${name} is at most a week (${MAX_NOTICE_MINUTES / 60} hours).` });
    // The server reads 0 as its default (2 hours), so none is not offered.
    else if (n.value.trim() !== "" && m < 1) out.push({ day: -1, message: `${name} is at least 1 minute; leave it empty for 2 hours.` });
  }
  return out;
}

/** The days off from today on, and how many have passed (they stay saved until removed). */
export function upcomingOff(off: string[], today: string): { upcoming: string[]; past: number } {
  const upcoming = off.filter((d) => d >= today);
  return { upcoming, past: off.length - upcoming.length };
}

// --- services as the preview lists them ---

/** A duration in minutes: "45 min", "1 hour", "1 hour 30 min", "2 hours". */
export function formatDuration(minutes: number | undefined): string {
  if (!minutes || minutes <= 0) return "";
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  const hours = h === 0 ? "" : h === 1 ? "1 hour" : `${h} hours`;
  return [hours, m ? `${m} min` : ""].filter(Boolean).join(" ");
}

/** A service's price, or "Price on the day" when the table gives none. */
export const servicePrice = (it: Pick<Item, "price_kobo">) => (it.price_kobo === undefined || it.price_kobo === null ? "Price on the day" : formatPrice(it.price_kobo));

/** A service in the preview, after its name: "45 min · ₦5,000 · 10 at once". */
export function serviceLine(it: Service): string {
  return [formatDuration(it.duration_minutes) || "No duration", servicePrice(it), (it.capacity ?? 1) > 1 ? `${it.capacity} at once` : ""].filter(Boolean).join(" · ");
}

/** A service from GET /v1/storefront/services. */
export interface Service extends Item {
  duration_minutes?: number;
  capacity?: number;
  buffer_minutes?: number;
}

export interface Services {
  items: Service[];
  /** The availability in force, defaults filled. */
  availability: Availability;
  synced_at: string | null;
  error: string | null;
}

// --- Lagos time ---

export const LAGOS = "Africa/Lagos";
/** Lagos keeps West Africa Time all year: no summer time. */
export const LAGOS_OFFSET = "+01:00";

const dayKeyFmt = new Intl.DateTimeFormat("en-CA", { timeZone: LAGOS, year: "numeric", month: "2-digit", day: "2-digit" });
const timeFmt = new Intl.DateTimeFormat("en-GB", { timeZone: LAGOS, hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

/** A moment's date in Lagos: "2026-10-24". */
export const lagosDay = (t: string | number | Date) => dayKeyFmt.format(new Date(t));

/** A moment's time of day in Lagos: "10:30". */
export const lagosTime = (t: string | number | Date) => timeFmt.format(new Date(t));

/** A date ("2026-10-24") as the list heads it: "Sat 24 Oct". */
export function dayLabel(day: string): string {
  const d = new Date(`${day}T12:00:00Z`);
  if (Number.isNaN(d.getTime())) return day;
  const p = new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", weekday: "short", day: "numeric", month: "short" }).formatToParts(d);
  const get = (t: string) => p.find((x) => x.type === t)?.value ?? "";
  return `${get("weekday")} ${get("day")} ${get("month")}`;
}

/** A booking's time as customers read it: "Sat 24 Oct, 10:30". */
export const whenLabel = (iso: string) => `${dayLabel(lagosDay(iso))}, ${lagosTime(iso)}`;

/** A date plus n days: "2026-10-24" + 7. */
export function addDays(day: string, n: number): string {
  const d = new Date(`${day}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

/** A Lagos date and time ("2026-10-24", "10:30") as RFC 3339, for a move. */
export const lagosInstant = (day: string, time: string) => `${day}T${time}:00${LAGOS_OFFSET}`;

// --- the bookings list ---

export type BookingRange = "week" | "month";

export const BOOKING_RANGES: { value: BookingRange; label: string; days: number }[] = [
  { value: "week", label: "This week", days: 7 },
  { value: "month", label: "Next 30 days", days: 31 },
];

/** The GET /v1/storefront/bookings call for a range from today (Lagos): to is exclusive. */
export function bookingsPath(range: string, now: number = Date.now()): string {
  const days = BOOKING_RANGES.find((x) => x.value === range)?.days ?? 31;
  const from = lagosDay(now);
  return `/v1/storefront/bookings?${new URLSearchParams({ from, to: addDays(from, days) })}`;
}

/** Bookings by Lagos day, soonest first, each day's soonest first. */
export function groupByDay(list: Order[]): { day: string; label: string; bookings: Order[] }[] {
  const timed = list.flatMap((b) => (b.starts_at ? [{ b, at: b.starts_at }] : []));
  timed.sort((x, y) => Date.parse(x.at) - Date.parse(y.at));
  const out: { day: string; label: string; bookings: Order[] }[] = [];
  for (const { b, at } of timed) {
    const day = lagosDay(at);
    let g = out[out.length - 1];
    if (!g || g.day !== day) {
      g = { day, label: dayLabel(day), bookings: [] };
      out.push(g);
    }
    g.bookings.push(b);
  }
  return out;
}

/** A booking's time span: "10:30–11:15". */
export function timeSpan(b: Pick<Order, "starts_at" | "ends_at">): string {
  if (!b.starts_at) return "";
  return b.ends_at ? `${lagosTime(b.starts_at)}–${lagosTime(b.ends_at)}` : lagosTime(b.starts_at);
}

/** The statuses in which the owner may still cancel or move a booking (engine/storefront liveBooking). */
export const LIVE_BOOKING = ["received", "accepted", "awaiting_payment", "paid"];
export const canChange = (b: Pick<Order, "kind" | "status">) => b.kind === "booking" && LIVE_BOOKING.includes(b.status);

/** Why a move cannot be sent yet, or null. */
export function moveProblem(day: string, time: string): string | null {
  if (!DATE.test(day)) return "Choose the new date.";
  if (!HHMM.test(time)) return "Choose the new time.";
  return null;
}

/** A services table row from what the owner types (no-technical-steps
 * N7): the name, minutes it takes and a price in naira, in the table's
 * own columns. */
export function serviceRow(columns: ServiceColumns, name: string, minutes: string, price: string): { row: Record<string, unknown> } | { error: string } {
  const base = itemRow(columns, name, price);
  if ("error" in base) return base;
  const m = Number(minutes.trim());
  if (!Number.isInteger(m) || m < 5 || m > 480) return { error: "Say how long it takes in minutes, from 5 to 480." };
  if (!columns.duration) return { error: "The services have no duration column." };
  // A services table keeps no "available" flag the editor sets.
  const row = Object.fromEntries(Object.entries({ ...base.row, [columns.duration]: m }).filter(([k]) => k !== columns.available));
  return { row };
}
