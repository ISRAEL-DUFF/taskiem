// People groups on the web (docs/spec/people-chatting.md §9, §13; "As
// built: stage 5b, groups (server)" and "…, group safety (server)"): what
// the API answers for a group, its messages, members, invites, link and
// the live connection's group events, and the words the screens build from
// it. Pure helpers, tested on their own.
//
// Groups are kept by Taskiem, sealed at rest and readable by its servers.
// No word here may say or suggest that they are end-to-end encrypted.

import { mayDeleteForEveryone, mergePeople, type ChatListItem, type PeopleAttachment, type PeopleCard, type PersonCard, type PersonPhoto } from "./people";

// --- what the API answers ---

/** A closed group (Taskiem's moderators): for members, and with the
 * closure's id, reason and appeal for its admins. */
export interface GroupClosed {
  at: string;
  until: string | null;
  words: string;
  action_id?: string;
  reason?: string;
  can_appeal: boolean;
  appeal?: { id: string; text: string; state: string; at: string; decided_at: string | null; note: string | null };
}

/** One of the person's groups, as their side shows it. */
export interface Group {
  id: string;
  kind: "group";
  name: string;
  description: string;
  photo: PersonPhoto | null;
  members: number;
  role: "admin" | "member";
  left: boolean;
  left_how?: "left" | "removed";
  admins_only_post: boolean;
  admins_only_edit: boolean;
  approve_joins: boolean;
  can_post: boolean;
  can_edit: boolean;
  muted: boolean;
  archived: boolean;
  /** A mention pushes even while muted (A7). */
  mentions: boolean;
  unread: number;
  mentioned: boolean;
  last: string;
  last_mine: boolean;
  last_sender?: string;
  last_at: string;
  joined_at: string;
  join_requests?: number;
  closed?: GroupClosed;
  link_stopped?: boolean;
  kept?: string;
}

/** What happened in a group, kept as a message. */
export interface GroupEvent {
  type: string;
  actor_id: string;
  target_id?: string;
}

/** One message of a group, as the person sees it. */
export interface GroupMessage {
  id: string;
  group_id: string;
  mine: boolean;
  sender_id: string | null;
  /** "You", the name as the person sees them, "Deleted account", or "Taskiem". */
  sender: string;
  kind: "text" | "media" | "card" | "location" | "event";
  body: string;
  event?: GroupEvent;
  card?: PeopleCard;
  location?: { lat: number; lng: number; label?: string };
  attachment?: PeopleAttachment;
  reply_to?: string;
  forwarded?: boolean;
  forwarded_many?: boolean;
  mentions?: string[];
  mentions_me?: boolean;
  status?: "sent" | "delivered" | "read";
  starred?: boolean;
  my_reaction?: string;
  reactions?: { reaction: string; count: number }[];
  deleted?: boolean;
  warn_transfer?: boolean;
  warn?: boolean;
  created_at: string;
  /** Ours, still on its way (not from the server). */
  local?: boolean;
  localPreview?: string;
}

export interface GroupMember {
  person_id: string;
  name: string;
  role: "admin" | "member";
  joined_at: string;
  you: boolean;
  person?: PersonCard;
  phone?: string;
}

/** Someone invited, or waiting to be let in by the link (admins see them). */
export interface GroupPending {
  person_id: string;
  name: string;
  at: string;
}

export interface GroupMembers {
  members: GroupMember[];
  invited?: GroupPending[];
  waiting?: GroupPending[];
}

export type AddState = "added" | "invited" | "member" | "full" | "unavailable";

export interface GroupAdded {
  person_id: string;
  state: AddState;
}

export interface GroupInvite {
  group_id: string;
  name: string;
  photo: PersonPhoto | null;
  invited_by: string;
  by: string;
  at: string;
}

export interface GroupLink {
  on: boolean;
  code?: string;
  url?: string;
  created_at?: string;
  words?: string;
  qr?: string;
}

/** GET /v1/group-links/{code}, for anyone. */
export interface GroupLinkPreview {
  code: string;
  link: string;
  name: string;
  photo: PersonPhoto | null;
  members: number;
  full: boolean;
}

export type JoinState = "joined" | "waiting" | "member";

