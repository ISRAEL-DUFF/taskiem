// Times from the server are RFC 3339 with as many fractional digits as the
// time needs ("…:00.5Z", "…:00.50123Z", "…:00Z"), so two of them cannot be
// compared as strings ("…00.5Z" sorts after "…00.50123Z", as "Z" follows
// "0"). `stamp` pads the fraction to nanoseconds, after which the strings
// compare as the times do, to the nanosecond (a Date would stop at the
// millisecond).

export function stamp(iso: string): string {
  const m = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?Z$/.exec(iso);
  if (!m) return iso;
  return `${m[1]}.${(m[2] ?? "").padEnd(9, "0").slice(0, 9)}Z`;
}

/** Negative, zero or positive as `a` is before, at or after `b`. */
export function cmpTime(a: string, b: string): number {
  const x = stamp(a);
  const y = stamp(b);
  return x < y ? -1 : x > y ? 1 : 0;
}

export const isAfter = (a: string, b: string) => cmpTime(a, b) > 0;
