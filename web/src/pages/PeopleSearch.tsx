import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { get } from "../api";
import { listTime } from "../lib/linkchat";
import { messageLine } from "../lib/people";
import { foundPath, foundWords, searchable, searchURL, snippetParts, starredURL, type FoundPage, type PeopleFound } from "../lib/peoplelive";
import { groupFoundPath, groupPath, groupSearchURL, groupStarredURL, shownFound, type FoundRow, type GroupFound } from "../lib/groups";
import { EmptyState, ErrorBox, Item, Items, PageHeader, Skeleton } from "../ui";
import { MoreRows } from "./MoreRows";

// Search across the person's own people chats and groups, and their
// starred messages (docs/spec/people-chatting.md §6.2, §7, §9; "As built:
// stage 5a gaps", "As built: stage 5b, groups (server)"): rows of
// messages, each opening its chat or group at the message, people chats'
// and groups' merged by time. Pages of 30 found (50 starred) from each,
// the next when the list's end comes into view.

/** One list the page reads, a page at a time. */
interface Source {
  url: (before?: string) => string;
  rows: (p: FoundPage & { starred?: unknown[]; results?: unknown[] }) => FoundRow[];
}

const peopleRow =
  (starred: boolean) =>
  (f: PeopleFound): FoundRow => ({
    key: `p:${f.message.id}`,
    path: foundPath(f),
    title: f.chat.name,
    group: false,
    at: (starred && f.starred_at) || f.message.created_at,
    mine: f.message.mine,
    snippet: f.snippet,
    line: messageLine(f.message),
  });

const groupRow =
  (starred: boolean) =>
  (f: GroupFound): FoundRow => ({
    key: `g:${f.message.id}`,
    path: groupFoundPath(f),
    title: f.group.name,
    group: true,
    at: (starred && f.starred_at) || f.message.created_at,
    mine: f.message.mine,
    sender: f.message.mine ? undefined : f.message.sender,
    snippet: f.snippet,
    line: messageLine(f.message),
  });

/** Found messages read from several lists, merged newest first. */
function useFound(sources: Source[] | undefined, key: string) {
  const [st, setSt] = useState<{ key: string; lists: { rows: FoundRow[]; more: boolean; next?: string }[] }>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const src = useRef(sources);
  src.current = sources;
  useEffect(() => {
    const s = src.current;
    if (!s) return;
    let live = true;
    setError(undefined);
    Promise.all(s.map((x) => get<FoundPage>(x.url()).then((p) => ({ rows: x.rows(p), more: p.more, next: p.next })))).then(
      (lists) => live && setSt({ key, lists }),
      (e: unknown) => live && setError(e),
    );
    return () => {
      live = false;
    };
  }, [key]);
  const cur = st?.key === key ? st : undefined;
  const more = !!cur?.lists.some((l) => l.more);
  const loadMore = () => {
    const s = src.current;
    if (!s || !cur || !more || busy) return;
    setBusy(true);
    Promise.all(
      cur.lists.map((l, i) =>
        l.more && l.next && s[i]
          ? get<FoundPage>(s[i].url(l.next)).then((p) => ({ rows: [...l.rows, ...(s[i]?.rows(p) ?? [])], more: p.more, next: p.next }))
          : Promise.resolve(l),
      ),
    )
      .then(
        (lists) => setSt((x) => (x && x.key === key ? { key, lists } : x)),
        (e: unknown) => setError(e),
      )
      .finally(() => setBusy(false));
  };
  const rows = cur ? shownFound(cur.lists) : undefined;
  return { rows, more, busy, loadMore, error, loading: !!sources && !cur && !error };
}

/** A found message's line: who wrote it in a group, the snippet with the
 * words searched for in bold, else what it holds. */
function FoundLine({ f, q }: { f: FoundRow; q?: string }) {
  const who = f.mine ? "You: " : f.sender ? `${f.sender}: ` : "";
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
      {f.line}
    </>
  );
}

function FoundTitle({ f }: { f: FoundRow }) {
  return (
    <>
      {f.title}
      {f.group && <span className="sr-only"> (a group)</span>}
    </>
  );
}