export interface GroupReader {
  person_id: string;
  name: string;
  at: string | null;
}

export interface GroupMessageInfo {
  read_by: GroupReader[];
  delivered_to: GroupReader[];
  waiting: number;
}

export interface GroupFound {
  group_id: string;
  group: Group;
  message: GroupMessage;
  snippet?: string;
  starred_at?: string;
}

// --- the live connection's group events ---

export interface LiveGroupMessage {
  group_id: string;
  message: GroupMessage;
}
export type LiveGroup = { id: string; hidden: false; group: Group } | { id: string; hidden: true };
export interface LiveGroupReceipts {
  group_id: string;
  messages: { id: string; status: GroupMessage["status"] }[];
}
export interface LiveGroupInvite {
  group_id: string;
  /** null: the invite went (taken back by an admin, answered on another
   * device, the group gone). */
  invite: GroupInvite | null;
}
export interface LiveGroupTyping {
  groups: { group_id: string; people: string[] }[];
}

// --- words ---

/** How groups are kept, for a group's info (§6.2, §13): honest, never end-to-end. */
export const GROUP_KEPT_WORDS =
  "Groups are kept by Taskiem, sealed on its servers, so they show on every phone you sign in on. " +
  "Taskiem’s servers can open them: they are not end-to-end encrypted. " +
  "No one at Taskiem reads them, except a moderator when a group or a member is reported, or when the law requires it.";

/** What reporting a group or a member asks consent for (§8.3). */
export const GROUP_REPORT_CONSENT = "The last 20 messages you can see, with their photos and files, go to Taskiem's moderators, so they can see what happened.";

/** The privacy setting "Who can add me to groups" (A3). */
export const GROUPS_ADD_CHOICES: { value: "contacts" | "everyone" | "nobody"; words: string }[] = [
  { value: "contacts", words: "My contacts" },
  { value: "everyone", words: "Everyone" },
  { value: "nobody", words: "Nobody" },
];
export const GROUPS_ADD_HINT = "People you haven't added send you an invite instead. It waits in your Requests.";

/** What an add said for each person, in words, never more than the server says. */
export function addedWords(state: AddState): string {
  return {
    added: "Added",
    invited: "Invited: they'll see it in their Requests",
    member: "Already in the group",
    full: "The group is full",
    unavailable: "Not on Taskiem",
  }[state];
}

/** "1 member", "12 members". */
export const membersWords = (n: number) => (n === 1 ? "1 member" : `${n} members`);

/** "1 invite to a group", "2 invites to groups". */
export const invitesWords = (n: number) => (n === 1 ? "1 invite to a group" : `${n} invites to groups`);

/** A group's row in the one Chats list: its last line with who wrote it. */
export function groupPreview(g: Pick<Group, "last" | "last_mine" | "last_sender" | "left" | "left_how" | "closed">): string {
  if (!g.last) {
    if (g.left) return g.left_how === "removed" ? "You were removed" : "You left";
    return "No messages yet";
  }
  if (g.last_sender) return `${g.last_mine ? "You" : g.last_sender}: ${g.last}`;
  return g.last;
}

/** The line under a group's name in its head: who is typing, else what the
 * group is (closed, left) or how many are in it. */
export function groupHeadWords(g: Pick<Group, "members" | "left" | "left_how" | "closed"> | undefined, typing: readonly string[] = []): string {
  if (!g) return "";
  if (typing.length === 1) return `${typing[0]} is typing…`;
  if (typing.length === 2) return `${typing[0]} and ${typing[1]} are typing…`;
  if (typing.length > 2) return `${typing.length} people are typing…`;
  if (g.left) return g.left_how === "removed" ? "You were removed from this group" : "You left this group";
  if (g.closed) return "Closed by Taskiem's moderators";
  return membersWords(g.members);
}

/** Why the composer is not there, in its place; null when the person may post. */
/** A group a card may be shared into from outside it (5b): one you may
 * post in now: still in it, not closed by Taskiem, and not admins-only
 * unless you are one (the server's can_post). */
export function canShareInto(g: Pick<Group, "can_post" | "left" | "closed">): boolean {
  return g.can_post && !g.left && !g.closed;
}

