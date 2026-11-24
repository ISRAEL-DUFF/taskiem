// A customer's answers kept in their chat with the business (build plan
// H6, g-ct-webkeep; the app's TheirRuns, mobile/lib/chat/my_chat_their_tools.dart):
// after Yes on one of their tools the sheet closes and the run goes into
// the conversation in its place, its answer read until the run ends, and
// kept in this browser with the chat, so a reload shows it again. Only the
// run's id, the tool's command and name and when it started are kept:
// never what the person typed (which may be personal), and never the
// answer, which is read again from the server each time. Signing out, or
// someone else signing in in the tab, forgets them (lib/tabOwner.ts).

/** A run of one of their tools, as the chat keeps it. */
export interface KeptTheirRun {
  /** The run's id. */
  id: string;
  command: string;
  name: string;
  /** When it was started, as an ISO time: its place in the conversation. */
  at: string;
}

const PREFIX = "taskiem_their_tool_runs:";

/** How many runs a chat keeps: the latest. */
export const THEIR_RUNS_MAX = 30;

const key = (chat: string) => `${PREFIX}${chat}`;

/** The runs a chat keeps, oldest first; none when none could be read. */
export function keptTheirRuns(chat: string, store: Pick<Storage, "getItem"> | undefined = safeStorage()): KeptTheirRun[] {
  try {
    const raw = store?.getItem(key(chat));
    if (!raw) return [];
    const list: unknown = JSON.parse(raw);
    if (!Array.isArray(list)) return [];
    // Only what is kept: an older page's typed values are dropped.
    return list.filter(isKept).map(({ id, command, name, at }) => ({ id, command, name, at }));
  } catch {
    // Kept by an older page, or not JSON: the conversation stands without them.
    return [];
  }
}

/** Keeps run r with the chat's runs (once; the latest THEIR_RUNS_MAX),
 * and gives the list now. */
export function keepTheirRun(chat: string, r: KeptTheirRun, store: Pick<Storage, "getItem" | "setItem"> | undefined = safeStorage()): KeptTheirRun[] {
  const list = keptTheirRuns(chat, store);
  if (!list.some((x) => x.id === r.id)) list.push({ id: r.id, command: r.command, name: r.name, at: r.at });
  const kept = list.slice(-THEIR_RUNS_MAX);
  try {
    store?.setItem(key(chat), JSON.stringify(kept));
  } catch {
    // Full, or turned off: kept while the page is open.
  }
  return kept;
}

/** The conversation's messages and the kept runs in one list by time.
 * While older messages are still to load, only runs at or after the
 * oldest message shown are placed, so none sits above messages not yet
 * read in. */
export function withTheirRuns<T extends { id: string; created_at: string }>(
  messages: T[],
  runs: KeptTheirRun[],
  hasOlder: boolean,
  asItem: (r: KeptTheirRun) => T,
): T[] {
  if (runs.length === 0) return messages;
  const first = messages[0]?.created_at;
  const placed = runs.filter((r) => !hasOlder || !first || r.at >= first).map(asItem);
  const out: T[] = [...messages];
  for (const it of placed) {
    let i = out.length;
    while (i > 0 && (out[i - 1]?.created_at ?? "") > it.created_at) i--;
    out.splice(i, 0, it);
  }
  return out;
}

function isKept(x: unknown): x is KeptTheirRun {
  if (!x || typeof x !== "object") return false;
  const r = x as Record<string, unknown>;
  return typeof r.id === "string" && typeof r.command === "string" && typeof r.name === "string" && typeof r.at === "string";
}

/** Forgets every chat's kept runs on this browser (signing out, or
 * someone else signing in). */
export function forgetTheirRuns(store: Pick<Storage, "length" | "key" | "removeItem"> | undefined = safeStorage()): void {
  try {
    if (!store) return;
    const keys: string[] = [];
    for (let i = 0; i < store.length; i++) {
      const k = store.key(i);
      if (k?.startsWith(PREFIX)) keys.push(k);
    }
    for (const k of keys) store.removeItem(k);
  } catch {
    // Storage blocked: nothing was kept.
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof localStorage === "undefined" ? undefined : localStorage;
  } catch {
    return undefined;
  }
}
