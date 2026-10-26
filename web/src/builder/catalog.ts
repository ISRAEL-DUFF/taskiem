// What can be added at a + ("What happens next?"): every step an owner can
// add, in their words, grouped, and found by searching. Each makes the
// step with the smallest setup that works; its window does the rest.
import type { Step } from "@sdk/wd";
import { newStep } from "../lib/graph";
import { newChoice } from "../lib/flow";
import { lookOf, type Tone } from "./look";

export interface Pick {
  key: string;
  title: string;
  sub: string;
  icon: string;
  tone: Tone;
  group: string;
  /** What a new step's id is made from: short words for expressions (steps.code.output). */
  base: string;
  /** The new step, with this id. */
  make: (id: string) => Step;
}

export const GROUPS: { name: string; what: string; icon: string; tone: Tone }[] = [
  { name: "Payments", what: "Payment links, pay a bank account, check a payment, refunds", icon: "card", tone: "warn" },
  { name: "Messages", what: "WhatsApp, Telegram, SMS and email", icon: "chat", tone: "ok" },
  { name: "Tables and sheets", what: "Your tables and Google Sheets: find, add or change a row", icon: "tables", tone: "ok" },
  { name: "People", what: "Ask for approval, ask someone to fill in a form", icon: "members", tone: "brand" },
  { name: "Decide and repeat", what: "If, do these together, for each, wait", icon: "split", tone: "info" },
  { name: "AI", what: "Write a reply, read a receipt, sort a message", icon: "sparkle", tone: "brand" },
  { name: "Invoices and orders", what: "Make an invoice, an order, a school's fees", icon: "invoices", tone: "warn" },
  { name: "Your other workflows", what: "Run one of your workflows, or a ready one", icon: "workflows", tone: "brand" },
  { name: "Other services", what: "Every other service Taskiem connects to", icon: "connections", tone: "muted" },
  { name: "Advanced", what: "Call a web address, run code, change the data", icon: "code", tone: "muted" },
];

interface ConnectorLike {
  ref: string;
  id: string;
  name: string;
  category?: string;
  actions: Record<string, { title?: string; class?: string }>;
}
interface CapabilityLike {
  name: string;
  title: string;
  description?: string;
  kind?: string;
}

const MESSAGES = new Set(["gmail", "email"]);
const TABLES = new Set(["googlesheets", "xenibase", "postgres", "mysql"]);
const BUSINESS = new Set(["invoices", "storefront", "schools"]);
const HIDDEN = new Set(["internal", "builtin", "form"]);

function groupOf(c: ConnectorLike): string | null {
  if (HIDDEN.has(c.id)) return null;
  if (c.category === "messaging" || MESSAGES.has(c.id)) return "Messages";
  if (TABLES.has(c.id) || c.category === "database") return "Tables and sheets";
  if (BUSINESS.has(c.id)) return "Invoices and orders";
  // Payment services are reached through Payments (any account does it);
  // their own actions are under Other services, by name.
  return "Other services";
}

const named = (s: Step, name: string): Step => ({ ...s, name }) as Step;