export function noPostWords(g: Pick<Group, "can_post" | "left" | "left_how" | "closed" | "admins_only_post" | "role"> | undefined): string | null {
  if (!g || g.can_post) return null;
  if (g.closed) return g.closed.words;
  if (g.left)
    return g.left_how === "removed"
      ? "You were removed from this group. You can still read what came before, until you delete it."
      : "You left this group. You can still read what came before, until you delete it.";
  if (g.admins_only_post && g.role !== "admin") return "Only admins can send messages in this group.";
  return "You can't send messages in this group now.";
}

/** A message from Taskiem itself (its moderators' events). */
export const fromTaskiem = (m: Pick<GroupMessage, "sender_id" | "sender" | "kind">) => m.kind === "event" && m.sender_id === null && m.sender === "Taskiem";

/** Members in the order the info shows: you first, admins, then by name. */
export function membersInOrder(ms: GroupMember[]): GroupMember[] {
  return [...ms].sort((a, b) => Number(b.you) - Number(a.you) || Number(b.role === "admin") - Number(a.role === "admin") || a.name.localeCompare(b.name));
}

/** Who a mention may name: the members now, but never yourself or a deleted account. */
export function mentionable(ms: GroupMember[]): GroupMember[] {
  return ms.filter((m) => !m.you && !m.person?.deleted && m.name !== "Deleted account");
}

const letterOrDigit = /[\p{L}\p{N}]/u;

/** Where each "@Name" of these names is in the text: the longest name
 * first, each followed by an end or a character that is not a letter. */
function mentionSpans(text: string, names: { key: string; name: string }[]): { at: number; len: number; key: string }[] {
  const lower = text.toLowerCase();
  const taken = new Array<boolean>(text.length).fill(false);
  const out: { at: number; len: number; key: string }[] = [];
  for (const n of [...names].filter((x) => x.name.trim()).sort((a, b) => b.name.length - a.name.length)) {
    const needle = "@" + n.name.toLowerCase();
    for (let from = 0; ; ) {
      const at = lower.indexOf(needle, from);
      if (at < 0) break;
      from = at + 1;
      const end = at + needle.length;
      const before = at === 0 ? "" : text.charAt(at - 1);
      if (before && letterOrDigit.test(before)) continue;
      if (end < text.length && letterOrDigit.test(text.charAt(end))) continue;
      if (taken.slice(at, end).some(Boolean)) continue;
      for (let i = at; i < end; i++) taken[i] = true;
      out.push({ at, len: needle.length, key: n.key });
    }
  }
  return out.sort((a, b) => a.at - b.at);
}

/** The members a message names with "@", as the send's `mentions`: each
 * "@Name" as a whole word. A name two members share names only the one
 * picked with "@" (its id kept with the name), never all of them. */
export function mentionedIn(text: string, members: Pick<GroupMember, "person_id" | "name">[], picks: { id: string; name: string }[] = []): string[] {
  const byName = new Map<string, string[]>();
  for (const m of members) {
    const k = m.name.toLowerCase();
    byName.set(k, [...(byName.get(k) ?? []), m.person_id]);
  }
  const ids: string[] = [];
  const spans = mentionSpans(
    text,
    [...byName.keys()].map((k) => ({ key: k, name: k })),
  );
  for (const s of spans) {
    const all = byName.get(s.key) ?? [];
    if (all.length === 1) ids.push(...all);
    else ids.push(...picks.filter((p) => p.name.toLowerCase() === s.key && all.includes(p.id)).map((p) => p.id));
  }
  return [...new Set(ids)];
}

/** A message's words in parts, each "@Name" of these names marked. */
export function mentionPieces(text: string, names: string[]): { text: string; mention: boolean }[] {
  const spans = mentionSpans(
    text,
    names.map((n) => ({ key: n, name: n })),
  );
  if (!spans.length) return [{ text, mention: false }];
  const out: { text: string; mention: boolean }[] = [];
  let at = 0;
  for (const s of spans) {
    if (s.at > at) out.push({ text: text.slice(at, s.at), mention: false });
    out.push({ text: text.slice(s.at, s.at + s.len), mention: true });
    at = s.at + s.len;
  }
  if (at < text.length) out.push({ text: text.slice(at), mention: false });
  return out;
}

