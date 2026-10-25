// The money charts on Home and Money > Overview: the day's money in as a
// line (GET /v1/money/summary for today, in hours) and a period's money in
// and out as bars (in days). Days and hours are Lagos's, as the summary's.

/** One stretch of a money summary (MoneySummary.buckets). */
export interface MoneyBucket {
  from: string;
  in_settled_kobo: number;
  out_settled_kobo: number;
  payments: number;
}

const LAGOS = "Africa/Lagos";

/** The hour of the day in Lagos, 0 to 23. */
export function lagosHour(d: Date): number {
  return Number(new Intl.DateTimeFormat("en-GB", { timeZone: LAGOS, hour: "2-digit", hourCycle: "h23" }).format(d));
}

/** "7 am", "12 pm", "midnight". */
export function hourWords(h: number): string {
  if (h === 0 || h === 24) return "midnight";
  if (h === 12) return "noon";
  return h < 12 ? `${h} am` : `${h - 12} pm`;
}

/**
 * The day's money in, added up hour by hour, from 6 am (or the first
 * payment, if earlier) to the hour now: the points of the line and the
 * hour it starts at. Null before the first hour has passed.
 */
export function dayLine(buckets: MoneyBucket[], now = new Date()): { points: number[]; start: number; end: number } | null {
  if (buckets.length === 0) return null;
  const end = Math.min(buckets.length - 1, lagosHour(now));
  const first = buckets.findIndex((b) => b.in_settled_kobo > 0);
  const start = Math.min(6, first < 0 ? 6 : first, end);
  const points: number[] = [];
  let sum = 0;
  for (let h = 0; h <= end; h++) {
    sum += buckets[h]?.in_settled_kobo ?? 0;
    if (h >= start) points.push(sum);
  }
  return points.length < 2 ? { points: [0, ...points], start, end } : { points, start, end };
}

/** The line's points in a w×h box, top 4px clear, as an SVG polyline's points and the area under it as a path. */
export function linePath(values: number[], w: number, h: number): { line: string; area: string } {
  const max = Math.max(1, ...values);
  const step = values.length > 1 ? w / (values.length - 1) : w;
  const pts = values.map((v, i) => [Math.round(i * step * 10) / 10, Math.round((h - 2 - ((h - 6) * v) / max) * 10) / 10] as const);
  const line = pts.map(([x, y]) => `${x},${y}`).join(" ");
  const area = `M0 ${h} ${pts.map(([x, y]) => `L${x} ${y}`).join(" ")} L${w} ${h} Z`;
  return { line, area };
}

/** A bucket's short label under its bar: "Mon" for a day, "9" for an hour. */
export function bucketShort(b: Pick<MoneyBucket, "from">, unit: string): string {
  const d = new Date(b.from);
  if (unit === "hour") return String(lagosHour(d));
  return new Intl.DateTimeFormat("en-GB", { timeZone: LAGOS, weekday: "short" }).format(d);
}

/** A bucket in words: "Today", "Monday 19 October", "2 pm to 3 pm". */
export function bucketLong(b: Pick<MoneyBucket, "from">, unit: string, now = new Date()): string {
  const d = new Date(b.from);
  if (unit === "hour") {
    const h = lagosHour(d);
    return `${hourWords(h)} to ${hourWords(h + 1)}`;
  }
  const day = (x: Date) => new Intl.DateTimeFormat("en-CA", { timeZone: LAGOS }).format(x);
  if (day(d) === day(now)) return "Today";
  return new Intl.DateTimeFormat("en-GB", { timeZone: LAGOS, weekday: "long", day: "numeric", month: "long" }).format(d);
}

/** The bar a chart opens on: the last that has begun (today's, in a period that holds today). */
export function openBucket(buckets: Pick<MoneyBucket, "from">[], now = new Date()): number {
  for (let i = buckets.length - 1; i >= 0; i--) {
    const b = buckets[i];
    if (b && new Date(b.from).getTime() <= now.getTime()) return i;
  }
  return buckets.length - 1;
}
