// Times as people say them: "Today, 09:41", "Yesterday, 18:02",
// "Tue 14 Oct, 08:00", and the year only when it is not this one. Day
// before month and a 24-hour clock, as in Nigeria.

const pad = (n: number) => String(n).padStart(2, "0");
const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

/** The calendar day of `d`, as `whenWords` names it. */
export function dayWords(d: Date, now = new Date()): string {
  const days = Math.round((startOfDay(now) - startOfDay(d)) / 86_400_000);
  if (days === 0) return "Today";
  if (days === 1) return "Yesterday";
  const date = `${d.getDate()} ${MONTHS[d.getMonth()]}`;
  if (d.getFullYear() !== now.getFullYear()) return `${date} ${d.getFullYear()}`;
  if (days > 1 && days < 7) return `${DAYS[d.getDay()]} ${date}`;
  return date;
}

/** A moment in local time, in words. */
export function whenWords(d: Date, now = new Date()): string {
  return `${dayWords(d, now)}, ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Only the clock time when it is today, else `whenWords`: for lists. */
export function shortWhen(d: Date, now = new Date()): string {
  return dayWords(d, now) === "Today" ? `${pad(d.getHours())}:${pad(d.getMinutes())}` : whenWords(d, now);
}

/** A greeting for the hour. */
export function greeting(now = new Date()): string {
  const h = now.getHours();
  return h < 12 ? "Good morning" : h < 17 ? "Good afternoon" : "Good evening";
}