/** "Read by" in words: who read it, who has it, who doesn't yet. */
export function readByWords(info: GroupMessageInfo): string {
  const parts: string[] = [];
  if (info.read_by.length) parts.push(`Read by ${info.read_by.length}`);
  if (info.delivered_to.length) parts.push(`delivered to ${info.delivered_to.length}`);
  if (info.waiting) parts.push(`${info.waiting} still to get it`);
  return parts.join(", ").replace(/^./, (x) => x.toUpperCase()) || "No one has it yet";
}

/** A joining's answer, in words. */
export function joinWords(state: JoinState, name: string): string {
  if (state === "waiting") return `Your request to join ${name} waits for an admin to let you in.`;
  if (state === "member") return `You are already in ${name}.`;
  return `You joined ${name}.`;
}

/** The code in a group's link, typed or pasted as a link or bare; "" when it is not one. */
export function groupLinkCode(text: string): string {
  const t = text.trim();
  const m = /(?:^|\/)g\/([A-Za-z0-9]{4,40})\/?(?:[?#].*)?$/.exec(t);
  const code = (m?.[1] ?? t).toLowerCase();
  return /^[a-z0-9]{4,40}$/.test(code) ? code : "";
}

/** A group's own page and its info. */
export const groupPath = (id: string) => `/you/groups/${encodeURIComponent(id)}`;
export const groupInfoPath = (id: string) => `/you/groups/${encodeURIComponent(id)}/info`;
export const groupFileURL = (group: string, message: string) => `/v1/me/groups/${encodeURIComponent(group)}/files/${encodeURIComponent(message)}`;

/** Where a found or starred group message opens: its group, at the message. */
export const groupFoundPath = (f: Pick<GroupFound, "group_id" | "message">) => `${groupPath(f.group_id)}?at=${encodeURIComponent(f.message.id)}`;

/** A group as a row of the one Chats list. */
export function groupAsRow(g: Group): ChatListItem {
  return { kind: "group", id: g.id, name: g.name, last: g.last, last_mine: g.last_mine, last_at: g.last_at, unread: g.unread, muted: g.muted, archived: g.archived, blocked: false, group: g };
}

const newestFirst = (a: { last_at: string }, b: { last_at: string }) => Date.parse(b.last_at) - Date.parse(a.last_at);

/** The one Chats list (or its archived part) after a `group` event: the
 * group's row in its place, newest first; gone when hidden or not of this part. */
export function rowsWithGroup(rows: ChatListItem[] | undefined, e: LiveGroup, archived: boolean): ChatListItem[] | undefined {
  if (!rows) return rows;
  const others = rows.filter((r) => !(r.kind === "group" && r.id === e.id));
  if (e.hidden || e.group.archived !== archived) return others.length === rows.length ? rows : others;
  return [...others, groupAsRow(e.group)].sort(newestFirst);
}

/** An open group's messages after a `group_message` event: as a people
 * chat's (our own told before its send was answered shown once; an old one
 * not read yet left out). */
export function groupThreadWithLive(cur: GroupMessage[] | undefined, group: string, e: LiveGroupMessage): GroupMessage[] | undefined {
  if (!cur || e.group_id !== group) return cur;
  const m = e.message;
  if (!cur.some((x) => x.id === m.id)) {
    const oldest = cur.find((x) => !x.local);
    if (oldest && Date.parse(m.created_at) < Date.parse(oldest.created_at)) return cur;
  }
  let kept = cur;
  if (m.mine && !cur.some((x) => x.id === m.id)) {
    const twin = cur.find((x) => x.local && !x.attachment && x.body === m.body && m.kind === "text");
    if (twin) kept = cur.filter((x) => x !== twin);
  }
  return mergePeople(kept, [m]);
}

/** An open group's messages after `group_receipts`: our ticks moved. */
export function groupThreadWithReceipts(cur: GroupMessage[] | undefined, group: string, e: LiveGroupReceipts): GroupMessage[] | undefined {
  if (!cur || e.group_id !== group) return cur;
  const by = new Map(e.messages.map((m) => [m.id, m.status]));
  let changed = false;
  const next = cur.map((m) => {
    const s = by.get(m.id);
    if (!s || s === m.status || !m.mine) return m;
    changed = true;
    return { ...m, status: s };
  });
  return changed ? next : cur;
}

/** Invites after a `group_invite` event: the invite in its place, newest
 * first; one that went (invite null) taken out. */
export function invitesWithInvite(rows: GroupInvite[] | undefined, e: LiveGroupInvite): GroupInvite[] | undefined {
  if (!rows) return rows;
  const rest = rows.filter((r) => r.group_id !== e.group_id);
  if (!e.invite) return rest;
  return [...rest, e.invite].sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
}

/** Whether an Accept or a Decline that failed means the invite is gone
 * already (taken back, answered on another device): read them again. */
export function inviteGone(e: unknown): boolean {
  return typeof e === "object" && e !== null && (e as { status?: unknown }).status === 404;
}

/** Who is typing in each group, from a `group_typing` event. */
export function typingByGroup(e: LiveGroupTyping): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const g of e.groups ?? []) if (g.people?.length) out.set(g.group_id, g.people);
  return out;
}

