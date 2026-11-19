import { useCallback, useEffect, useRef, useState } from "react";
import { get } from "../api";
import { appendPage, pageURL, withFirstPage, type Paged } from "./people";

/** A list the server answers a page at a time (`more`, `next` passed as
 * `before`; people-chatting, Review fixes): the first page read (and read
 * again every `pollMs` while the page is seen), later pages added by
 * `loadMore`. `head` is the latest first page's answer, for what is the
 * whole list's (unread, requests). */
export function usePagedList<R extends Paged, T>(
  base: string,
  rowsOf: (r: R) => T[],
  key: (t: T) => string,
  pollMs = 0,
): {
  rows: T[] | undefined;
  head: R | undefined;
  error: unknown;
  more: boolean;
  loadingMore: boolean;
  loadMore: () => void;
  reload: () => void;
  /** Changes the rows read so far in place (a live event), until the next read. */
  patch: (fn: (rows: T[]) => T[] | undefined) => void;
} {
  const [st, setSt] = useState<{ base: string; head: R; rows: T[]; next?: string; more: boolean; laterRead: boolean }>();
  const [error, setError] = useState<unknown>();
  const [loadingMore, setLoadingMore] = useState(false);
  const fns = useRef({ rowsOf, key });
  fns.current = { rowsOf, key };
  const busyMore = useRef(false);

  const order = useRef(new ReadOrder());

  const reload = useCallback(() => {
    const seq = order.current.start();
    get<R>(base).then(
      (r) => {
        // An answer older than one shown, or asked before a live patch
        // (it would undo it): dropped, and read again when it was the last.
        const take = order.current.take(seq);
        if (take !== "apply") {
          if (take === "again") reload();
          return;
        }
        const { rowsOf: of, key: k } = fns.current;
        setError(undefined);
        setSt((s) => {
          if (!s || s.base !== base) return { base, head: r, rows: of(r), next: r.next, more: !!r.more, laterRead: false };
          if (!r.more) return { base, head: r, rows: of(r), next: undefined, more: false, laterRead: false };
          return {
            ...s,
            head: r,
            rows: withFirstPage(s.rows, of(r), k, true, s.laterRead),
            next: s.laterRead ? s.next : r.next,
            more: s.laterRead ? s.more : true,
          };
        });
      },
      (e: unknown) => setError(e),
    );
  }, [base]);

  useEffect(() => {
    reload();
    if (!pollMs) return;
    const t = setInterval(() => document.visibilityState === "visible" && reload(), pollMs);
    return () => clearInterval(t);
  }, [reload, pollMs]);

  const cur = st?.base === base ? st : undefined;
  const loadMore = useCallback(() => {
    if (!cur?.more || !cur.next || busyMore.current) return;
    busyMore.current = true;
    setLoadingMore(true);
    get<R>(pageURL(base, cur.next))
      .then(
        (r) => {
          const { rowsOf: of, key: k } = fns.current;
          setSt((s) => (s && s.base === base ? { ...s, rows: appendPage(s.rows, of(r), k), next: r.next, more: !!r.more, laterRead: true } : s));
        },
        (e: unknown) => setError(e),
      )
      .finally(() => {
        busyMore.current = false;
        setLoadingMore(false);
      });
  }, [base, cur?.more, cur?.next]);

  const patch = useCallback(
    (fn: (rows: T[]) => T[] | undefined) => {
      // A read already asked for may not hold this change yet.
      order.current.patched();
      setSt((s) => {
        if (!s || s.base !== base) return s;
        const rows = fn(s.rows);
        return rows && rows !== s.rows ? { ...s, rows } : s;
      });
    },
    [base],
  );

  return { rows: cur?.rows, head: cur?.head, error, more: !!cur?.more, loadingMore, loadMore, reload, patch };
}

/** The order of a list's reads against its live patches: an answer is
 * shown only when it is newer than the last one shown and was asked for
 * after the last patch. One asked before a patch could undo it: it is
 * dropped, and when no later read is on its way the list is read again. */
export class ReadOrder {
  private asked = 0;
  private shown = 0;
  private patchedAfter = 0;
  /** A read starts: its number. */
  start(): number {
    return ++this.asked;
  }
  /** A live patch: every read asked so far may not hold it. */
  patched(): void {
    this.patchedAfter = this.asked;
  }
  /** A read's answer came: show it, drop it, or drop it and read again. */
  take(seq: number): "apply" | "drop" | "again" {
    if (seq <= this.shown) return "drop";
    if (seq <= this.patchedAfter) return seq === this.asked ? "again" : "drop";
    this.shown = seq;
    return "apply";
  }
}
