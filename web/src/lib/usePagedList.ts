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

  const reload = useCallback(() => {
    get<R>(base).then(
      (r) => {
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
    (fn: (rows: T[]) => T[] | undefined) =>
      setSt((s) => {
        if (!s || s.base !== base) return s;
        const rows = fn(s.rows);
        return rows && rows !== s.rows ? { ...s, rows } : s;
      }),
    [base],
  );

  return { rows: cur?.rows, head: cur?.head, error, more: !!cur?.more, loadingMore, loadMore, reload, patch };
}