/** Reaction counts in words for a screen reader: "2 thumbs up, 1 heart". */
export function reactionCountWords(rs: { reaction: string; count: number }[], names: Record<string, string>): string {
  return rs.map((r) => `${r.count} ${names[r.reaction] ?? r.reaction}`).join(", ");
}

// --- search and starred, with people chats ---

/** One row of the merged search or starred list, from a people chat or a group. */
export interface FoundRow {
  key: string;
  path: string;
  title: string;
  /** A group's row says so for a screen reader. */
  group: boolean;
  at: string;
  mine: boolean;
  sender?: string;
  snippet?: string;
  /** What the message holds, in a line, when there is no snippet. */
  line: string;
}

/** Rows of two lists merged, newest first, each once. */
export function mergeFound(a: FoundRow[], b: FoundRow[]): FoundRow[] {
  const seen = new Set<string>();
  const out: FoundRow[] = [];
  for (const r of [...a, ...b].sort((x, y) => Date.parse(y.at) - Date.parse(x.at))) {
    if (seen.has(r.key)) continue;
    seen.add(r.key);
    out.push(r);
  }
  return out;
}

/** The rows of several paged lists to show, merged newest first: only
 * those no older than the oldest row read so far of any list that still
 * has more. An older row could otherwise show above rows of another list
 * not yet read (and Show more would put rows above ones already shown);
 * the rest are held back until that list pages past them. */
export function shownFound(lists: { rows: FoundRow[]; more: boolean }[]): FoundRow[] {
  let frontier: number | undefined;
  for (const l of lists) {
    if (!l.more || l.rows.length === 0) continue;
    const oldest = Math.min(...l.rows.map((r) => Date.parse(r.at)));
    if (frontier === undefined || oldest > frontier) frontier = oldest;
  }
  const all = lists.reduce<FoundRow[]>((acc, l) => mergeFound(acc, l.rows), []);
  return frontier === undefined ? all : all.filter((r) => Date.parse(r.at) >= (frontier as number));
}

/** GET /v1/me/groups/search's address. */
export function groupSearchURL(q: string, group?: string, before?: string): string {
  const p = new URLSearchParams({ q: q.trim() });
  if (group) p.set("group", group);
  if (before) p.set("before", before);
  return `/v1/me/groups/search?${p.toString()}`;
}

/** GET /v1/me/groups/starred's address. */
export const groupStarredURL = (before?: string) => (before ? `/v1/me/groups/starred?before=${encodeURIComponent(before)}` : "/v1/me/groups/starred");

/** Whether a group just made opens at once: when nobody's add needs
 * saying and its photo, if any, was saved. Otherwise the sheet stays to
 * say so (a failed photo is never silently lost). */
export function newGroupOpensAtOnce(added: unknown[], photoFailed: boolean): boolean {
  return added.length === 0 && !photoFailed;
}

/** Whether "Delete for everyone" is offered for a message of a group:
 * only to a member now (not after leaving), for their own recent one. */
export function mayDeleteGroupForEveryone(g: Pick<Group, "left">, m: Parameters<typeof mayDeleteForEveryone>[0], now: number = Date.now()): boolean {
  return !g.left && mayDeleteForEveryone(m, now);
}
