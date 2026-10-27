// The public connector catalogue (docs/connector-submissions.md): what a
// version asks a tenant to consent to, and how an upgrade changes it.

export interface Consent {
  hosts: string[];
  writes: Record<string, string>;
}

export interface CatalogueVersion {
  id: string;
  version: string;
  ref: string;
  name: string;
  description: string;
  category: string;
  hosts: string[];
  actions: Record<string, string>;
  pii: string[];
  triggers: string[];
  publisher: string;
  publisher_name: string;
  licence: string;
  source_url?: string;
  digest: string;
  state?: string;
  published_at?: string;
  revoke_reason?: string;
  consent: Consent;
}

export interface CatalogueEntry {
  id: string;
  name: string;
  description: string;
  category: string;
  publisher: string;
  publisher_name: string;
  versions: CatalogueVersion[]; // newest first
  installed: Record<string, string>; // major -> version
}

export interface Install {
  connector: string;
  major: number;
  ref: string;
  version: string;
  state: string; // published, revoked, unavailable
  revoke_reason?: string;
  latest?: string;
  consent: Consent;
  installed_by: string;
  installed_at: string;
  updated_at?: string;
}

export interface ClassChange {
  action: string;
  from: string;
  to: string;
}

export interface Diff {
  from: string;
  to: string;
  added_hosts?: string[];
  removed_hosts?: string[];
  added_actions?: string[];
  removed_actions?: string[];
  class_changes?: ClassChange[];
  added_writes?: string[];
  removed_fields?: string[];
  added_pii?: string[];
  removed_pii?: string[];
  needs_consent: boolean;
}

export function major(version: string): string {
  return version.split(".")[0] ?? version;
}

/** The newest version of each major, newest major first. */
export function newestPerMajor(
  versions: CatalogueVersion[],
): CatalogueVersion[] {
  const seen = new Set<string>();
  const out: CatalogueVersion[] = [];
  for (const v of versions) {
    const m = major(v.version);
    if (!seen.has(m)) {
      seen.add(m);
      out.push(v);
    }
  }
  return out;
}

const classWords: Record<string, string> = {
  idempotent_write: "changes things; safe to retry",
  reconcilable_write: "changes things; checked before any retry",
  unsafe_write: "changes things; never retried after a timeout",
};

/** One line per write action, saying what its class means. */
export function writeLines(c: Consent): string[] {
  return Object.keys(c.writes)
    .sort()
    .map((a) => {
      const cls = c.writes[a] ?? "";
      return `${a}: ${classWords[cls] ?? cls}`;
    });
}

/** What an upgrade changes, in words, most important first. */
export function diffLines(d: Diff): string[] {
  const out: string[] = [];
  for (const h of d.added_hosts ?? []) out.push(`New host: ${h}`);
  for (const c of d.class_changes ?? []) out.push(`${c.action} changes from ${c.from.replaceAll("_", " ")} to ${c.to.replaceAll("_", " ")}`);
  for (const a of d.added_writes ?? []) out.push(`New action that changes things: ${a}`);
  for (const p of d.added_pii ?? []) out.push(`Now treated as personal data: ${p}`);
  for (const a of (d.added_actions ?? []).filter((x) => !(d.added_writes ?? []).includes(x))) out.push(`New action: ${a}`);
  for (const a of d.removed_actions ?? []) out.push(`Removed action: ${a}`);
  for (const f of d.removed_fields ?? []) out.push(`Removed field: ${f}`);
  for (const h of d.removed_hosts ?? []) out.push(`No longer reaches: ${h}`);
  if (out.length === 0) out.push("No change to hosts, actions or classes");
  return out;
}

// --- Taskiem's own connectors (decision 0044) ---

export type Kind = "rail" | "product" | "tool";

/** The groups the catalogue shows Taskiem's own connectors in, in order. */
export const KINDS: { kind: Kind; title: string; help: string }[] = [
  { kind: "rail", title: "Payments and banking", help: "Services that move money. Taskiem builds payments, payouts and payroll on them." },
  { kind: "product", title: "Apps", help: "Apps that run a whole job themselves. Taskiem connects to them and works around them." },
  { kind: "tool", title: "Messages, records and other tools", help: "Sheets, email, chat and other services your workflows use." },
];

interface Listed {
  id: string;
  name: string;
  kind?: Kind;
  category?: string;
  description?: string;
  capabilities?: string[];
}

/** What a rail's capabilities (schemas/capabilities-v1.json) let a
 * business do, in the order the catalogue says them. */
const CAPABILITY_WORDS: [string, string][] = [
  ["pay_bank", "pay bank accounts"],
  ["pay_wallet", "pay wallets"],
  ["pay_mobile_money", "pay mobile money"],
  ["pay_many", "pay a list in one batch"],
  ["collect_link", "take payments by link"],
  ["collect_push", "take payments by phone prompt"],
  ["collect_transfer", "take payments by bank transfer"],
  ["virtual_account", "give customers account numbers"],
  ["refund", "refund payments"],
  ["balance", "check the balance"],
  ["resolve_account", "check account names"],
  ["books_invoice", "record invoices in the books"],
  ["books_payment", "record payments in the books"],
  ["books_sale", "record the day's takings"],
  ["books_expense", "record expenses"],
  ["books_bill", "record supplier bills"],
];

/** "Can pay bank accounts, …": what a rail can do, or "" for none. The
 * lookups (get_payout, get_payment, list_banks) go without saying. */
export function capabilityLine(capabilities: string[] | undefined): string {
  const words = CAPABILITY_WORDS.filter(([c]) => capabilities?.includes(c)).map(([, w]) => w);
  if (words.length === 0) return "";
  const last = words.pop();
  return `Can ${words.length > 0 ? `${words.join(", ")} and ${last}` : last}.`;
}

/** Taskiem's own connectors (not an organisation's own `x_` ones nor
 * installed catalogue `p_` ones), matching a search, by kind, each
 * sorted by name. */
export function builtInByKind<T extends Listed>(connectors: T[], search: string): Record<Kind, T[]> {
  const q = search.trim().toLowerCase();
  const out: Record<Kind, T[]> = { rail: [], product: [], tool: [] };
  for (const c of connectors) {
    if (c.id.startsWith("x_") || c.id.startsWith("p_")) continue;
    const text = `${c.name} ${c.id} ${c.description ?? ""} ${(c.category ?? "").replaceAll("_", " ")} ${capabilityLine(c.capabilities)}`.toLowerCase();
    if (q && !text.includes(q)) continue;
    out[c.kind ?? "tool"].push(c);
  }
  for (const k of Object.keys(out) as Kind[]) out[k].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
  return out;
}
