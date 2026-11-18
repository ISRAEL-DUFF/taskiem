import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { get } from "../api";
import { listTime } from "../lib/linkchat";
import { messageLine } from "../lib/people";
import { appendFound, foundPath, foundWords, searchable, searchURL, snippetParts, starredURL, type FoundPage, type PeopleFound } from "../lib/peoplelive";
import { EmptyState, ErrorBox, Item, Items, PageHeader, Skeleton } from "../ui";
import { MoreRows } from "./MoreRows";

// Search across the person's own people chats, and their starred messages
// (docs/spec/people-chatting.md §6.2, §7; "As built: stage 5a gaps"): rows
// of messages, each opening its chat at the message. Pages of 30 found
// (50 starred), the next when the list's end comes into view.

/** A list of found messages read a page at a time from `url(before)`. */
function useFound(url: ((before?: string) => string) | undefined, rowsOf: (p: FoundPage) => PeopleFound[] | undefined) {
  const [st, setSt] = useState<{ key: string; rows: PeopleFound[]; more: boolean; next?: string }>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const key = url?.() ?? "";
  const of = useRef(rowsOf);
  of.current = rowsOf;
  useEffect(() => {
    if (!url) return;
    let live = true;
    setError(undefined);
    get<FoundPage>(key).then(
      (p) => live && setSt({ key, rows: of.current(p) ?? [], more: p.more, next: p.next }),
      (e: unknown) => live && setError(e),
    );
    return () => {
      live = false;
    };
    // The address is what changes.
  }, [key]);
  const cur = st?.key === key ? st : undefined;
  const more = () => {
    if (!url || !cur?.more || !cur.next || busy) return;
    setBusy(true);
    get<FoundPage>(url(cur.next))
      .then(
        (p) => setSt((s) => (s && s.key === key ? { key, rows: appendFound(s.rows, of.current(p) ?? []), more: p.more, next: p.next } : s)),
        (e: unknown) => setError(e),
      )
      .finally(() => setBusy(false));
  };
  return { rows: cur?.rows, more: !!cur?.more, busy, loadMore: more, error, loading: !!url && !cur && !error };
}

/** A found message's line: the snippet with the words searched for in bold, else what it holds. */
function FoundLine({ f, q }: { f: PeopleFound; q?: string }) {
  const who = f.message.mine ? "You: " : "";
  if (f.snippet && q)
    return (
      <>
        {who}
        {snippetParts(f.snippet, q).map((p, i) => (p.hit ? <strong key={i}>{p.text}</strong> : <span key={i}>{p.text}</span>))}
      </>
    );
  return (
    <>
      {who}
      {messageLine(f.message)}
    </>
  );
}

/** /you/people/search (?q=, ?chat= to keep to one chat, &name= its name). */
export function PeopleSearchPage() {
  const loc = useLocation();
  const nav = useNavigate();
  const params = new URLSearchParams(loc.search);
  const chat = params.get("chat") ?? undefined;
  const chatName = params.get("name") ?? undefined;
  const [text, setText] = useState(params.get("q") ?? "");
  const [q, setQ] = useState(params.get("q") ?? "");
  // Searched a moment after the typing stops; the words kept in the
  // address, so Back from a chat comes back to them.
  useEffect(() => {
    const t = window.setTimeout(() => {
      setQ(text.trim());
      const next = new URLSearchParams(loc.search);
      if (text.trim()) next.set("q", text.trim());
      else next.delete("q");
      nav({ search: next.toString() }, { replace: true });
    }, 400);
    return () => window.clearTimeout(t);
  }, [text]);
  const ok = searchable(q);
  const found = useFound(ok ? (before) => searchURL(q, chat, before) : undefined, (p) => p.results);
  const back = chat ? `/you/people/${encodeURIComponent(chat)}` : "/you";
  return (
    <>
      <p className="crumbs">
        <Link to={back} aria-label={chat ? "Back to the chat" : "Back to Chats"}>
          {chat ? (chatName ?? "Chat") : "Chats"}
        </Link>
      </p>
      <PageHeader title={chat && chatName ? `Search in your chat with ${chatName}` : "Search messages"} description="Only your own chats with people are searched." />
      <section className="card" aria-label="Search" data-testid="people-search">
        <form role="search" className="people-search-form" onSubmit={(e) => (e.preventDefault(), setQ(text.trim()))}>
          <label htmlFor="people-search-q" className="sr-only">
            Words to find
          </label>
          <input
            id="people-search-q"
            type="search"
            autoFocus
            value={text}
            maxLength={200}
            placeholder="Words to find"
            onChange={(e) => setText(e.target.value)}
            data-testid="people-search-q"
          />
        </form>
        {!q && <p className="hint">Type a word or two. A word is found at its start: "jol" finds "jollof".</p>}
        {q && !ok && <p className="hint" data-testid="people-search-short">Type a word of two letters or more.</p>}
        <ErrorBox error={found.error} />
        {found.loading && <Skeleton rows={2} label="Searching…" />}
        {ok && found.rows && (
          <>
            <p className="hint" role="status" data-testid="people-search-count">
              {foundWords(found.rows.length, found.more)}
            </p>
            {found.rows.length === 0 ? (
              <EmptyState icon="search">Nothing in your chats with people has these words.</EmptyState>
            ) : (
              <Items label="Messages found">
                {found.rows.map((f) => (
                  <Item
                    key={f.message.id}
                    to={foundPath(f)}
                    icon="chat"
                    title={f.chat.name}
                    side={listTime(f.message.created_at)}
                    sub={<FoundLine f={f} q={q} />}
                    testid="people-found"
                  />
                ))}
              </Items>
            )}
            <MoreRows more={found.more} busy={found.busy} onMore={found.loadMore} label="Show more found" testid="people-search-more" />
          </>
        )}
      </section>
    </>
  );
}

/** /you/people/starred: the person's starred messages, the latest starred first. */
export function PeopleStarredPage() {
  const found = useFound(starredURL, (p) => p.starred);
  return (
    <>
      <p className="crumbs">
        <Link to="/you" aria-label="Back to Chats">
          Chats
        </Link>
      </p>
      <PageHeader title="Starred messages" description="Messages you starred in your chats with people. Only you see your stars." />
      <section className="card" aria-label="Starred messages" data-testid="people-starred">
        <ErrorBox error={found.error} />
        {found.loading && <Skeleton rows={2} label="Loading your starred messages…" />}
        {found.rows && found.rows.length === 0 && <EmptyState icon="star">No starred messages yet. Star one from its menu in a chat to find it here.</EmptyState>}
        {found.rows && found.rows.length > 0 && (
          <Items label="Starred messages">
            {found.rows.map((f) => (
              <Item key={f.message.id} to={foundPath(f)} icon="star" title={f.chat.name} side={listTime(f.message.created_at)} sub={<FoundLine f={f} />} testid="people-starred-row" />
            ))}
          </Items>
        )}
        <MoreRows more={found.more} busy={found.busy} onMore={found.loadMore} label="Show more starred" testid="people-starred-more" />
      </section>
    </>
  );
}
