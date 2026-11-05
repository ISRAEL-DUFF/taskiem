// The unread count in the browser tab: "(3) Taskiem" in the title, and a dot
// on the tab's icon.

const BADGE = /^\(\d+\+?\) /;

/** The title as the app gave it, without a count. */
export function stripBadge(title: string): string {
  return title.replace(BADGE, "");
}

/** The title with the count in front (nothing for none; 99+ for many). */
export function withBadge(title: string, n: number): string {
  const base = stripBadge(title);
  if (n <= 0) return base;
  return `(${n > 99 ? "99+" : n}) ${base}`;
}