/** Everything that can be added, from what this Taskiem connects to. */
export function catalog(connectors: ConnectorLike[], capabilities: CapabilityLike[]): Pick[] {
  const out: Pick[] = [];
  for (const c of capabilities) {
    if (c.kind === "event") continue;
    out.push({ key: `cap:${c.name}`, title: c.title, sub: c.description ?? "With whichever payments account you connected", icon: "card", tone: "warn", group: "Payments", base: c.name, make: (id) => named({ id, type: "capability", uses: c.name, input: {} } as Step, c.title) });
  }
  for (const c of connectors) {
    const group = groupOf(c);
    if (!group) continue;
    for (const [action, a] of Object.entries(c.actions)) {
      const step = (id: string) => ({ id, type: "connector", connector: c.ref, action, input: {} }) as Step;
      const look = lookOf(step("x"), [c]);
      out.push({ key: `con:${c.ref}:${action}`, title: `${a.title || action}`, sub: c.name, icon: look.icon, tone: look.tone, group, base: action, make: (id) => named(step(id), `${a.title || action}`) });
    }
  }
  const own: [string, string, string, Step["type"] | "choice", string, string, Tone][] = [
    ["People", "Ask for approval", "Wait until someone with a role says yes or no", "approval", "approvals", "approval", "brand"],
    ["People", "Ask someone to fill in a form", "A link by WhatsApp, SMS or email; the answers come back here", "form", "forms", "form", "brand"],
    ["Decide and repeat", "If", "Go one way or another: Yes or No", "choice", "split", "check", "info"],
    ["Decide and repeat", "Do these together", "Steps that run at the same time", "parallel", "split", "together", "info"],
    ["Decide and repeat", "For each", "The same steps for every item in a list", "foreach", "loop", "each", "info"],
    ["Decide and repeat", "Wait", "An hour, a day, or until a time", "wait", "clock", "wait", "info"],
    ["AI", "Ask the AI", "Write a reply, read a receipt, sort a message", "ai", "sparkle", "ask_ai", "brand"],
    ["Your other workflows", "Run another of your workflows", "It runs as a step here, and this one waits for it", "subflow", "workflows", "run_workflow", "brand"],
    ["Your other workflows", "Use a ready workflow", "One of Taskiem's ready workflows, as a step here", "template", "templates", "ready_workflow", "brand"],
    ["Advanced", "Call a web address", "Send or fetch data from another system", "http", "code", "call", "muted"],
    ["Advanced", "Run code", "JavaScript or Python, for what no step does", "code", "code", "code", "muted"],
    ["Advanced", "Change the data", "Shape values for the steps after it", "transform", "code", "change", "muted"],
    ["Advanced", "Answer the caller", "What a website that started it gets back", "respond", "code", "answer", "muted"],
    ["Advanced", "Wait to be called back", "Until another system calls this run's own address", "callback", "inbox", "callback", "muted"],
    ["Advanced", "Wait for an event", "Until a payment service says something happened", "signal", "inbox", "wait_event", "muted"],
  ];
  for (const [group, title, sub, type, icon, base, tone] of own) {
    out.push({ key: `own:${type}`, title, sub, icon, tone, group, base, make: (id) => named(type === "choice" ? newChoice(id) : newStep(type, id), title === "If" ? "Is it so?" : title) });
  }
  return out;
}

/** The three to offer first: a message, an approval and a payment link, as far as this Taskiem has them. */
export function suggested(all: Pick[]): Pick[] {
  const want = [
    (p: Pick) => p.group === "Messages" && /whatsapp/i.test(p.sub) && /^send a (text )?message$/i.test(p.title),
    (p: Pick) => p.group === "Messages" && /whatsapp/i.test(p.sub) && /send/i.test(p.title),
    (p: Pick) => p.key === "own:approval",
    (p: Pick) => p.group === "Payments" && /link|payment page/i.test(p.title),
    (p: Pick) => p.group === "Messages" && /send/i.test(p.title),
    (p: Pick) => p.key === "own:choice",
  ];
  const out: Pick[] = [];
  for (const w of want) {
    const p = all.find((x) => w(x) && !out.includes(x));
    // One message at most: the second rule is the first's fallback.
    if (p && out.length < 3 && !(p.group === "Messages" && out.some((x) => x.group === "Messages"))) out.push(p);
  }
  return out;
}

/** What matches every word searched, in its title, service or group. */
export function search(all: Pick[], q: string): Pick[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [];
  return all.filter((p) => {
    const hay = `${p.title} ${p.sub} ${p.group}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

/** An id for a new step from its title, not used yet: send_a_message, send_a_message_2. */
export function idFrom(title: string, taken: Set<string>): string {
  const base =
    title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .replace(/^(\d)/, "s_$1")
      .slice(0, 40)
      .replace(/_+$/, "") || "step";
  if (!taken.has(base)) return base;
  for (let i = 2; ; i++) if (!taken.has(`${base}_${i}`)) return `${base}_${i}`;
}