/** /you/people/search (?q=, ?chat= or ?group= to keep to one, &name= its name). */
export function PeopleSearchPage() {
  const loc = useLocation();
  const nav = useNavigate();
  const params = new URLSearchParams(loc.search);
  const chat = params.get("chat") ?? undefined;
  const group = params.get("group") ?? undefined;
  const oneName = params.get("name") ?? undefined;
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
  const sources: Source[] = [];
  if (!group) sources.push({ url: (before) => searchURL(q, chat, before), rows: (p) => (p.results as PeopleFound[] | undefined)?.map(peopleRow(false)) ?? [] });
  if (!chat) sources.push({ url: (before) => groupSearchURL(q, group, before), rows: (p) => (p.results as unknown as GroupFound[] | undefined)?.map(groupRow(false)) ?? [] });
  const found = useFound(ok ? sources : undefined, `${q}|${chat ?? ""}|${group ?? ""}`);
  const back = chat ? `/you/people/${encodeURIComponent(chat)}` : group ? groupPath(group) : "/you";
  const one = chat ?? group;
  return (
    <>
      <p className="crumbs">
        <Link to={back} aria-label={one ? `Back to ${oneName ?? "the chat"}` : "Back to Chats"}>
          {one ? (oneName ?? "Chat") : "Chats"}
        </Link>
      </p>
      <PageHeader
        title={chat && oneName ? `Search in your chat with ${oneName}` : group && oneName ? `Search in ${oneName}` : "Search messages"}
        description={group ? "Only messages you can see in this group are searched." : "Only your own chats with people and your groups are searched."}
      />
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
              <EmptyState icon="search">Nothing in your chats with people or your groups has these words.</EmptyState>
            ) : (
              <Items label="Messages found">
                {found.rows.map((f) => (
                  <Item
                    key={f.key}
                    to={f.path}
                    icon={f.group ? "members" : "chat"}
                    title={<FoundTitle f={f} />}
                    side={listTime(f.at)}
                    sub={<FoundLine f={f} q={q} />}
                    testid={f.group ? "group-found" : "people-found"}
                  />
                ))}
              </Items>
            )}
            <MoreRows more={found.more} busy={found.busy} onMore={found.loadMore} count={found.rows?.length ?? 0} label="Show more found" testid="people-search-more" />
          </>
        )}
      </section>
    </>
  );
}

const STARRED: Source[] = [
  { url: starredURL, rows: (p) => (p.starred as PeopleFound[] | undefined)?.map(peopleRow(true)) ?? [] },
  { url: groupStarredURL, rows: (p) => (p.starred as unknown as GroupFound[] | undefined)?.map(groupRow(true)) ?? [] },
];

/** /you/people/starred: the person's starred messages in people chats and
 * groups, the latest starred first. */
export function PeopleStarredPage() {
  const found = useFound(STARRED, "starred");
  return (
    <>
      <p className="crumbs">
        <Link to="/you" aria-label="Back to Chats">
          Chats
        </Link>
      </p>
      <PageHeader title="Starred messages" description="Messages you starred in your chats with people and your groups. Only you see your stars." />
      <section className="card" aria-label="Starred messages" data-testid="people-starred">
        <ErrorBox error={found.error} />
        {found.loading && <Skeleton rows={2} label="Loading your starred messages…" />}
        {found.rows && found.rows.length === 0 && <EmptyState icon="star">No starred messages yet. Star one from its menu in a chat to find it here.</EmptyState>}
        {found.rows && found.rows.length > 0 && (
          <Items label="Starred messages">
            {found.rows.map((f) => (
              <Item key={f.key} to={f.path} icon="star" title={<FoundTitle f={f} />} side={listTime(f.at)} sub={<FoundLine f={f} />} testid={f.group ? "group-starred-row" : "people-starred-row"} />
            ))}
          </Items>
        )}
        <MoreRows more={found.more} busy={found.busy} onMore={found.loadMore} count={found.rows?.length ?? 0} label="Show more starred" testid="people-starred-more" />
      </section>
    </>
  );
}
