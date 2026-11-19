// A small memory of lists already loaded (Discover's results, Orders on
// You's pages), so Back from a business's profile or an order returns the
// person to the list they were reading: the same rows, the pages they had
// opened, and where they had scrolled to. Kept in this tab's memory only
// (gone on a reload), for a few minutes, and only a few lists at a time.

export interface Kept<T> {
  value: T;
  /** Where the window was scrolled to when the list was left. */
  scroll: number;
  at: number;
}

export class PageCache<T> {
  private kept = new Map<string, Kept<T>>();

  constructor(
    private max = 8,
    private ttlMs = 5 * 60_000,
    private now: () => number = () => Date.now(),
  ) {}

  /** The list kept under key, while it is fresh. */
  get(key: string): Kept<T> | undefined {
    const k = this.kept.get(key);
    if (!k) return undefined;
    if (this.now() - k.at > this.ttlMs) {
      this.kept.delete(key);
      return undefined;
    }
    return k;
  }

  /** Keeps a list (newest last; the oldest goes past max). */
  set(key: string, value: T, scroll = 0): void {
    this.kept.delete(key);
    this.kept.set(key, { value, scroll, at: this.now() });
    while (this.kept.size > this.max) {
      const oldest = this.kept.keys().next().value;
      if (oldest === undefined) break;
      this.kept.delete(oldest);
    }
  }

  /** Notes where the list was scrolled to, keeping it as it is. */
  scrolled(key: string, scroll: number): void {
    const k = this.kept.get(key);
    if (k) k.scroll = scroll;
  }

  delete(key: string): void {
    this.kept.delete(key);
  }

  clear(): void {
    this.kept.clear();
  }
}
